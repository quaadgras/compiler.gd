// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buildcfg

import (
	"fmt"
	"strings"
)

// A program for several systems at once is built by naming them:
//
//	GOOS=linux,darwin GOARCH=amd64,arm64 go build
//
// writes one file that runs on each named system on each named
// architecture; every pair of them has to be one there is a host of
// (PortableAllHosts). Inside the toolchain a list of systems is
// GOOS=portable, the port whose programs find the system they run on when
// they start, and a list of architectures is its first, the one a tool
// works for (the go command runs itself for each other one): GOOS and
// GOARCH above are that, and the lists stay what the environment says,
// for PortableHosts and PortableArches. GOOS=portable is not a system to
// ask for.

// PortableAllHosts lists, by GOARCH, the systems a GOOS=portable program can run
// on, in the order of the runtime's numbers for them (portablehost). This
// list also appears in internal/syslist.
var PortableAllHosts = map[string][]string{
	"amd64": {"linux", "freebsd", "windows", "darwin"},
	"arm64": {"linux", "darwin"},
}

// PortableHosts returns the systems the GOOS=portable program being built
// runs on on goarch: those of PortableAllHosts that the list in $GOOS
// names (all of them, if it is no list).
func PortableHosts(goarch string) []string {
	return PortableHostsOf(goarch, PortableList())
}

// PortableList returns the list in $GOOS, as it is, or "" if $GOOS is no
// list.
func PortableList() string {
	if goos := getenv("GOOS"); strings.Contains(goos, ",") {
		return goos
	}
	return ""
}

// PortableListed returns the systems of the list in $GOOS, in its order.
func PortableListed() []string {
	return splitList(PortableList())
}

// PortableArches returns the architectures of the list in $GOARCH, in
// its order, or nil if $GOARCH is no list.
func PortableArches() []string {
	if goarch := getenv("GOARCH"); strings.Contains(goarch, ",") {
		return splitList(goarch)
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// PortableHostsOf is PortableHosts for only as the list in $GOOS.
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

// portableTarget returns what the toolchain's GOOS or GOARCH (key) is if
// $GOOS or $GOARCH is a list: "portable", and the first architecture.
// If the lists are no program's, it sets PortableError and returns what
// the tools know, so that they get to say so.
func portableTarget(key string) (string, bool) {
	goos, goarch := getenv("GOOS"), getenv("GOARCH")
	if !strings.Contains(goos, ",") && !strings.Contains(goarch, ",") {
		if goos == "portable" {
			PortableError = fmt.Errorf("GOOS=portable: name the systems the program is for, as in GOOS=linux,windows")
			return map[string]string{"GOOS": defaultGOOS, "GOARCH": defaultGOARCH}[key], key == "GOOS"
		}
		return "", false
	}
	oses, arches := splitList(goos), splitList(goarch)
	if oses == nil {
		oses = []string{defaultGOOS}
	}
	if arches == nil {
		arches = []string{defaultGOARCH}
	}
	fail := func(format string, args ...any) (string, bool) {
		PortableError = fmt.Errorf("GOOS=%s GOARCH=%s: "+format, append([]any{goos, goarch}, args...)...)
		return map[string]string{"GOOS": defaultGOOS, "GOARCH": defaultGOARCH}[key], true
	}
	if len(oses) < 2 {
		return fail("several architectures of one system are not one file (yet)")
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
		return fail("no %s in a program for several systems (there are %s)", strings.Join(missing, ", "), strings.Join(have, ", "))
	}
	if key == "GOOS" {
		return "portable", true
	}
	return arches[0], true
}
