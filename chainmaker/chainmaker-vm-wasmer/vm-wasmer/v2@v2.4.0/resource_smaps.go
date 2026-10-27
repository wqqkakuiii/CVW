/*
Copyright (C) BABEC. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var vmaHeaderRe = regexp.MustCompile(`^[0-9a-fA-F]+-[0-9a-fA-F]+\s+[rwxps-]+`)

// SmapsVMA is one /proc/self/smaps mapping with rollup counters.
type SmapsVMA struct {
	Key      string // "start-end perms"
	Perms    string
	Path     string
	SizeKB   int64
	RssKB    int64
	AnonKB   int64
	PssKB    int64
	Category string
}

// SmapsRollup aggregates VMAs by category.
type SmapsRollup struct {
	Label      string
	TotalRSSKB int64
	TotalAnonKB int64
	ByCategory map[string]int64 // category -> rss_kb
	VMAs       []SmapsVMA
}

func ReadSmapsRollup(label string) (SmapsRollup, error) {
	f, err := os.Open("/proc/self/smaps")
	if err != nil {
		return SmapsRollup{}, err
	}
	defer f.Close()

	out := SmapsRollup{
		Label:      label,
		ByCategory: make(map[string]int64),
	}
	var cur *SmapsVMA
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if vmaHeaderRe.MatchString(line) {
			if cur != nil {
				cur.Category = categorizeVMA(cur)
				out.VMAs = append(out.VMAs, *cur)
				out.ByCategory[cur.Category] += cur.RssKB
				out.TotalRSSKB += cur.RssKB
				out.TotalAnonKB += cur.AnonKB
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			path := ""
			if len(parts) > 5 {
				path = strings.Join(parts[5:], " ")
			}
			cur = &SmapsVMA{
				Key:   parts[0],
				Perms: parts[1],
				Path:  path,
			}
			continue
		}
		if cur == nil {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "Size:":
			cur.SizeKB = v
		case "Rss:":
			cur.RssKB = v
		case "Anonymous:":
			cur.AnonKB = v
		case "Pss:":
			cur.PssKB = v
		}
	}
	if cur != nil {
		cur.Category = categorizeVMA(cur)
		out.VMAs = append(out.VMAs, *cur)
		out.ByCategory[cur.Category] += cur.RssKB
		out.TotalRSSKB += cur.RssKB
		out.TotalAnonKB += cur.AnonKB
	}
	return out, sc.Err()
}

func categorizeVMA(v *SmapsVMA) string {
	path := v.Path
	if path == "" || path == "0" {
		path = ""
	}
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "libwasmer.so") && strings.Contains(v.Perms, "x"):
		return "libwasmer_exec"
	case strings.Contains(lower, "libwasmer.so"):
		return "libwasmer_data"
	case strings.Contains(v.Perms, "x") && path == "":
		return "anon_executable"
	case strings.Contains(v.Perms, "x"):
		return "file_executable"
	case strings.Contains(lower, "[heap]"):
		return "heap"
	case strings.Contains(lower, "[stack]"):
		return "stack"
	case path != "" && strings.Contains(v.Perms, "r"):
		return "file_mmap"
	case v.RssKB >= 4096:
		return "anon_rw_large"
	case strings.Contains(v.Perms, "rw"):
		return "anon_rw_small"
	default:
		return "other"
	}
}

// SmapsDelta is category-level RSS/Anon change (a minus b).
type SmapsDelta struct {
	Label    string
	RSSKB    int64
	AnonKB   int64
	Category map[string]int64 // delta rss per category
	TopVMA   []vmaDelta
}

type vmaDelta struct {
	Key      string
	Category string
	Path     string
	DeltaRSS int64
	AfterRSS int64
}

func DiffSmaps(a, b SmapsRollup) SmapsDelta {
	d := SmapsDelta{
		Label:    a.Label + "_minus_" + b.Label,
		RSSKB:    a.TotalRSSKB - b.TotalRSSKB,
		AnonKB:   a.TotalAnonKB - b.TotalAnonKB,
		Category: make(map[string]int64),
	}
	allCats := make(map[string]struct{})
	for c := range a.ByCategory {
		allCats[c] = struct{}{}
	}
	for c := range b.ByCategory {
		allCats[c] = struct{}{}
	}
	for c := range allCats {
		d.Category[c] = a.ByCategory[c] - b.ByCategory[c]
	}
	bmap := make(map[string]SmapsVMA, len(b.VMAs))
	for _, v := range b.VMAs {
		bmap[v.Key] = v
	}
	for _, av := range a.VMAs {
		bv, ok := bmap[av.Key]
		dr := av.RssKB
		if ok {
			dr = av.RssKB - bv.RssKB
		}
		if dr == 0 {
			continue
		}
		d.TopVMA = append(d.TopVMA, vmaDelta{
			Key: av.Key, Category: av.Category, Path: av.Path,
			DeltaRSS: dr, AfterRSS: av.RssKB,
		})
	}
	sort.Slice(d.TopVMA, func(i, j int) bool {
		adi := d.TopVMA[i].DeltaRSS
		adj := d.TopVMA[j].DeltaRSS
		if adi < 0 {
			adi = -adi
		}
		if adj < 0 {
			adj = -adj
		}
		return adi > adj
	})
	if len(d.TopVMA) > 12 {
		d.TopVMA = d.TopVMA[:12]
	}
	return d
}

func (d SmapsDelta) FormatCategories() string {
	type kv struct {
		k string
		v int64
	}
	var items []kv
	for k, v := range d.Category {
		if v == 0 {
			continue
		}
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		ai, aj := items[i].v, items[j].v
		if ai < 0 {
			ai = -ai
		}
		if aj < 0 {
			aj = -aj
		}
		return ai > aj
	})
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, " %s=%+.2fMB", it.k, float64(it.v)/1024)
	}
	return strings.TrimSpace(b.String())
}

func (d SmapsDelta) FormatTopVMA() string {
	var b strings.Builder
	for _, v := range d.TopVMA {
		fmt.Fprintf(&b, "\n    [%s] Δrss=%+.2fMB after=%.2fMB | %s %s",
			v.Category, float64(v.DeltaRSS)/1024, float64(v.AfterRSS)/1024, v.Key, v.Path)
	}
	return b.String()
}
