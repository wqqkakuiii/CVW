/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0

Warm process isolation for Wasmer:

  Parent compiles Module once → Serialize → artifact.
  Per-contract worker process: Deserialize Module once, host many Instances.
  Invoke runs in worker; env.sys_call is forwarded to parent via IPC
  (host_call / host_resp) so SimContext / chain state stay in the node.
  CloseInstance → IPC destroy (in-process Close); check VmRSS after destroy.
  If VmRSS >= limit (default 2GiB): mark worker draining; when active==0, kill
  and the next create respawns a fresh worker.

Env:
  CHAINMAKER_WASMER_PROCESS_ISOLATION=1
  WASMER_PROCESS_RSS_LIMIT_MB=2048   (optional)
  WASMER_PROCESS_WORKER_BIN=<path>  (optional; else sibling wasmer-process-worker)
  WASMER_PROCESS_SHM=<path>         (set by parent for worker; /dev/shm mmap)
*/

package wasmer

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"chainmaker.org/chainmaker/common/v2/random/uuid"
	"chainmaker.org/chainmaker/utils/v2"
)

const (
	envProcessIsolation = "CHAINMAKER_WASMER_PROCESS_ISOLATION"
	envProcessWorker    = "WASMER_PROCESS_WORKER"
	envProcessArtifact  = "WASMER_PROCESS_ARTIFACT"
	envProcessWorkerBin = "WASMER_PROCESS_WORKER_BIN"
	envProcessRSSLimit  = "WASMER_PROCESS_RSS_LIMIT_MB"

	procOpReady      = "ready"
	procOpPing       = "ping"
	procOpStats      = "stats"
	procOpCreate     = "create"
	procOpDestroy    = "destroy"
	procOpExit       = "exit"
	procOpInvoke     = "invoke"
	procOpInvokeDone = "invoke_done"
	procOpHostCall   = "host_call"
	procOpHostResp   = "host_resp"

	defaultProcessRSSLimitMB = 2048
)

func processIsolationEnabled() bool {
	v := strings.TrimSpace(os.Getenv(envProcessIsolation))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func processRSSLimitKB() int64 {
	v := strings.TrimSpace(os.Getenv(envProcessRSSLimit))
	if v == "" {
		return int64(defaultProcessRSSLimitMB) * 1024
	}
	mb, err := strconv.ParseInt(v, 10, 64)
	if err != nil || mb <= 0 {
		return int64(defaultProcessRSSLimitMB) * 1024
	}
	return mb * 1024
}

// processMsg is newline-delimited JSON between parent and worker.
type processMsg struct {
	Op    string `json:"op"`
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
	PID   int    `json:"pid,omitempty"`

	InstanceID string `json:"instance_id,omitempty"`
	Active     int    `json:"active,omitempty"`
	Threads    int    `json:"threads,omitempty"`
	VmRSSKB    int64  `json:"vm_rss_kb,omitempty"`
	AnonRSSKB  int64  `json:"anon_rss_kb,omitempty"`

	// invoke (parent → worker)
	Method  string            `json:"method,omitempty"`
	Params  map[string]string `json:"params,omitempty"` // base64 values
	GasUsed uint64            `json:"gas_used,omitempty"`
	CtxPtr  int32             `json:"ctx_ptr,omitempty"`

	// invoke_done (worker → parent)
	InvokeErr    string `json:"invoke_err,omitempty"`
	GasRemaining uint64 `json:"gas_remaining,omitempty"`

	// host_call / host_resp — memory travels via WASMER_PROCESS_SHM (mmap).
	// Legacy base64 fields kept only for fallback when shm is unavailable.
	HeaderB64 string `json:"header_b64,omitempty"`
	BodyB64   string `json:"body_b64,omitempty"`
	MemoryB64 string `json:"memory_b64,omitempty"`
	Ret       int32  `json:"ret,omitempty"`
}

func b64enc(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

func b64dec(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

func paramsToWire(m map[string][]byte) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = b64enc(v)
	}
	return out
}

func paramsFromWire(m map[string]string) (map[string][]byte, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		b, err := b64dec(v)
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	return out, nil
}

func writeProcessMsg(w io.Writer, m processMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func readProcessMsg(r *bufio.Reader, timeout time.Duration) (*processMsg, error) {
	type result struct {
		msg *processMsg
		err error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = bytes.TrimSpace(line)
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var msg processMsg
			if err := json.Unmarshal(line, &msg); err != nil {
				ch <- result{err: fmt.Errorf("decode: %w: %s", err, string(line))}
				return
			}
			ch <- result{msg: &msg}
			return
		}
	}()
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		return res.msg, res.err
	case <-timer.C:
		return nil, errors.New("read process msg timeout")
	}
}

// processWorker is one OS process that can host many Wasmer instances.
type processWorker struct {
	id       string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	shm      *processShm
	mu       sync.Mutex
	active   int
	draining bool
	dead     bool
}

func (w *processWorker) pid() int {
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return 0
	}
	return w.cmd.Process.Pid
}

func (w *processWorker) alive() bool {
	return w != nil && !w.dead && w.cmd != nil && w.cmd.Process != nil
}

func (w *processWorker) call(req processMsg, timeout time.Duration) (*processMsg, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead || w.stdin == nil || w.stdout == nil {
		return nil, errors.New("process worker closed")
	}
	if err := writeProcessMsg(w.stdin, req); err != nil {
		return nil, fmt.Errorf("write worker: %w", err)
	}
	resp, err := readProcessMsg(w.stdout, timeout)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	if !resp.OK {
		return resp, errors.New("worker returned not ok")
	}
	return resp, nil
}

func (w *processWorker) ping() error {
	_, err := w.call(processMsg{Op: procOpPing}, 5*time.Second)
	return err
}

func (w *processWorker) stats() (*processMsg, error) {
	return w.call(processMsg{Op: procOpStats}, 5*time.Second)
}

func (w *processWorker) createInstance() (instanceID string, resp *processMsg, err error) {
	w.mu.Lock()
	if w.draining {
		w.mu.Unlock()
		return "", nil, errors.New("worker draining, refuse create")
	}
	w.mu.Unlock()

	id := uuid.GetUUID()
	resp, err = w.call(processMsg{Op: procOpCreate, InstanceID: id}, 2*time.Minute)
	if err != nil {
		return "", resp, err
	}
	if resp.InstanceID == "" {
		resp.InstanceID = id
	}
	w.mu.Lock()
	w.active++
	w.mu.Unlock()
	return resp.InstanceID, resp, nil
}

func (w *processWorker) destroyInstance(instanceID string) (*processMsg, error) {
	resp, err := w.call(processMsg{Op: procOpDestroy, InstanceID: instanceID}, 2*time.Minute)
	w.mu.Lock()
	if w.active > 0 {
		w.active--
	}
	if resp != nil && resp.Active >= 0 {
		// prefer worker-reported active if present
		w.active = resp.Active
	}
	w.mu.Unlock()
	return resp, err
}

func (w *processWorker) markDraining() {
	w.mu.Lock()
	w.draining = true
	w.mu.Unlock()
}

func (w *processWorker) isDraining() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.draining
}

func (w *processWorker) activeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.active
}

func (w *processWorker) kill() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead {
		return
	}
	w.dead = true
	if w.stdin != nil {
		_ = writeProcessMsg(w.stdin, processMsg{Op: procOpExit})
		_ = w.stdin.Close()
		w.stdin = nil
	}
	if w.cmd != nil && w.cmd.Process != nil {
		done := make(chan struct{})
		go func() {
			_ = w.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = w.cmd.Process.Kill()
			<-done
		}
	}
	if w.shm != nil {
		_ = w.shm.Close()
		w.shm = nil
	}
}

func processWorkerCmd() (*exec.Cmd, error) {
	if bin := strings.TrimSpace(os.Getenv(envProcessWorkerBin)); bin != "" {
		return exec.Command(bin), nil
	}
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "wasmer-process-worker")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return exec.Command(candidate), nil
		}
	}
	// cwd relative (tests)
	if st, err := os.Stat("./wasmer-process-worker"); err == nil && !st.IsDir() {
		return exec.Command("./wasmer-process-worker"), nil
	}
	return nil, fmt.Errorf("set %s to wasmer-process-worker binary", envProcessWorkerBin)
}

func spawnProcessWorker(artifactPath string) (*processWorker, error) {
	cmd, err := processWorkerCmd()
	if err != nil {
		return nil, err
	}
	shm, err := createProcessShm(defaultShmCapacity)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(os.Environ(),
		envProcessWorker+"=1",
		envProcessArtifact+"="+artifactPath,
		envProcessIsolation+"=0", // prevent nested isolation
		envProcessShm+"="+shm.Path(),
	)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = shm.Close()
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = shm.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = shm.Close()
		return nil, err
	}
	w := &processWorker{
		id:     uuid.GetUUID(),
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
		shm:    shm,
	}
	ready, err := readProcessMsg(w.stdout, 2*time.Minute)
	if err != nil {
		w.kill()
		return nil, fmt.Errorf("wait ready: %w", err)
	}
	if !ready.OK || ready.Op != procOpReady {
		w.kill()
		errMsg := ready.Error
		if errMsg == "" {
			errMsg = "worker ready not ok"
		}
		return nil, errors.New(errMsg)
	}
	return w, nil
}

// ensureAcceptingWorker returns a non-draining live worker for this contract pool.
func (p *vmPool) ensureAcceptingWorker() (*processWorker, error) {
	p.workerMu.Lock()
	defer p.workerMu.Unlock()
	if p.worker != nil && p.worker.alive() && !p.worker.isDraining() {
		return p.worker, nil
	}
	if p.artifactPath == "" {
		return nil, errors.New("process isolation: missing artifactPath")
	}
	w, err := spawnProcessWorker(p.artifactPath)
	if err != nil {
		return nil, err
	}
	// Old draining worker is retired by destroy path when active hits 0.
	p.worker = w
	if p.log != nil {
		p.log.Infof("process worker spawned pid=%d artifact=%s", w.pid(), p.artifactPath)
	}
	return w, nil
}

func (p *vmPool) newProcessIsolatedInstance() (*wrappedInstance, error) {
	w, err := p.ensureAcceptingWorker()
	if err != nil {
		return nil, err
	}
	instID, resp, err := w.createInstance()
	if err != nil {
		return nil, fmt.Errorf("worker create: %w", err)
	}
	_ = resp
	inst := &wrappedInstance{
		id:              instID,
		processWorker:   w,
		processInstID:   instID,
		lastUseTime:     utils.CurrentTimeMillisSeconds(),
		createTime:      utils.CurrentTimeMillisSeconds(),
		errCount:        0,
	}
	if p.log != nil {
		p.log.Infof("process instance created id=%s worker_pid=%d active=%d",
			instID, w.pid(), w.activeCount())
	}
	return inst, nil
}

func (p *vmPool) closeProcessInstance(instance *wrappedInstance) {
	if instance == nil || instance.processWorker == nil {
		return
	}
	w := instance.processWorker
	instID := instance.processInstID
	instance.processWorker = nil
	instance.processInstID = ""

	resp, err := w.destroyInstance(instID)
	if err != nil {
		if p.log != nil {
			p.log.Errorf("worker destroy id=%s: %v", instID, err)
		}
		// Best-effort: if IPC failed, kill worker to reclaim.
		w.kill()
		p.workerMu.Lock()
		if p.worker == w {
			p.worker = nil
		}
		p.workerMu.Unlock()
		return
	}

	limit := p.rssLimitKB
	if limit <= 0 {
		limit = processRSSLimitKB()
	}
	if resp != nil && resp.VmRSSKB >= limit {
		w.markDraining()
		if p.log != nil {
			p.log.Infof("worker pid=%d VmRSS=%.2fMB >= limit=%.2fMB → draining (active=%d)",
				w.pid(), float64(resp.VmRSSKB)/1024, float64(limit)/1024, w.activeCount())
		}
	}

	if w.activeCount() == 0 && (w.isDraining() || !w.alive()) {
		w.kill()
		p.workerMu.Lock()
		if p.worker == w {
			p.worker = nil
		}
		p.workerMu.Unlock()
		if p.log != nil {
			p.log.Infof("worker pid=%d retired after drain", w.pid())
		}
	}
}

func (p *vmPool) shutdownProcessWorkers() {
	p.workerMu.Lock()
	w := p.worker
	p.worker = nil
	p.workerMu.Unlock()
	if w != nil {
		w.kill()
	}
	if p.artifactPath != "" {
		_ = os.Remove(p.artifactPath)
		_ = os.Remove(filepath.Dir(p.artifactPath))
		p.artifactPath = ""
	}
}
