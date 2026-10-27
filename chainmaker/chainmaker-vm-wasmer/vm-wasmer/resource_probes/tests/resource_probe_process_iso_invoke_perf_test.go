/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wasmer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
)

// TestProcessIsolationInvokePerf compares Invoke latency:
//
//	in-process vs warm process isolation (with sys_call IPC).
//
//	go test -run TestProcessIsolationInvokePerf -v -count=1 -timeout 20m
//	go test -run TestProcessIsolationInvokePerf -v -count=1 -args -proc_iso_probe_wasm=./testdata/bigInput-go.wasm
func TestProcessIsolationInvokePerf(t *testing.T) {
	ensureProcessWorkerBin(t)

	warmup := 5
	n := 30
	wasmPath := *procIsoProbeWasm

	logPath := fmt.Sprintf("../resource_probes/logs/resource_probe_process_iso_invoke_perf_%s.log",
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

	contractName, method, params := invokePerfCase(wasmPath)
	logf("=== TestProcessIsolationInvokePerf ===")
	logf("time=%s wasm=%s contract=%s method=%s warmup=%d measure_n=%d",
		time.Now().Format(time.RFC3339), wasmPath, contractName, method, warmup, n)
	logf("")

	run := func(label, isolation string) []float64 {
		t.Setenv(envProcessIsolation, isolation)
		if isolation == "1" {
			t.Setenv(envProcessRSSLimit, "2048")
		}
		logger := logger2.GetLogger("invoke_perf_" + label)
		contract := &commonPb.Contract{
			Name:        contractName,
			Version:     ContractVersion,
			RuntimeType: commonPb.RuntimeType_WASMER,
		}
		t0 := time.Now()
		pool, err := newVmPool(contract, byteCode, logger)
		poolMS := time.Since(t0).Seconds() * 1000
		if err != nil {
			t.Fatalf("[%s] newVmPool: %v", label, err)
		}
		defer pool.close()
		if isolation == "1" && !pool.processIsolation {
			t.Fatalf("[%s] expected processIsolation", label)
		}
		if isolation != "1" && pool.processIsolation {
			t.Fatalf("[%s] unexpected processIsolation", label)
		}

		rt := RuntimeInstance{pool: pool, log: logger, chainId: ChainId}
		logf("--- %s isolation=%s ---", label, isolation)
		logf("  newVmPool_ms=%.3f processIsolation=%v", poolMS, pool.processIsolation)

		samples := make([]float64, 0, n)
		for i := 0; i < warmup+n; i++ {
			p := cloneParams(params)
			fillingBaseParams(p)
			txCtx := prepareTxSimContext(ChainId, BlockVersion, contractName, method, p, SnapshotMock{cache: map[string][]byte{}})
			t1 := time.Now()
			cr, _ := rt.Invoke(contract, method, byteCode, p, txCtx, 0)
			ms := time.Since(t1).Seconds() * 1000
			if cr.Code != 0 {
				t.Fatalf("[%s] invoke #%d failed code=%d msg=%s", label, i+1, cr.Code, cr.Message)
			}
			if i >= warmup {
				samples = append(samples, ms)
			}
		}
		logInvokeStats(logf, label, samples)
		return samples
	}

	inproc := run("in-process", "0")
	proc := run("process-iso", "1")

	logf("")
	logf("=== COMPARISON (exclude warmup) ===")
	logf("in-process   avg=%.3fms  p50=%.3f  p95=%.3f  n=%d",
		avgF(inproc), percentile(inproc, 50), percentile(inproc, 95), len(inproc))
	logf("process-iso  avg=%.3fms  p50=%.3f  p95=%.3f  n=%d",
		avgF(proc), percentile(proc, 50), percentile(proc, 95), len(proc))
	if avgF(inproc) > 0 {
		logf("overhead_ratio avg(process)/avg(inproc)=%.2fx  delta_ms=%.3f",
			avgF(proc)/avgF(inproc), avgF(proc)-avgF(inproc))
	}
	logf("log_file=%s", logPath)
	t.Logf("invoke perf log: %s", logPath)
}

func invokePerfCase(wasmPath string) (name, method string, params map[string][]byte) {
	base := filepath.Base(wasmPath)
	switch {
	case strings.Contains(base, "bigInput"):
		return "bigInput", "inputSize", map[string][]byte{
			"data": make([]byte, 128),
		}
	default:
		// fact-go and similar
		return "fact", "save", map[string][]byte{
			"file_hash": []byte("perf_hash"),
			"file_name": []byte("perf_name"),
			"time":      []byte("1"),
		}
	}
}

func cloneParams(m map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

func avgF(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	if p <= 0 {
		return cp[0]
	}
	if p >= 100 {
		return cp[len(cp)-1]
	}
	idx := int(float64(len(cp)-1) * p / 100.0)
	return cp[idx]
}

func logInvokeStats(logf func(string, ...interface{}), label string, samples []float64) {
	if len(samples) == 0 {
		logf("  %s: no samples", label)
		return
	}
	lo, hi := samples[0], samples[0]
	for _, x := range samples[1:] {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	logf("  invoke_n=%d avg=%.3fms min=%.3f max=%.3f p50=%.3f p95=%.3f",
		len(samples), avgF(samples), lo, hi, percentile(samples, 50), percentile(samples, 95))
}
