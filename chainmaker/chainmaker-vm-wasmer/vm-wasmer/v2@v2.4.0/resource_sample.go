/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// ResourceSnapshot is a point-in-time view of process / Go runtime resource usage.
// Threads and VmRSS come from /proc (OS truth for native/Tokio threads);
// GoAlloc/GoSys come from runtime.MemStats (Go heap only, misses most CGO/Wasmer RSS).
type ResourceSnapshot struct {
	Label        string
	Time         time.Time
	Threads      int
	VmRSSKB      int64
	VmSizeKB     int64
	AnonRSSKB    int64 // from smaps_rollup Anonymous; -1 if unavailable
	GoAllocMB    float64
	GoSysMB      float64
	NumGC        uint32
	NumGoroutine int
}

// Delta vs a baseline snapshot (positive means growth).
type ResourceDelta struct {
	Label        string
	Threads      int
	VmRSSKB      int64
	VmSizeKB     int64
	AnonRSSKB    int64
	GoAllocMB    float64
	GoSysMB      float64
	NumGoroutine int
	Elapsed      time.Duration
}

// SampleResource reads /proc/self/status + runtime.MemStats.
func SampleResource(label string) ResourceSnapshot {
	runtime.GC() // optional: comment out if you want to observe unreclaimed growth
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	s := ResourceSnapshot{
		Label:        label,
		Time:         time.Now(),
		Threads:      -1,
		VmRSSKB:      -1,
		VmSizeKB:     -1,
		AnonRSSKB:    -1,
		GoAllocMB:    float64(mem.Alloc) / 1024 / 1024,
		GoSysMB:      float64(mem.Sys) / 1024 / 1024,
		NumGC:        mem.NumGC,
		NumGoroutine: runtime.NumGoroutine(),
	}
	fillProcStatus(&s)
	fillSmapsRollup(&s)
	return s
}

// SampleResourceNoGC is the same as SampleResource but does not force GC.
func SampleResourceNoGC(label string) ResourceSnapshot {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	s := ResourceSnapshot{
		Label:        label,
		Time:         time.Now(),
		Threads:      -1,
		VmRSSKB:      -1,
		VmSizeKB:     -1,
		AnonRSSKB:    -1,
		GoAllocMB:    float64(mem.Alloc) / 1024 / 1024,
		GoSysMB:      float64(mem.Sys) / 1024 / 1024,
		NumGC:        mem.NumGC,
		NumGoroutine: runtime.NumGoroutine(),
	}
	fillProcStatus(&s)
	fillSmapsRollup(&s)
	return s
}

func fillProcStatus(s *ResourceSnapshot) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "Threads:"):
			fmt.Sscanf(line, "Threads: %d", &s.Threads)
		case strings.HasPrefix(line, "VmRSS:"):
			fmt.Sscanf(line, "VmRSS: %d kB", &s.VmRSSKB)
		case strings.HasPrefix(line, "VmSize:"):
			fmt.Sscanf(line, "VmSize: %d kB", &s.VmSizeKB)
		}
	}
}

func fillSmapsRollup(s *ResourceSnapshot) {
	data, err := os.ReadFile("/proc/self/smaps_rollup")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Anonymous:") {
			fmt.Sscanf(line, "Anonymous: %d kB", &s.AnonRSSKB)
			return
		}
	}
}

func (s ResourceSnapshot) Sub(base ResourceSnapshot) ResourceDelta {
	return ResourceDelta{
		Label:        s.Label,
		Threads:      s.Threads - base.Threads,
		VmRSSKB:      s.VmRSSKB - base.VmRSSKB,
		VmSizeKB:     s.VmSizeKB - base.VmSizeKB,
		AnonRSSKB:    s.AnonRSSKB - base.AnonRSSKB,
		GoAllocMB:    s.GoAllocMB - base.GoAllocMB,
		GoSysMB:      s.GoSysMB - base.GoSysMB,
		NumGoroutine: s.NumGoroutine - base.NumGoroutine,
		Elapsed:      s.Time.Sub(base.Time),
	}
}

func (s ResourceSnapshot) String() string {
	return fmt.Sprintf("%s threads=%d VmRSS=%.2fMB Anon=%.2fMB VmSize=%.2fMB goAlloc=%.2fMB goSys=%.2fMB goroutine=%d numGC=%d",
		s.Label, s.Threads,
		float64(s.VmRSSKB)/1024, float64(s.AnonRSSKB)/1024, float64(s.VmSizeKB)/1024,
		s.GoAllocMB, s.GoSysMB, s.NumGoroutine, s.NumGC)
}

func (d ResourceDelta) String() string {
	return fmt.Sprintf("Δ%-28s threads=%+d VmRSS=%+.2fMB Anon=%+.2fMB VmSize=%+.2fMB goAlloc=%+.2fMB goSys=%+.2fMB goroutine=%+d elapsed=%v",
		d.Label, d.Threads,
		float64(d.VmRSSKB)/1024, float64(d.AnonRSSKB)/1024, float64(d.VmSizeKB)/1024,
		d.GoAllocMB, d.GoSysMB, d.NumGoroutine, d.Elapsed)
}

// ResourceProbe records staged snapshots and prints absolute + delta vs previous / baseline.
type ResourceProbe struct {
	Baseline ResourceSnapshot
	Steps    []ResourceSnapshot
}

func NewResourceProbe(baselineLabel string) *ResourceProbe {
	base := SampleResource(baselineLabel)
	return &ResourceProbe{Baseline: base, Steps: []ResourceSnapshot{base}}
}

func (p *ResourceProbe) Mark(label string) ResourceSnapshot {
	s := SampleResourceNoGC(label)
	p.Steps = append(p.Steps, s)
	return s
}

func (p *ResourceProbe) Report() string {
	var b strings.Builder
	b.WriteString("=== resource probe report ===\n")
	b.WriteString("absolute:\n")
	for _, s := range p.Steps {
		b.WriteString("  ")
		b.WriteString(s.String())
		b.WriteByte('\n')
	}
	b.WriteString("delta vs previous step:\n")
	for i := 1; i < len(p.Steps); i++ {
		d := p.Steps[i].Sub(p.Steps[i-1])
		b.WriteString("  ")
		b.WriteString(d.String())
		b.WriteByte('\n')
	}
	b.WriteString("delta vs baseline:\n")
	for i := 1; i < len(p.Steps); i++ {
		d := p.Steps[i].Sub(p.Baseline)
		b.WriteString("  ")
		b.WriteString(d.String())
		b.WriteByte('\n')
	}
	return b.String()
}
