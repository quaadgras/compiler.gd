// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objabi

import (
	"internal/abi"
	"internal/buildcfg"
	"maps"
	"slices"
	"strings"
)

var funcIDs = map[string]abi.FuncID{
	"abort":        abi.FuncID_abort,
	"asmcgocall":   abi.FuncID_asmcgocall,
	"asyncPreempt": abi.FuncID_asyncPreempt,
	"cgocallback":  abi.FuncID_cgocallback,
	// fastcbentry (the fastcb direct C-ABI virtual-call entry) arranges its
	// goroutine-stack frame exactly like cgocallback — fake return PC on top
	// of a fixed-size frame matching the tracked pcsp delta — so it shares
	// cgocallback's funcID to get the same SPWrite exemption in the unwinder.
	"fastcbentry":        abi.FuncID_cgocallback,
	"corostart":          abi.FuncID_corostart,
	"debugCallV2":        abi.FuncID_debugCallV2,
	"gcBgMarkWorker":     abi.FuncID_gcBgMarkWorker,
	"rt0_go":             abi.FuncID_rt0_go,
	"goexit":             abi.FuncID_goexit,
	"gogo":               abi.FuncID_gogo,
	"gopanic":            abi.FuncID_gopanic,
	"handleAsyncEvent":   abi.FuncID_handleAsyncEvent,
	"main":               abi.FuncID_runtime_main,
	"mcall":              abi.FuncID_mcall,
	"morestack":          abi.FuncID_morestack,
	"mstart":             abi.FuncID_mstart,
	"panicwrap":          abi.FuncID_panicwrap,
	"runFinalizers":      abi.FuncID_runFinalizers,
	"runCleanups":        abi.FuncID_runCleanups,
	"sigpanic":           abi.FuncID_sigpanic,
	"systemstack_switch": abi.FuncID_systemstack_switch,
	"systemstack":        abi.FuncID_systemstack,

	// Don't show in call stack but otherwise not special.
	"deferreturn": abi.FuncIDWrapper,
}

// Get the function ID for the named function in the named file.
// The function should be package-qualified.
func GetFuncID(name string, isWrapper bool) abi.FuncID {
	if isWrapper {
		return abi.FuncIDWrapper
	}
	if strings.HasPrefix(name, "runtime.") {
		if id, ok := funcIDs[PortableUnprefixed(name[len("runtime."):])]; ok {
			return id
		}
	}
	return abi.FuncIDNormal
}

// PortableUnprefixed returns the name a GOOS=portable host copy's name
// (linux_sigpanic, Linux_Stat) was made from, or name.
func PortableUnprefixed(name string) string {
	for _, h := range allPortableHosts {
		for _, p := range [...]string{h + "_", strings.ToUpper(h[:1]) + h[1:] + "_"} {
			if rest, ok := strings.CutPrefix(name, p); ok && rest != "" {
				return rest
			}
		}
	}
	return name
}

// PortableDisplayName returns the symbol name of a GOOS=portable host copy's
// function as people know it: runtime.linux_sigpanic is
// runtime.sigpanic, syscall.(*Linux_Stat_t).f is syscall.(*Stat_t).f.
func PortableDisplayName(name string) string {
	start := strings.LastIndexByte(name, '/') + 1
	parts := strings.Split(name[start:], ".")
	changed := false
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		lead := ""
		for strings.HasPrefix(p, "(") || strings.HasPrefix(p, "*") {
			lead += p[:1]
			p = p[1:]
		}
		if u := PortableUnprefixed(p); u != p {
			parts[i] = lead + u
			changed = true
		}
	}
	if !changed {
		return name
	}
	return name[:start] + strings.Join(parts, ".")
}

// PortableHosts is the systems a GOOS=portable program can run on, by GOARCH
// (buildcfg.PortableAllHosts; buildcfg.PortableHosts is those of this build).
var PortableHosts = buildcfg.PortableAllHosts

// allPortableHosts is every architecture's PortableHosts.
var allPortableHosts = func() []string {
	var all []string
	for _, arch := range slices.Sorted(maps.Keys(PortableHosts)) {
		hosts := PortableHosts[arch]
		for _, h := range hosts {
			if !slices.Contains(all, h) {
				all = append(all, h)
			}
		}
	}
	return all
}()
