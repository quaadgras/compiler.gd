// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buildcfg

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// A program for several systems at once is built by naming them:
//
//	GOOS=linux,darwin GOARCH=amd64,arm64 go build
//
// writes one file that runs on each named system on each named
// architecture; every pair of them has to be one there is a host of
// (PortableAllHosts). Inside the toolchain that is GOOS=portable, the port whose
// programs find the system they run on when they start, for the hosts
// $GOPORTABLEHOSTS on the architectures $GOPORTABLEARCHES: portableLists rewrites the
// environment so, when the package starts. GOOS=portable is not a system to
// ask for: only the toolchain says it, with $GOPORTABLEHOSTS.

// PortableAllHosts lists, by GOARCH, the systems a GOOS=portable program can run
// on, in the order of the runtime's numbers for them (portablehost). This
// list also appears in internal/syslist.
var PortableAllHosts = map[string][]string{
	"amd64": {"linux", "freebsd", "windows", "darwin"},
	"arm64": {"linux", "darwin"},
}

// PortableHosts returns the systems the GOOS=portable program being built runs on
// on goarch: those of PortableAllHosts that $GOPORTABLEHOSTS (a comma-separated
// list) names, or all of them.
func PortableHosts(goarch string) []string {
	return PortableHostsOf(goarch, os.Getenv("GOPORTABLEHOSTS"))
}

// PortableHostsOf is PortableHosts for only as $GOPORTABLEHOSTS.
func PortableHostsOf(goarch, only string) []string {
	all := PortableAllHosts[goarch]
	if only == "" {
		return all
	}
	var hosts []string
	for _, h := range all {
		for _, o := range strings.Split(only, ",") {
			if strings.TrimSpace(o) == h {
				hosts = append(hosts, h)
				break
			}
		}
	}
	return hosts
}

// PortableHostIndex returns the runtime's number for host on goarch, less
// one: its index in PortableAllHosts.
func PortableHostIndex(goarch, host string) int {
	for i, h := range PortableAllHosts[goarch] {
		if h == host {
			return i
		}
	}
	return -1
}

// PortableError is what is wrong with lists in $GOOS and $GOARCH, if anything.
var PortableError error

var portableListsOnce sync.Once

// portableLists makes $GOOS and $GOARCH that are lists GOOS=portable's.
func portableLists() {
	portableListsOnce.Do(func() {
		goos, goarch := os.Getenv("GOOS"), os.Getenv("GOARCH")
		if !strings.Contains(goos, ",") && !strings.Contains(goarch, ",") {
			if goos == "portable" && os.Getenv("GOPORTABLEHOSTS") == "" {
				PortableError = fmt.Errorf("GOOS=portable: name the systems the program is for, as in GOOS=linux,windows")
			}
			return
		}
		split := func(s, def string) []string {
			var out []string
			for _, f := range strings.Split(s, ",") {
				if f = strings.TrimSpace(f); f != "" {
					out = append(out, f)
				}
			}
			if out == nil {
				out = []string{def}
			}
			return out
		}
		oses, arches := split(goos, defaultGOOS), split(goarch, defaultGOARCH)
		fail := func(format string, args ...any) {
			PortableError = fmt.Errorf("GOOS=%s GOARCH=%s: "+format, append([]any{goos, goarch}, args...)...)
			// (Something the tools know, so that they get to say so.)
			os.Setenv("GOOS", defaultGOOS)
			os.Setenv("GOARCH", defaultGOARCH)
		}
		if len(oses) < 2 {
			fail("several architectures of one system are not one file (yet)")
			return
		}
		// Every system on every architecture.
		var missing []string
		for _, a := range arches {
			for _, o := range oses {
				if len(PortableHostsOf(a, o)) == 0 {
					missing = append(missing, o+"/"+a)
				}
			}
		}
		if missing != nil {
			var have []string
			for _, a := range []string{"amd64", "arm64"} {
				for _, o := range PortableAllHosts[a] {
					have = append(have, o+"/"+a)
				}
			}
			fail("no %s in a program for several systems (there are %s)", strings.Join(missing, ", "), strings.Join(have, ", "))
			return
		}
		list := strings.Join(oses, ",")
		os.Setenv("GOOS", "portable")
		os.Setenv("GOARCH", arches[0])
		os.Setenv("GOPORTABLEHOSTS", list)
		os.Setenv("GOPORTABLEARCHES", strings.Join(arches, ","))
	})
}
