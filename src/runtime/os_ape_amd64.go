// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ape

package runtime

// The hosts of a GOOS=ape program on amd64, in the order of
// internal/syslist.APEHosts["amd64"].
const (
	apeLinux = 1 + iota
	apeFreebsd
	apeWindows
)

// apeGOOSNames are the hosts' names, by apehost.
var apeGOOSNames = [...]string{apeLinux: "linux", apeFreebsd: "freebsd", apeWindows: "windows"}
