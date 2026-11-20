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
// (APEAllHosts). Inside the toolchain that is GOOS=ape, the port whose
// programs find the system they run on when they start, for the hosts
// $GOAPEHOSTS on the architectures $GOAPEARCHES: apeLists rewrites the
// environment so, when the package starts. GOOS=ape is not a system to
// ask for: only the toolchain says it, with $GOAPEHOSTS.

// APEAllHosts lists, by GOARCH, the systems a GOOS=ape program can run
// on, in the order of the runtime's numbers for them (apehost). This
// list also appears in internal/syslist.
var APEAllHosts = map[string][]string{
	"amd64": {"linux", "freebsd", "windows", "darwin"},
	"arm64": {"linux", "darwin"},
}

// APEHosts returns the systems the GOOS=ape program being built runs on
// on goarch: those of APEAllHosts that $GOAPEHOSTS (a comma-separated
// list) names, or all of them.
func APEHosts(goarch string) []string {
	return APEHostsOf(goarch, os.Getenv("GOAPEHOSTS"))
}

// APEHostsOf is APEHosts for only as $GOAPEHOSTS.
func APEHostsOf(goarch, only string) []string {
	all := APEAllHosts[goarch]
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

// APEHostIndex returns the runtime's number for host on goarch, less
// one: its index in APEAllHosts.
func APEHostIndex(goarch, host string) int {
	for i, h := range APEAllHosts[goarch] {
		if h == host {
			return i
		}
	}
	return -1
}

// APEError is what is wrong with lists in $GOOS and $GOARCH, if anything.
var APEError error

var apeListsOnce sync.Once

// apeLists makes $GOOS and $GOARCH that are lists GOOS=ape's.
func apeLists() {
	apeListsOnce.Do(func() {
		goos, goarch := os.Getenv("GOOS"), os.Getenv("GOARCH")
		if !strings.Contains(goos, ",") && !strings.Contains(goarch, ",") {
			if goos == "ape" && os.Getenv("GOAPEHOSTS") == "" {
				APEError = fmt.Errorf("GOOS=ape: name the systems the program is for, as in GOOS=linux,windows")
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
			APEError = fmt.Errorf("GOOS=%s GOARCH=%s: "+format, append([]any{goos, goarch}, args...)...)
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
				if len(APEHostsOf(a, o)) == 0 {
					missing = append(missing, o+"/"+a)
				}
			}
		}
		if missing != nil {
			var have []string
			for _, a := range []string{"amd64", "arm64"} {
				for _, o := range APEAllHosts[a] {
					have = append(have, o+"/"+a)
				}
			}
			fail("no %s in a program for several systems (there are %s)", strings.Join(missing, ", "), strings.Join(have, ", "))
			return
		}
		list := strings.Join(oses, ",")
		os.Setenv("GOOS", "ape")
		os.Setenv("GOARCH", arches[0])
		os.Setenv("GOAPEHOSTS", list)
		os.Setenv("GOAPEARCHES", strings.Join(arches, ","))
	})
}
