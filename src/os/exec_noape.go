// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !ape

package os

// apeFile and apeShell are for GOOS=ape (exec_ape.go).

func apeFile() string { return "" }

func apeShell(name string, argv []string, attr *ProcAttr, err error) (string, []string) {
	return "", nil
}
