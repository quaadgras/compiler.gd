// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package signal

import (
	"internal/c23"
	"strconv"
	"strings"
)

// POSIX 2024's sig2str and str2sig: signals' names without "SIG", by
// Linux's numbers, and RTMIN+n, RTMAX-n for the real-time signals.

var sigNames = [...]string{
	1: "HUP", 2: "INT", 3: "QUIT", 4: "ILL", 5: "TRAP", 6: "ABRT", 7: "BUS",
	8: "FPE", 9: "KILL", 10: "USR1", 11: "SEGV", 12: "USR2", 13: "PIPE",
	14: "ALRM", 15: "TERM", 16: "STKFLT", 17: "CHLD", 18: "CONT", 19: "STOP",
	20: "TSTP", 21: "TTIN", 22: "TTOU", 23: "URG", 24: "XCPU", 25: "XFSZ",
	26: "VTALRM", 27: "PROF", 28: "WINCH", 29: "IO", 30: "PWR", 31: "SYS",
}

const (
	sigRTMIN = 34
	sigRTMAX = 64
)

// Sig2str is sig2str: str has SIG2STR_MAX (32) bytes.
func Sig2str(sig int32, str *c23.Void) int32 {
	var s string
	switch {
	case sig > 0 && int(sig) < len(sigNames):
		s = sigNames[sig]
	case sig == sigRTMIN:
		s = "RTMIN"
	case sig == sigRTMAX:
		s = "RTMAX"
	case sig > sigRTMIN && sig <= (sigRTMIN+sigRTMAX)/2:
		s = "RTMIN+" + strconv.Itoa(int(sig-sigRTMIN))
	case sig > sigRTMIN && sig < sigRTMAX:
		s = "RTMAX-" + strconv.Itoa(int(sigRTMAX-sig))
	default:
		return -1
	}
	copy(c23.Writable(str, uintptr(len(s)+1)), s+"\x00")
	return 0
}

// Str2sig is str2sig: a name without "SIG", or a signal's number.
func Str2sig(str, pnum *c23.Void) int32 {
	s := c23.GoString(str)
	sig := int32(-1)
	for i, n := range sigNames {
		if n != "" && n == s {
			sig = int32(i)
		}
	}
	rt := func(prefix string, base, sign int32) {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			if rest == "" {
				sig = base
			} else if rest[0] == '+' && sign > 0 || rest[0] == '-' && sign < 0 {
				if n, err := strconv.Atoi(rest[1:]); err == nil && rest[1:] != "" && rest[1] != '+' && rest[1] != '-' {
					if v := base + sign*int32(n); v >= sigRTMIN && v <= sigRTMAX {
						sig = v
					}
				}
			}
		}
	}
	if sig < 0 {
		rt("RTMIN", sigRTMIN, 1)
	}
	if sig < 0 {
		rt("RTMAX", sigRTMAX, -1)
	}
	if sig < 0 && s != "" && s[0] >= '0' && s[0] <= '9' {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= sigRTMAX {
			sig = int32(n)
		}
	}
	if sig < 0 {
		return -1
	}
	c23.Store32(pnum, uint32(sig))
	return 0
}
