/*
Copyright (C) BABEC. All rights reserved.
Copyright (C) THL A29 Limited, a Tencent company. All rights reserved.

SPDX-License-Identifier: Apache-2.0

Shared-memory channel for process-isolation host_call:

  Parent creates /dev/shm file + MAP_SHARED; path passed via WASMER_PROCESS_SHM.
  Worker copies WASM linear memory into the region (memcpy, no base64/JSON).
  Control plane stays tiny newline-JSON (op / ret only).

Layout (little-endian):
  0:  u64 memLen
  8:  u32 headerOff
  12: u32 headerLen
  16: u32 bodyOff
  20: u32 bodyLen
  24: i32 ret
  28: u32 reserved
  32: u64 capacity (bytes after header)
  64: memory bytes...
*/

package wasmer

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	envProcessShm = "WASMER_PROCESS_SHM"

	shmHeaderSize      = 64
	defaultShmCapacity = 64 << 20 // 64 MiB linear memory capacity
)

type processShm struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	data     []byte
	capacity int // bytes available for memory (after header)
	owned    bool
}

func createProcessShm(capacity int) (*processShm, error) {
	if capacity <= 0 {
		capacity = defaultShmCapacity
	}
	dir := "/dev/shm"
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "wasmer-iso-*.shm")
	if err != nil {
		return nil, fmt.Errorf("create shm file: %w", err)
	}
	path := f.Name()
	total := shmHeaderSize + capacity
	if err := f.Truncate(int64(total)); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("truncate shm: %w", err)
	}
	data, err := unix.Mmap(int(f.Fd()), 0, total, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("mmap shm: %w", err)
	}
	s := &processShm{
		path:     path,
		file:     f,
		data:     data,
		capacity: capacity,
		owned:    true,
	}
	binary.LittleEndian.PutUint64(s.data[32:40], uint64(capacity))
	return s, nil
}

func openProcessShm(path string) (*processShm, error) {
	if path == "" {
		return nil, fmt.Errorf("empty shm path")
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open shm %s: %w", path, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	total := int(st.Size())
	if total < shmHeaderSize {
		_ = f.Close()
		return nil, fmt.Errorf("shm too small: %d", total)
	}
	data, err := unix.Mmap(int(f.Fd()), 0, total, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("mmap shm: %w", err)
	}
	capacity := total - shmHeaderSize
	if c := binary.LittleEndian.Uint64(data[32:40]); c > 0 && int(c) <= capacity {
		capacity = int(c)
	}
	return &processShm{
		path:     path,
		file:     f,
		data:     data,
		capacity: capacity,
		owned:    false,
	}, nil
}

func (s *processShm) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *processShm) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	if s.data != nil {
		if err := unix.Munmap(s.data); err != nil && first == nil {
			first = err
		}
		s.data = nil
	}
	if s.file != nil {
		if err := s.file.Close(); err != nil && first == nil {
			first = err
		}
		s.file = nil
	}
	if s.owned && s.path != "" {
		_ = os.Remove(s.path)
		s.path = ""
	}
	return first
}

func (s *processShm) Mem() []byte {
	if s == nil || s.data == nil {
		return nil
	}
	return s.data[shmHeaderSize : shmHeaderSize+s.capacity]
}

func (s *processShm) Ensure(memLen int) error {
	if s == nil {
		return fmt.Errorf("nil shm")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if memLen <= s.capacity {
		return nil
	}
	// Grow by doubling until enough (page-aligned to 1MiB).
	newCap := s.capacity
	if newCap <= 0 {
		newCap = defaultShmCapacity
	}
	for newCap < memLen {
		newCap *= 2
	}
	total := shmHeaderSize + newCap
	if err := unix.Munmap(s.data); err != nil {
		return err
	}
	s.data = nil
	if err := s.file.Truncate(int64(total)); err != nil {
		return fmt.Errorf("grow shm: %w", err)
	}
	data, err := unix.Mmap(int(s.file.Fd()), 0, total, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("remap shm: %w", err)
	}
	s.data = data
	s.capacity = newCap
	binary.LittleEndian.PutUint64(s.data[32:40], uint64(newCap))
	return nil
}

func (s *processShm) PutMemory(memory []byte, headerOff, headerLen, bodyOff, bodyLen int32) error {
	if s == nil {
		return fmt.Errorf("nil shm")
	}
	if err := s.Ensure(len(memory)); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := s.data[shmHeaderSize : shmHeaderSize+len(memory)]
	copy(mem, memory)
	binary.LittleEndian.PutUint64(s.data[0:8], uint64(len(memory)))
	binary.LittleEndian.PutUint32(s.data[8:12], uint32(headerOff))
	binary.LittleEndian.PutUint32(s.data[12:16], uint32(headerLen))
	binary.LittleEndian.PutUint32(s.data[16:20], uint32(bodyOff))
	binary.LittleEndian.PutUint32(s.data[20:24], uint32(bodyLen))
	binary.LittleEndian.PutUint32(s.data[24:28], 0) // ret cleared
	return nil
}

func (s *processShm) remmapFromFileLocked() error {
	st, err := s.file.Stat()
	if err != nil {
		return err
	}
	total := int(st.Size())
	if total < shmHeaderSize {
		return fmt.Errorf("shm too small: %d", total)
	}
	if s.data != nil && len(s.data) == total {
		capFromHdr := int(binary.LittleEndian.Uint64(s.data[32:40]))
		if capFromHdr > 0 && capFromHdr <= total-shmHeaderSize {
			s.capacity = capFromHdr
		} else {
			s.capacity = total - shmHeaderSize
		}
		return nil
	}
	if s.data != nil {
		if err := unix.Munmap(s.data); err != nil {
			return err
		}
		s.data = nil
	}
	data, err := unix.Mmap(int(s.file.Fd()), 0, total, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("remap shm: %w", err)
	}
	s.data = data
	capacity := total - shmHeaderSize
	if c := binary.LittleEndian.Uint64(data[32:40]); c > 0 && int(c) <= capacity {
		capacity = int(c)
	}
	s.capacity = capacity
	return nil
}

func (s *processShm) RequestView() (memory []byte, header, body []byte, err error) {
	if s == nil || s.file == nil {
		return nil, nil, nil, fmt.Errorf("nil shm")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.remmapFromFileLocked(); err != nil {
		return nil, nil, nil, err
	}
	memLen := int(binary.LittleEndian.Uint64(s.data[0:8]))
	if memLen < 0 || memLen > s.capacity {
		return nil, nil, nil, fmt.Errorf("bad memLen %d capacity %d", memLen, s.capacity)
	}
	hoff := int(binary.LittleEndian.Uint32(s.data[8:12]))
	hlen := int(binary.LittleEndian.Uint32(s.data[12:16]))
	boff := int(binary.LittleEndian.Uint32(s.data[16:20]))
	blen := int(binary.LittleEndian.Uint32(s.data[20:24]))
	memory = s.data[shmHeaderSize : shmHeaderSize+memLen]
	if hoff < 0 || hlen < 0 || hoff+hlen > memLen {
		return nil, nil, nil, fmt.Errorf("bad header range %d+%d mem=%d", hoff, hlen, memLen)
	}
	if boff < 0 || blen < 0 || boff+blen > memLen {
		return nil, nil, nil, fmt.Errorf("bad body range %d+%d mem=%d", boff, blen, memLen)
	}
	header = memory[hoff : hoff+hlen]
	body = memory[boff : boff+blen]
	return memory, header, body, nil
}

func (s *processShm) SetRet(ret int32) {
	if s == nil || s.data == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binary.LittleEndian.PutUint32(s.data[24:28], uint32(ret))
}

func (s *processShm) Ret() int32 {
	if s == nil || s.data == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return int32(binary.LittleEndian.Uint32(s.data[24:28]))
}

func (s *processShm) CopyMemoryOut(dst []byte) error {
	if s == nil || s.data == nil {
		return fmt.Errorf("nil shm")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	memLen := int(binary.LittleEndian.Uint64(s.data[0:8]))
	if memLen != len(dst) {
		return fmt.Errorf("host memory size mismatch: shm %d want %d", memLen, len(dst))
	}
	copy(dst, s.data[shmHeaderSize:shmHeaderSize+memLen])
	return nil
}
