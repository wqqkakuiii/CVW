/*
Copyright (C) BABEC. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

// TestVMAMemoryBaseTrace correlates wasm export memory.Data() with /proc/self/smaps
// to prove which VMA is WASM linear memory vs unrelated anon_rw_large leaks.
//
//	cd vm-wasmer/v2@v2.4.0
//	ln -sf ../resource_probes/tests/resource_probe_vma_trace_test.go .
//	go test -run TestVMAMemoryBaseTrace -v -count=1 -timeout 10m
func TestVMAMemoryBaseTrace(t *testing.T) {
	wasmPath := "./testdata/fact-go.wasm"
	logPath := fmt.Sprintf("../resource_probes/logs/resource_probe_vma_trace_%s.log",
		time.Now().Format("20060102_150405"))
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := io.MultiWriter(f, probeTestWriter{t})
	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		_, _ = io.WriteString(w, line)
	}

	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	contract := &commonPb.Contract{
		Name:        strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath)),
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}
	logger := logger2.GetLogger("vma_trace")

	logf("=== TestVMAMemoryBaseTrace ===")
	logf("time=%s wasm=%s", time.Now().Format(time.RFC3339), wasmPath)

	cfg := wasmergo.NewConfig()
	cfg.MaxPagesLimit(512)
	cfg.CompilerNumThreads(1)
	cfg.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(cfg)
	store := wasmergo.NewStore(engine)
	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		t.Fatal(err)
	}
	pool := &vmPool{
		contractId: contract, byteCode: byteCode,
		store: store, module: module, engine: engine,
		instances: make(chan *wrappedInstance, 2),
		log:       logger,
	}

	inst, err := createInstanceWithProbe(logf, pool, NewResourceProbe("x"), contract.Name, "trace", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	mem, err := inst.wasmInstance.Exports.GetMemory("memory")
	if err != nil {
		t.Fatal(err)
	}
	data := mem.Data()
	if len(data) == 0 {
		t.Fatal("empty wasm memory")
	}
	memBase := uintptr(unsafe.Pointer(&data[0]))
	logf("wasm_memory_data_ptr=0x%x pages=%d bytes=%d", memBase, mem.Size(), len(data))

	smaps, err := ReadSmapsRollup("after_create")
	if err != nil {
		t.Fatal(err)
	}

	var matched, largeAnon []SmapsVMA
	for _, v := range smaps.VMAs {
		start, end, ok := parseVMARange(v.Key)
		if !ok {
			continue
		}
		if memBase >= start && memBase < end {
			matched = append(matched, v)
		}
		if v.Category == "anon_rw_large" && v.RssKB >= 4096 {
			largeAnon = append(largeAnon, v)
		}
	}

	logf("")
	logf("--- VMA containing memory.Data() ---")
	if len(matched) == 0 {
		logf("(no smaps vma contains memory.Data base)")
	} else {
		for _, v := range matched {
			logf("  MATCH %s rss=%.2fMB anon=%.2fMB cat=%s path=%s",
				v.Key, float64(v.RssKB)/1024, float64(v.AnonKB)/1024, v.Category, v.Path)
		}
	}

	logf("")
	logf("--- anon_rw_large VMAs (rss>=4MB), excluding wasm match ---")
	for _, v := range largeAnon {
		start, _, _ := parseVMARange(v.Key)
		if uintptr(start) == memBase&^0xfff {
			continue
		}
		contains := false
		for _, m := range matched {
			if m.Key == v.Key {
				contains = true
				break
			}
		}
		if contains {
			continue
		}
		logf("  OTHER %s rss=%.2fMB anon=%.2fMB path=%s",
			v.Key, float64(v.RssKB)/1024, float64(v.AnonKB)/1024, v.Path)
	}

	closeWrappedInstanceGoEnhanced(pool, inst)
	time.Sleep(500 * time.Millisecond)

	after, err := ReadSmapsRollup("after_destroy")
	if err != nil {
		t.Fatal(err)
	}
	afterMap := make(map[string]SmapsVMA, len(after.VMAs))
	for _, v := range after.VMAs {
		afterMap[v.Key] = v
	}
	logf("")
	logf("--- large anon VMA persistence after destroy ---")
	for _, v := range largeAnon {
		av, ok := afterMap[v.Key]
		if !ok {
			logf("  GONE %s (was %.2fMB)", v.Key, float64(v.RssKB)/1024)
			continue
		}
		dr := av.RssKB - v.RssKB
		if dr > 512 || dr < -512 {
			logf("  STILL %s before=%.2fMB after=%.2fMB Δ=%.2fMB",
				v.Key, float64(v.RssKB)/1024, float64(av.RssKB)/1024, float64(dr)/1024)
		}
	}

	logf("")
	logf("log_file=%s", logPath)
	t.Logf("vma trace log: %s", logPath)
}

func parseVMARange(key string) (uintptr, uintptr, bool) {
	parts := strings.SplitN(key, " ", 2)
	sep := strings.Index(parts[0], "-")
	if sep < 0 {
		return 0, 0, false
	}
	start, err1 := strconvParseHex(parts[0][:sep])
	end, err2 := strconvParseHex(parts[0][sep+1:])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return uintptr(start), uintptr(end), true
}

func strconvParseHex(s string) (uint64, error) {
	return strconv.ParseUint(s, 16, 64)
}
