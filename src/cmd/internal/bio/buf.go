// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bio implements common I/O abstractions used within the Go toolchain.
package bio

import (
	"bufio"
	"io"
	"log"
	"os"
)

// Reader implements a seekable buffered io.Reader.
type Reader struct {
	f *os.File
	*bufio.Reader

	// mmaps tracks every read-only mmap'd block sliceOS handed out
	// for this Reader so the caller can transfer ownership to a
	// longer-lived sink (typically *Link.mmaps in cmd/link/host's
	// in-process linker, where the bin/go process outlives the
	// individual link). Without a sink, the mappings live until the
	// process exits — matches the historical "never unmapped"
	// contract that was safe under fork/exec but leaks GBs of VM
	// when the linker runs in-process across hundreds of links.
	mmaps     [][]byte
	mmapDrain func([][]byte) // see SetMmapSink
}

// Writer implements a seekable buffered io.Writer.
type Writer struct {
	f *os.File
	*bufio.Writer
}

// Create creates the file named name and returns a Writer
// for that file.
func Create(name string) (*Writer, error) {
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, Writer: bufio.NewWriter(f)}, nil
}

// Open returns a Reader for the file named name.
func Open(name string) (*Reader, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	return NewReader(f), nil
}

// NewReader returns a Reader from an open file.
func NewReader(f *os.File) *Reader {
	return &Reader{f: f, Reader: bufio.NewReader(f)}
}

func (r *Reader) MustSeek(offset int64, whence int) int64 {
	if whence == 1 {
		offset -= int64(r.Buffered())
	}
	off, err := r.f.Seek(offset, whence)
	if err != nil {
		log.Fatalf("seeking in output: %v", err)
	}
	r.Reset(r.f)
	return off
}

func (w *Writer) MustSeek(offset int64, whence int) int64 {
	if err := w.Flush(); err != nil {
		log.Fatalf("writing output: %v", err)
	}
	off, err := w.f.Seek(offset, whence)
	if err != nil {
		log.Fatalf("seeking in output: %v", err)
	}
	return off
}

func (r *Reader) Offset() int64 {
	off, err := r.f.Seek(0, 1)
	if err != nil {
		log.Fatalf("seeking in output [0, 1]: %v", err)
	}
	off -= int64(r.Buffered())
	return off
}

func (w *Writer) Offset() int64 {
	if err := w.Flush(); err != nil {
		log.Fatalf("writing output: %v", err)
	}
	off, err := w.f.Seek(0, 1)
	if err != nil {
		log.Fatalf("seeking in output [0, 1]: %v", err)
	}
	return off
}

func (r *Reader) Close() error {
	if r.mmapDrain != nil && len(r.mmaps) > 0 {
		r.mmapDrain(r.mmaps)
		r.mmaps = nil
	}
	return r.f.Close()
}

// SetMmapSink registers a callback Reader.Close will invoke with
// every mmap'd block this Reader has handed out. The callback is
// expected to retain the slices for the lifetime of any data they
// back, then call Munmap on each before exit.
//
// This is a fork addition: stock cmd's bio leaks every mmap until
// process teardown, which was free under fork/exec but accumulates
// GBs of VM in long-lived processes (cmd/link/host inside cmd/go).
func (r *Reader) SetMmapSink(drain func([][]byte)) {
	r.mmapDrain = drain
}

func (w *Writer) Close() error {
	err := w.Flush()
	err1 := w.f.Close()
	if err == nil {
		err = err1
	}
	return err
}

func (r *Reader) File() *os.File {
	return r.f
}

func (w *Writer) File() *os.File {
	return w.f
}

// Slice reads the next length bytes of r into a slice.
//
// This slice may be backed by mmap'ed memory; see SliceRO and
// SetMmapSink for the mapping lifecycle. The second result reports
// whether the backing memory is read-only.
func (r *Reader) Slice(length uint64) ([]byte, bool, error) {
	if length == 0 {
		return []byte{}, false, nil
	}

	data, ok := r.sliceOS(length)
	if ok {
		return data, true, nil
	}

	data = make([]byte, length)
	_, err := io.ReadFull(r, data)
	if err != nil {
		return nil, false, err
	}
	return data, false, nil
}

// SliceRO returns a slice containing the next length bytes of r
// backed by a read-only mmap'd data. If the mmap cannot be
// established (limit exceeded, region too small, etc) a nil slice
// will be returned.
//
// The mapping is recorded against the Reader and is unmapped only
// if the caller has registered a sink via SetMmapSink and later
// calls Munmap on the entries the sink received; otherwise the
// mapping survives until process exit (the historical contract).
func (r *Reader) SliceRO(length uint64) []byte {
	data, ok := r.sliceOS(length)
	if ok {
		return data
	}
	return nil
}
