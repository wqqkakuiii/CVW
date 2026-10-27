/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0

Create / destroy latency probe — black-box timing in the test only.

What this measures (wall clock)
  1) newVmPool(...)             compile Module (+ serialize / worker path when isolation on)
  2) pool.newInstanceFromModule one Instance create
  3) pool.CloseInstance         one Instance destroy

What this does NOT do
  No instrumentation inside vm_pool.go / process_isolation.go.
  Only time.Since around those calls → end-to-end latency, not per-step
  (CloneStore vs NewInstance vs _start, or IPC vs worker CPU).

Modes
  isolation ON : create ≈ parent IPC create → worker NewInstance (warm worker).
  isolation OFF: create ≈ in-process CloneStore + NewInstance + WASI _start.

Run from vm-wasmer/v2@v2.4.0:

  go test -run TestProcessIsolationWarmPerf -v -count=1 -timeout 20m
  go test -run TestInProcessCreateDestroyPerf -v -count=1 -timeout 20m

Optional: -create_destroy_perf_wasm=./testdata/fact-go.wasm

Logs: ../resource_probes/logs/resource_probe_*_perf_<timestamp>.log
*/

package wasmer

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	logger2 "chainmaker.org/chainmaker/logger/v2"
	commonPb "chainmaker.org/chainmaker/pb-go/v2/common"
)

var createDestroyPerfWasm = flag.String(
	"create_destroy_perf_wasm",
	"./testdata/fact-go.wasm",
	"wasm bytecode for create/destroy latency probes",
)

// ---------------------------------------------------------------------------
// Timing helpers (test-only)
// ---------------------------------------------------------------------------

func millisSince(start time.Time) float64 {
	return time.Since(start).Seconds() * 1000
}

// timingSamples holds millisecond measurements.
type timingSamples []float64

func (s timingSamples) avg() float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

func (s timingSamples) minMax() (min, max float64) {
	if len(s) == 0 {
		return 0, 0
	}
	min, max = s[0], s[0]
	for _, v := range s[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return min, max
}

func (s timingSamples) peak() float64 {
	_, max := s.minMax()
	return max
}

type probeLogger struct {
	w io.Writer
}

func newProbeLogger(t *testing.T, logPath string) (*probeLogger, *os.File, error) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.Create(logPath)
	if err != nil {
		return nil, nil, err
	}
	return &probeLogger{w: io.MultiWriter(f, probeTestWriter{t})}, f, nil
}

func (l *probeLogger) printf(format string, args ...interface{}) {
	line := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = io.WriteString(l.w, line)
}

// ensureCreateDestroyWorkerBin builds wasmer-process-worker if needed.
// Self-contained so this file does not depend on other probe helpers.
func ensureCreateDestroyWorkerBin(t *testing.T) string {
	t.Helper()
	if bin := strings.TrimSpace(os.Getenv(envProcessWorkerBin)); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(pkgDir, "wasmer-process-worker")
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/wasmer-process-worker")
		cmd.Dir = pkgDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
		out, berr := cmd.CombinedOutput()
		if berr != nil {
			t.Fatalf("build worker: %v\n%s", berr, out)
		}
	}
	t.Setenv(envProcessWorkerBin, bin)
	return bin
}

// ---------------------------------------------------------------------------
// Shared create / destroy runner
// ---------------------------------------------------------------------------

type createDestroyPerfConfig struct {
	name       string // log / logger name
	logPrefix  string // file name under resource_probes/logs/
	isolation  bool   // CHAINMAKER_WASMER_PROCESS_ISOLATION
	nPerRound  int
	rounds     int
	needWorker bool
}

type createDestroyPerfResult struct {
	poolInitMS float64
	createMS   timingSamples
	destroyMS  timingSamples
	workerRSS  timingSamples // MB at each create; empty when isolation off
	logPath    string
}

// runCreateDestroyPerf records only outer wall-clock samples around pool APIs.
func runCreateDestroyPerf(t *testing.T, cfg createDestroyPerfConfig) createDestroyPerfResult {
	t.Helper()

	if cfg.needWorker {
		ensureCreateDestroyWorkerBin(t)
	}
	if cfg.isolation {
		t.Setenv(envProcessIsolation, "1")
		t.Setenv(envProcessRSSLimit, "2048")
	} else {
		t.Setenv(envProcessIsolation, "0")
	}

	logPath := fmt.Sprintf("../resource_probes/logs/%s_%s.log",
		cfg.logPrefix, time.Now().Format("20060102_150405"))
	log, f, err := newProbeLogger(t, logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	wasmPath := *createDestroyPerfWasm
	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	logger := logger2.GetLogger(cfg.name)
	contract := &commonPb.Contract{
		Name:        cfg.name,
		Version:     "v1",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}

	log.printf("=== %s ===", cfg.name)
	log.printf("time=%s wasm=%s n_per_round=%d rounds=%d isolation=%v",
		time.Now().Format(time.RFC3339), wasmPath, cfg.nPerRound, cfg.rounds, cfg.isolation)
	if cfg.isolation {
		log.printf("worker_bin=%s rss_limit_mb=2048", os.Getenv(envProcessWorkerBin))
	}
	log.printf("timing=black-box (test-only time.Since; no source Printf probes)")
	log.printf("")

	// Pool init: compile (+ serialize / verify create-destroy under isolation).
	tPool := time.Now()
	pool, err := newVmPool(contract, byteCode, logger)
	poolMS := millisSince(tPool)
	if err != nil {
		t.Fatalf("newVmPool: %v", err)
	}
	defer pool.close()
	log.printf("TIMING pool_init_ms=%.3f", poolMS)

	var createMS, destroyMS, workerRSS timingSamples

	for r := 1; r <= cfg.rounds; r++ {
		log.printf("--- round %d/%d ---", r, cfg.rounds)
		insts := make([]*wrappedInstance, 0, cfg.nPerRound)

		for i := 0; i < cfg.nPerRound; i++ {
			t0 := time.Now()
			inst, cerr := pool.newInstanceFromModule()
			ms := millisSince(t0)
			if cerr != nil {
				t.Fatalf("create r%d #%d: %v", r, i+1, cerr)
			}
			createMS = append(createMS, ms)
			insts = append(insts, inst)

			if cfg.isolation && inst.processWorker != nil {
				st, _ := inst.processWorker.stats()
				rssMB := float64(st.VmRSSKB) / 1024
				workerRSS = append(workerRSS, rssMB)
				tag := ""
				if len(createMS) == 1 {
					tag = "  (first sample)"
				}
				log.printf("TIMING create round=%d i=%02d ms=%.3f active=%d rss_mb=%.2f%s",
					r, i+1, ms, st.Active, rssMB, tag)
			} else {
				log.printf("TIMING create round=%d i=%02d ms=%.3f", r, i+1, ms)
			}
		}

		for i := len(insts) - 1; i >= 0; i-- {
			t0 := time.Now()
			pool.CloseInstance(insts[i])
			ms := millisSince(t0)
			destroyMS = append(destroyMS, ms)
			log.printf("TIMING destroy round=%d i=%02d ms=%.3f", r, cfg.nPerRound-i, ms)
		}
	}

	// Drop first create for a rough steady-state view (warm-up / one-off noise).
	steady := createMS
	if len(steady) > 1 {
		steady = createMS[1:]
	}
	cMin, cMax := createMS.minMax()
	dMin, dMax := destroyMS.minMax()
	sMin, sMax := steady.minMax()

	log.printf("")
	log.printf("=== SUMMARY ===")
	log.printf("pool_init_ms=%.3f", poolMS)
	log.printf("create n=%d avg=%.3fms min=%.3f max=%.3f",
		len(createMS), createMS.avg(), cMin, cMax)
	log.printf("create_steady n=%d avg=%.3fms min=%.3f max=%.3f  (exclude first create)",
		len(steady), steady.avg(), sMin, sMax)
	log.printf("destroy n=%d avg=%.3fms min=%.3f max=%.3f",
		len(destroyMS), destroyMS.avg(), dMin, dMax)
	if len(workerRSS) > 0 {
		log.printf("worker_VmRSS_mb peak=%.2f last=%.2f",
			workerRSS.peak(), workerRSS[len(workerRSS)-1])
	}
	log.printf("log_file=%s", logPath)
	t.Logf("%s log: %s", cfg.name, logPath)

	return createDestroyPerfResult{
		poolInitMS: poolMS,
		createMS:   createMS,
		destroyMS:  destroyMS,
		workerRSS:  workerRSS,
		logPath:    logPath,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestProcessIsolationWarmPerf: isolation ON, one warm worker, many instances.
//
//	go test -run TestProcessIsolationWarmPerf -v -count=1 -timeout 20m
func TestProcessIsolationWarmPerf(t *testing.T) {
	_ = runCreateDestroyPerf(t, createDestroyPerfConfig{
		name:       "TestProcessIsolationWarmPerf",
		logPrefix:  "resource_probe_process_iso_warm_perf",
		isolation:  true,
		nPerRound:  10,
		rounds:     3,
		needWorker: true,
	})
}

// TestInProcessCreateDestroyPerf: isolation OFF baseline (same loop).
//
//	go test -run TestInProcessCreateDestroyPerf -v -count=1 -timeout 20m
func TestInProcessCreateDestroyPerf(t *testing.T) {
	_ = runCreateDestroyPerf(t, createDestroyPerfConfig{
		name:       "TestInProcessCreateDestroyPerf",
		logPrefix:  "resource_probe_inprocess_create_perf",
		isolation:  false,
		nPerRound:  10,
		rounds:     3,
		needWorker: false,
	})
}
