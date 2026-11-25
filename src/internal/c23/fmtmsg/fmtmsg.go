// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fmtmsg is fmtmsg.h's function for cmd/cc.
package fmtmsg

import (
	c23 "internal/c23"
	"os"
	"strings"
)

const (
	MM_PRINT   = 0x100
	MM_CONSOLE = 0x200

	MM_NOTOK = -1
	MM_OK    = 0
	MM_NOMSG = 1
	MM_NOCON = 4
)

var severities = []string{"", "HALT", "ERROR", "WARNING", "INFO"}

// Fmtmsg is fmtmsg, as glibc's: "label: severity: text\nTO FIX: action
// tag", of the parts given (and that MSGVERB selects, for standard
// error), to standard error (MM_PRINT) and /dev/console (MM_CONSOLE).
func Fmtmsg(classification int64, label *c23.Void, severity int32, text, action, tag *c23.Void) int32 {
	str := func(p *c23.Void) (string, bool) {
		if c23.Addr(p) == 0 {
			return "", false
		}
		return c23.GoString(p), true
	}
	lbl, hasLabel := str(label)
	if hasLabel {
		a, b, ok := strings.Cut(lbl, ":")
		if !ok || len(a) > 10 || len(b) > 14 {
			return MM_NOTOK
		}
	}
	if severity < 0 || int(severity) >= len(severities) {
		return MM_NOTOK
	}
	txt, hasText := str(text)
	act, hasAction := str(action)
	tg, hasTag := str(tag)
	format := func(verb map[string]bool) string {
		on := func(k string, has bool) bool { return has && (verb == nil || verb[k]) }
		l, s, t, a, g := on("label", hasLabel), on("severity", severity != 0), on("text", hasText), on("action", hasAction), on("tag", hasTag)
		var b strings.Builder
		if l {
			b.WriteString(lbl)
			if s || t || a || g {
				b.WriteString(": ")
			}
		}
		if s {
			b.WriteString(severities[severity])
			if t || a || g {
				b.WriteString(": ")
			}
		}
		if t {
			b.WriteString(txt)
			if a || g {
				b.WriteString("\n")
			}
		}
		if a {
			b.WriteString("TO FIX: " + act)
			if g {
				b.WriteString("  ")
			}
		}
		if g {
			b.WriteString(tg)
		}
		b.WriteString("\n")
		return b.String()
	}
	// MSGVERB selects the parts written to standard error; all of them if
	// it names one that is not.
	var verb map[string]bool
	if v := os.Getenv("MSGVERB"); v != "" {
		verb = map[string]bool{}
		for k := range strings.SplitSeq(v, ":") {
			switch k {
			case "label", "severity", "text", "action", "tag":
				verb[k] = true
			default:
				verb = nil
			}
			if verb == nil {
				break
			}
		}
	}
	r := int32(MM_OK)
	if classification&MM_PRINT != 0 {
		if !c23.FileOf(c23.Stdio(2)).Write([]byte(format(verb))) {
			r = MM_NOMSG
		}
	}
	if classification&MM_CONSOLE != 0 {
		f, err := os.OpenFile("/dev/console", os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString(format(nil))
			f.Close()
		}
		if err != nil {
			if r == MM_NOMSG {
				return MM_NOTOK
			}
			r = MM_NOCON
		}
	}
	return r
}
