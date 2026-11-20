// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !portable

package os

// portableFile and portableShell are for GOOS=portable (exec_portable.go).

func portableFile() string { return "" }

func portableShell(name string, argv []string, attr *ProcAttr, err error) (string, []string) {
	return "", nil
}
