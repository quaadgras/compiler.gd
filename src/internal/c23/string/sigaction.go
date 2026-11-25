// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package string

import (
	c23 "internal/c23"
	"sync"
)

// Strsignal is strsignal: glibc's descriptions.
func Strsignal(sig int32) *c23.Void {
	strsignalMu.Lock()
	defer strsignalMu.Unlock()
	p, ok := strsignalMsg[sig]
	if !ok {
		p = c23.CString(c23.Strsignal(sig))
		strsignalMsg[sig] = p
	}
	return p
}

var strsignalMu sync.Mutex

var strsignalMsg = map[int32]*c23.Void{}
