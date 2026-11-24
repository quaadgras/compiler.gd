// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package netif

import (
	"internal/c23"
	"net"
)

// IfNameindex is if_nameindex: the interfaces Go's net package finds, as
// an array of struct if_nameindex {unsigned int if_index; char *if_name;}
// that ends with one of index 0 and a null name; null with errno ENOBUFS
// if the system's cannot be listed.
func IfNameindex() Ptr {
	ifs, err := net.Interfaces()
	if err != nil {
		c23.SetErrno(c23.ENOBUFS)
		return Ptr(nil)
	}
	ws := int(c23.WordSize)
	a := c23.Malloc(uint64((len(ifs) + 1) * 2 * ws))
	for i, ifi := range ifs {
		e := c23.Add(a, 2*ws*i)
		c23.Store32(e, uint32(ifi.Index))
		c23.StorePtr(c23.Add(e, ws), c23.CString(ifi.Name))
	}
	e := c23.Add(a, 2*ws*len(ifs))
	c23.Store32(e, 0)
	c23.StorePtr(c23.Add(e, ws), Ptr(nil))
	return a
}

// IfFreenameindex is if_freenameindex: the collector frees the array.
func IfFreenameindex(p Ptr) {}
