/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0
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
	wasmergo "chainmaker.org/chainmaker/vm-wasmer/v2/wasmer-go"
)

var (
	instCloseMetricsWasm = flag.String("inst_close_metrics_wasm", "./testdata/fact-go.wasm",
		"wasm for Instance.Close metrics A/B")
	instCloseMetricsN = flag.Int("inst_close_metrics_n", 8,
		"instances for RSS reclaim arm")
	instCloseMetricsSettle = flag.Duration("inst_close_metrics_settle", 250*time.Millisecond,
		"settle before RSS sample")
	instCloseMetricsLog = flag.String("inst_close_metrics_log", "",
		"metrics log; empty => ./resource_probe_instance_close_metrics_<ts>.log")
	instCloseMetricsCase = flag.String("inst_close_metrics_case", "",
		"worker case: refs_legacy|refs_enhanced|dbl_legacy|dbl_enhanced|rss_legacy|rss_enhanced")
	instCloseMetricsOut = flag.String("inst_close_metrics_out", "",
		"worker writes KEY=VALUE lines here")
)

// TestInstanceCloseMetricsAB measures which enhanced Close items actually improve.
//
// Metrics:
//  1. RSS reclaim after N create→destroy (expect ~same)
//  2. Go ref state after Close (_inner / Exports nil?)
//  3. Double-Close safety (subprocess exit)
//
//	go test -run TestInstanceCloseMetricsAB -v -count=1 -timeout 25m
func TestInstanceCloseMetricsAB(t *testing.T) {
	logPath := *instCloseMetricsLog
	if logPath == "" {
		logPath = fmt.Sprintf("./resource_probe_instance_close_metrics_%s.log", time.Now().Format("20060102_150405"))
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
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

	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir, err := os.MkdirTemp("", "inst_close_metrics_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	wasm := *instCloseMetricsWasm
	n := *instCloseMetricsN
	logf("=== TestInstanceCloseMetricsAB ===")
	logf("time=%s", time.Now().Format(time.RFC3339))
	logf("log_file=%s", logPath)
	logf("wasm=%s n=%d settle=%v", wasm, n, *instCloseMetricsSettle)
	logf("compare: CloseLegacy (原版语义) vs Close (增强版)")
	logf("")

	type kv map[string]string
	runCase := func(caseName string) (kv, int, string) {
		outFile := filepath.Join(tmpDir, caseName+".env")
		cmd := exec.Command("go", "test",
			"-run", "^TestInstanceCloseMetricsWorker$",
			"-count=1", "-timeout", "10m",
			"-args",
			"-inst_close_metrics_case="+caseName,
			"-inst_close_metrics_wasm="+wasm,
			fmt.Sprintf("-inst_close_metrics_n=%d", n),
			fmt.Sprintf("-inst_close_metrics_settle=%s", (*instCloseMetricsSettle).String()),
			"-inst_close_metrics_out="+outFile,
		)
		cmd.Dir = pkgDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
		out, err := cmd.CombinedOutput()
		exit := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			} else {
				exit = -1
			}
		}
		m := kv{}
		if data, rerr := os.ReadFile(outFile); rerr == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || !strings.Contains(line, "=") {
					continue
				}
				p := strings.SplitN(line, "=", 2)
				m[p[0]] = p[1]
			}
		}
		logf("----- case=%s exit=%d -----", caseName, exit)
		logf("%s", string(out))
		return m, exit, string(out)
	}

	// --- 1) Go ref state ---
	logf("### METRIC 1: Close 后 Go 引用是否清空")
	refsL, _, _ := runCase("refs_legacy")
	refsE, _, _ := runCase("refs_enhanced")
	logf("legacy:   native_alive=%s exports_held=%s", refsL["native_alive"], refsL["exports_held"])
	logf("enhanced: native_alive=%s exports_held=%s", refsE["native_alive"], refsE["exports_held"])
	refsImproved := refsE["native_alive"] == "false" && refsE["exports_held"] == "false" &&
		(refsL["native_alive"] == "true" || refsL["exports_held"] == "true")
	logf("improved=%v  (增强版应 native_alive=false exports_held=false；原版仍为 true)", refsImproved)
	logf("")

	// --- 2) Double Close safety ---
	logf("### METRIC 2: 连续 Close 两次是否安全")
	_, exitL, _ := runCase("dbl_legacy")
	_, exitE, _ := runCase("dbl_enhanced")
	logf("legacy double-Close exit=%d (0=ok, non-zero=crash/fail)", exitL)
	logf("enhanced double-Close exit=%d", exitE)
	dblImproved := exitE == 0 && exitL != 0
	dblSameOK := exitE == 0 && exitL == 0
	if dblImproved {
		logf("improved=true  (增强版幂等；原版二次 Close 会再次 wasm_instance_delete → 崩溃)")
	} else if dblSameOK {
		logf("improved=false (两侧都未崩；仍建议增强版，因 _inner 已悬空)")
	} else {
		logf("improved=partial exit_legacy=%d exit_enhanced=%d", exitL, exitE)
	}
	logf("")

	// --- 3) RSS reclaim ---
	logf("### METRIC 3: 创建 N 再销毁后的 RSS 回收量")
	rssL, _, _ := runCase("rss_legacy")
	rssE, _, _ := runCase("rss_enhanced")
	logf("legacy:   reclaim_rss_mb=%s reclaim_anon_mb=%s peak=%s after=%s thr=%s→%s",
		rssL["reclaim_rss_mb"], rssL["reclaim_anon_mb"], rssL["peak_rss_mb"], rssL["after_rss_mb"],
		rssL["peak_thr"], rssL["after_thr"])
	logf("enhanced: reclaim_rss_mb=%s reclaim_anon_mb=%s peak=%s after=%s thr=%s→%s",
		rssE["reclaim_rss_mb"], rssE["reclaim_anon_mb"], rssE["peak_rss_mb"], rssE["after_rss_mb"],
		rssE["peak_thr"], rssE["after_thr"])
	var dReclaim float64
	fmt.Sscanf(rssE["reclaim_rss_mb"], "%f", &dReclaim)
	var dLegacy float64
	fmt.Sscanf(rssL["reclaim_rss_mb"], "%f", &dLegacy)
	logf("delta_reclaim(enhanced-legacy)=%+.2fMB", dReclaim-dLegacy)
	rssImproved := (dReclaim - dLegacy) > 1.0
	logf("improved=%v  (通常为 false：两边都调 wasm_instance_delete)", rssImproved)
	logf("")

	// --- summary table ---
	logf("=== SUMMARY: 哪项有提升 ===")
	logf("%-28s %-12s %-12s %-8s", "metric", "legacy", "enhanced", "提升?")
	logf("%-28s %-12s %-12s %-8s",
		"Close后_inner置nil", refsL["native_alive"]+"→alive", refsE["native_alive"]+"→alive", boolCN(refsImproved))
	logf("%-28s %-12s %-12s %-8s",
		"Close后Exports置nil", refsL["exports_held"], refsE["exports_held"], boolCN(refsImproved))
	logf("%-28s %-12s %-12s %-8s",
		"二次Close安全(exit)", fmt.Sprintf("%d", exitL), fmt.Sprintf("%d", exitE), boolCN(dblImproved || (exitE == 0 && exitL != 0)))
	logf("%-28s %-12s %-12s %-8s",
		"RSS回收MB", rssL["reclaim_rss_mb"], rssE["reclaim_rss_mb"], boolCN(rssImproved))
	logf("%-28s %-12s %-12s %-8s",
		"线程数回收", rssL["after_thr"], rssE["after_thr"], "否")
	logf("")
	logf("log_file=%s", logPath)
	abs, _ := filepath.Abs(logPath)
	logf("log_abs=%s", abs)
	t.Logf("metrics log: %s", abs)
}

func boolCN(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

// TestInstanceCloseMetricsWorker is the subprocess worker.
func TestInstanceCloseMetricsWorker(t *testing.T) {
	c := strings.TrimSpace(*instCloseMetricsCase)
	if c == "" {
		t.Skip("worker only")
	}
	out := *instCloseMetricsOut
	if out == "" {
		t.Fatal("inst_close_metrics_out required")
	}
	var lines []string
	write := func() {
		_ = os.WriteFile(out, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	put := func(k, v string) { lines = append(lines, k+"="+v) }

	switch c {
	case "refs_legacy", "refs_enhanced":
		inst, cleanup, err := newOneProbeInstance(*instCloseMetricsWasm)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if c == "refs_legacy" {
			inst.CloseLegacy()
		} else {
			inst.Close()
		}
		put("native_alive", fmt.Sprintf("%v", inst.NativeAlive()))
		put("exports_held", fmt.Sprintf("%v", inst.ExportsHeld()))
		write()

	case "dbl_legacy", "dbl_enhanced":
		inst, cleanup, err := newOneProbeInstance(*instCloseMetricsWasm)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if c == "dbl_legacy" {
			inst.CloseLegacy()
			inst.CloseLegacy() // expect crash / bad
		} else {
			inst.Close()
			inst.Close() // must be safe
		}
		put("double_close", "ok")
		write()

	case "rss_legacy", "rss_enhanced":
		*instCloseABSettle = *instCloseMetricsSettle
		arm := "enhanced"
		if strings.HasSuffix(c, "legacy") {
			arm = "legacy"
		}
		row, err := runInstanceCloseABArm(
			func(string, ...interface{}) {},
			*instCloseMetricsWasm,
			arm,
			*instCloseMetricsN,
		)
		if err != nil {
			t.Fatal(err)
		}
		put("peak_rss_mb", fmt.Sprintf("%.2f", row.PeakRSSMB))
		put("after_rss_mb", fmt.Sprintf("%.2f", row.AfterShrinkMB))
		put("reclaim_rss_mb", fmt.Sprintf("%.2f", row.ReclaimRSSMB))
		put("reclaim_anon_mb", fmt.Sprintf("%.2f", row.ReclaimAnonMB))
		put("peak_thr", fmt.Sprintf("%d", row.PeakThreads))
		put("after_thr", fmt.Sprintf("%d", row.AfterThreads))
		write()

	default:
		t.Fatalf("unknown case %q", c)
	}
}

func newOneProbeInstance(wasmPath string) (*wasmergo.Instance, func(), error) {
	byteCode, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, nil, err
	}
	name := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
	logger := logger2.GetLogger("inst_close_metrics_" + name)
	contract := &commonPb.Contract{
		Name:        name,
		Version:     "1.0.0",
		RuntimeType: commonPb.RuntimeType_WASMER,
	}
	config := wasmergo.NewConfig()
	config.MaxPagesLimit(512)
	config.CompilerNumThreads(4)
	config.PushMeteringMiddleware(1e19, map[wasmergo.Opcode]uint32{wasmergo.Opcode(0): 0}, map[string]uint32{}, "")
	engine := wasmergo.NewEngineWithConfig(config)
	store := wasmergo.NewStore(engine)
	module, err := wasmergo.NewModule(store, byteCode, logger)
	if err != nil {
		return nil, nil, err
	}
	pool := &vmPool{
		contractId: contract,
		byteCode:   byteCode,
		store:      store,
		module:     module,
		instances:  make(chan *wrappedInstance, 2),
		log:        logger,
	}
	wrapped, err := createInstanceWithProbe(func(string, ...interface{}) {}, pool, NewResourceProbe("m"), name, "m", 0, false)
	if err != nil {
		module.Close()
		store.Close()
		engine.Close()
		return nil, nil, err
	}
	inst := wrapped.wasmInstance
	cleanup := func() {
		if wrapped.wasiEnv != nil {
			wrapped.wasiEnv.Close()
			wrapped.wasiEnv = nil
		}
		// do not Close instance again if already closed by test
		module.Close()
		store.Close()
		engine.Close()
	}
	return inst, cleanup, nil
}
