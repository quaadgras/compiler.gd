// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sysinfo is sys/sysinfo.h's functions for cmd/cc: Linux's
// /proc and /sys, and what Go knows elsewhere.
package sysinfo

import (
	c23 "internal/c23"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// info is struct sysinfo, in bytes.
type info struct {
	uptime                       int64
	loads                        [3]uint64 // scaled by 1<<16
	totalram, freeram, sharedram uint64
	bufferram                    uint64
	totalswap, freeswap          uint64
	procs                        uint16
}

// read returns what /proc says (on Linux; zeros elsewhere, one process
// for this one).
func read() info {
	in := info{procs: 1}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				in.uptime = int64(v)
			}
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		for i := 0; i < 3 && i < len(f); i++ {
			if v, err := strconv.ParseFloat(f[i], 64); err == nil {
				in.loads[i] = uint64(v*65536 + 0.5)
			}
		}
		if len(f) > 3 {
			if _, total, ok := strings.Cut(f[3], "/"); ok {
				if n, err := strconv.Atoi(total); err == nil {
					in.procs = uint16(n)
				}
			}
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for line := range strings.SplitSeq(string(b), "\n") {
			name, rest, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			f := strings.Fields(rest)
			if len(f) == 0 {
				continue
			}
			kb, err := strconv.ParseUint(f[0], 10, 64)
			if err != nil {
				continue
			}
			v := kb * 1024
			switch name {
			case "MemTotal":
				in.totalram = v
			case "MemFree":
				in.freeram = v
			case "Shmem":
				in.sharedram = v
			case "Buffers":
				in.bufferram = v
			case "SwapTotal":
				in.totalswap = v
			case "SwapFree":
				in.freeswap = v
			}
		}
	}
	return in
}

// Sysinfo is sysinfo. The memory is in units of mem_unit bytes, 1 unless
// a size does not fit in an unsigned long.
func Sysinfo(buf *c23.Void) int32 {
	in := read()
	l := int(c23.LongSize)
	unit := uint64(1)
	if l == 4 {
		for max(in.totalram, in.totalswap)/unit > 0xffffffff {
			unit *= 2
		}
	}
	size := (13*l + 4 + 20 - 2*l - 4 + l - 1) &^ (l - 1)
	c23.Memset(buf, 0, uint64(size))
	c23.StoreLong(buf, in.uptime)
	for i, v := range in.loads {
		c23.StoreLong(c23.Add(buf, l*(1+i)), int64(v))
	}
	for i, v := range []uint64{in.totalram, in.freeram, in.sharedram, in.bufferram, in.totalswap, in.freeswap} {
		c23.StoreLong(c23.Add(buf, l*(4+i)), int64(v/unit))
	}
	c23.Store16(c23.Add(buf, 10*l), in.procs)
	c23.Store32(c23.Add(buf, 13*l), uint32(unit))
	return 0
}

// cpuList counts the CPUs of a list such as /sys's "0-3,5".
func cpuList(s string) int {
	n := 0
	for r := range strings.SplitSeq(strings.TrimSpace(s), ",") {
		lo, hi, ok := strings.Cut(r, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			return 0
		}
		b := a
		if ok {
			if b, err = strconv.Atoi(hi); err != nil {
				return 0
			}
		}
		n += b - a + 1
	}
	return n
}

// GetNprocs is get_nprocs: the CPUs online (Linux's
// /sys/devices/system/cpu/online, else those the program may use).
func GetNprocs() int32 {
	if b, err := os.ReadFile("/sys/devices/system/cpu/online"); err == nil {
		if n := cpuList(string(b)); n > 0 {
			return int32(n)
		}
	}
	return int32(runtime.NumCPU())
}

// GetNprocsConf is get_nprocs_conf: the CPUs the system has (Linux's
// cpuN directories in /sys/devices/system/cpu, as glibc counts them).
func GetNprocsConf() int32 {
	if ents, err := os.ReadDir("/sys/devices/system/cpu"); err == nil {
		n := 0
		for _, e := range ents {
			name := e.Name()
			if len(name) > 3 && name[:3] == "cpu" {
				if _, err := strconv.Atoi(name[3:]); err == nil {
					n++
				}
			}
		}
		if n > 0 {
			return int32(n)
		}
	}
	return GetNprocs()
}

// GetPhysPages is get_phys_pages: the memory's size in pages (0 where it
// is not known).
func GetPhysPages() int64 {
	return int64(read().totalram / uint64(os.Getpagesize()))
}

// GetAvphysPages is get_avphys_pages: the free memory in pages.
func GetAvphysPages() int64 {
	return int64(read().freeram / uint64(os.Getpagesize()))
}
