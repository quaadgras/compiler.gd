// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/godebugs"
	"unsafe"
)

// Package runtime/metrics. The runtime here keeps few statistics: the
// metrics it has are the counters of internal/godebug and the few
// below; the others read as KindBad (unsupported), as metrics.Read
// documents for a metric the runtime does not know.

// metricSample and metricValue are runtime/metrics.Sample and Value.
type metricSample struct {
	name  string
	value metricValue
}

type metricValue struct {
	kind    metricKind
	scalar  uint64
	pointer unsafe.Pointer
}

type metricKind int

const (
	metricKindBad metricKind = iota
	metricKindUint64
	metricKindFloat64
	metricKindFloat64Histogram
)

// godebugMetrics are the counters internal/godebug registers, by name.
var godebugMetrics map[string]func() uint64

//go:linkname godebug_registerMetric internal/godebug.registerMetric
func godebug_registerMetric(name string, read func() uint64) {
	initGodebugMetrics()
	if _, ok := godebugMetrics[name]; !ok {
		throw("runtime: unexpected metric registration for " + name)
	}
	godebugMetrics[name] = read
}

// initGodebugMetrics makes the counter of each setting of GODEBUG
// that has one, which reads 0 until internal/godebug registers it.
func initGodebugMetrics() {
	if godebugMetrics != nil {
		return
	}
	godebugMetrics = make(map[string]func() uint64)
	for _, info := range godebugs.All {
		if !info.Opaque {
			godebugMetrics["/godebug/non-default-behavior/"+info.Name+":events"] = metricZero
		}
	}
}

func metricZero() uint64 { return 0 }

// runtimeMetrics are the metrics of the runtime itself that it has.
var runtimeMetrics = map[string]func() uint64{
	"/sched/goroutines:goroutines": func() uint64 { return uint64(ngoroutine) },
	"/gc/heap/allocs:objects":      func() uint64 { return memstatsMallocs },
	"/gc/cycles/total:gc-cycles":   func() uint64 { return uint64(linCollections) },
	"/sched/gomaxprocs:threads":    func() uint64 { return 1 },
}

//go:linkname readMetricNames runtime/metrics_test.runtime_readMetricNames
func readMetricNames() []string {
	initGodebugMetrics()
	var list []string
	for name := range runtimeMetrics {
		list = append(list, name)
	}
	for name := range godebugMetrics {
		list = append(list, name)
	}
	return list
}

//go:linkname readMetrics runtime/metrics.runtime_readMetrics
func readMetrics(samplesp unsafe.Pointer, len int, cap int) {
	initGodebugMetrics()
	samples := unsafe.Slice((*metricSample)(samplesp), len)
	for i := range samples {
		s := &samples[i]
		read := godebugMetrics[s.name]
		if read == nil {
			read = runtimeMetrics[s.name]
		}
		if read == nil {
			s.value.kind = metricKindBad
			continue
		}
		s.value.kind = metricKindUint64
		s.value.scalar = read()
	}
}
