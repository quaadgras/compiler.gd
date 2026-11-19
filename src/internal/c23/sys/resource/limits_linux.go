// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package resource

// sysResource is the system's resource of C's: Linux's are C's.
func sysResource(res int32) (int, bool) { return int(res), true }
