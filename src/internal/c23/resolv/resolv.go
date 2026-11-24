// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package resolv is resolv.h's functions for cmd/cc. Go's resolver gives
// no DNS messages, so the queries fail with NO_RECOVERY in h_errno.
package resolv

import (
	c23 "internal/c23"
	"internal/c23/netdb"
)

// ResInit is res_init: there is no state to read.
func ResInit() int32 { return 0 }

func fail() int32 {
	netdb.SetHErrno(netdb.NO_RECOVERY)
	c23.SetErrno(c23.ENOSYS)
	return -1
}

// ResQuery is res_query: it fails, as no DNS message can be had.
func ResQuery(dname *c23.Void, class, typ int32, answer *c23.Void, anslen int32) int32 { return fail() }

func ResSearch(dname *c23.Void, class, typ int32, answer *c23.Void, anslen int32) int32 {
	return fail()
}

func ResQuerydomain(name, domain *c23.Void, class, typ int32, answer *c23.Void, anslen int32) int32 {
	return fail()
}
