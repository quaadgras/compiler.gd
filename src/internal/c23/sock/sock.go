// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sock is C's sockets for cmd/cc (sys/socket.h, netinet/in.h,
// arpa/inet.h, netdb.h): descriptors of internal/c23's table whose
// objects are net's listeners and connections. It is a package of its
// own so that only programs that use sockets link net.
//
// A stream socket's connection is read ahead by a goroutine
// (c23.ReadAhead), so that poll and select know when it is readable and
// O_NONBLOCK reads return EAGAIN; a listener accepts in a goroutine into
// a queue; a datagram socket receives into one. Writes block until Go
// has written (O_NONBLOCK does not make them return early).
package sock

import (
	"context"
	"errors"
	"internal/c23"
	"io"
	"math/bits"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type Ptr = c23.Ptr

// sys/socket.h's values: Linux's.
const (
	AF_UNSPEC = 0
	AF_UNIX   = 1
	AF_INET   = 2
	AF_INET6  = 10

	SOCK_STREAM   = 1
	SOCK_DGRAM    = 2
	SOCK_RAW      = 3
	SOCK_NONBLOCK = 0o4000
	SOCK_CLOEXEC  = 0o2000000

	SOL_SOCKET    = 1
	SO_REUSEADDR  = 2
	SO_TYPE       = 3
	SO_ERROR      = 4
	SO_BROADCAST  = 6
	SO_SNDBUF     = 7
	SO_RCVBUF     = 8
	SO_KEEPALIVE  = 9
	SO_LINGER     = 13
	SO_REUSEPORT  = 15
	SO_RCVTIMEO   = 20
	SO_SNDTIMEO   = 21
	SO_ACCEPTCONN = 30
	SO_DOMAIN     = 39
	SO_PROTOCOL   = 38

	IPPROTO_IP   = 0
	IPPROTO_TCP  = 6
	IPPROTO_UDP  = 17
	IPPROTO_IPV6 = 41

	TCP_NODELAY   = 1
	TCP_KEEPIDLE  = 4
	TCP_KEEPINTVL = 5
	TCP_KEEPCNT   = 6

	IPV6_V6ONLY = 26

	SHUT_RD   = 0
	SHUT_WR   = 1
	SHUT_RDWR = 2

	MSG_PEEK     = 0x2
	MSG_TRUNC    = 0x20
	MSG_DONTWAIT = 0x40
	MSG_WAITALL  = 0x100
	MSG_NOSIGNAL = 0x4000

	sIFSOCK = 0o140000
)

// A socket's states.
const (
	stNew = iota
	stBound
	stListening
	stConnecting
	stConnected
	stClosed
)

type dgram struct {
	b    []byte
	from net.Addr
}

// A socket is a descriptor's c23.Special.
type socket struct {
	family, typ, proto int32

	mu    sync.Mutex
	cond  sync.Cond
	state int

	laddr net.Addr // bound, not yet listening or connected
	ln    net.Listener
	queue []net.Conn // accepted, not yet taken by accept
	lnErr error

	conn   net.Conn
	ra     *c23.ReadAhead
	shutRd bool
	shutWr bool

	pc    net.PacketConn
	dq    []dgram
	peer  net.Addr // a datagram socket's, by connect
	dqErr error

	soError int32 // SO_ERROR, of a connect in progress

	// Options.
	reuseaddr, reuseport, keepalive, nodelay, broadcast int32
	v6only                                              int32
	rcvbuf, sndbuf                                      int32
	rcvtimeo, sndtimeo                                  time.Duration
	linger                                              [2]int32
	keepidle, keepintvl, keepcnt                        int32
}

func newSocket(family, typ, proto int32) *socket {
	s := &socket{family: family, typ: typ, proto: proto, rcvbuf: 131072, sndbuf: 16384}
	s.cond.L = &s.mu
	return s
}

func errno(e int32) int32 {
	c23.SetErrno(e)
	return -1
}

// sockOf returns fd's socket, or nil with errno EBADF or ENOTSOCK.
func sockOf(fd int32) (*socket, bool) {
	sp, nonblock := c23.FdSpecial(fd, c23.ENOTSOCK)
	if sp == nil {
		return nil, false
	}
	s, ok := sp.(*socket)
	if !ok {
		c23.SetErrno(c23.ENOTSOCK)
		return nil, false
	}
	return s, nonblock
}

// Socket is socket.
func Socket(family, typ, proto int32) int32 {
	flags := typ & (SOCK_NONBLOCK | SOCK_CLOEXEC)
	typ &^= flags
	switch family {
	case AF_INET, AF_INET6, AF_UNIX:
	default:
		return errno(c23.EAFNOSUPPORT)
	}
	switch {
	case typ == SOCK_STREAM && (proto == 0 || proto == IPPROTO_TCP && family != AF_UNIX):
	case typ == SOCK_DGRAM && family != AF_UNIX && (proto == 0 || proto == IPPROTO_UDP):
	case typ == SOCK_STREAM || typ == SOCK_DGRAM && family != AF_UNIX:
		return errno(c23.EPROTONOSUPPORT)
	default:
		return errno(c23.ESOCKTNOSUPPORT)
	}
	return install(newSocket(family, typ, proto), flags)
}

func install(s *socket, flags int32) int32 {
	var fl int32
	if flags&SOCK_NONBLOCK != 0 {
		fl = c23.O_NONBLOCK
	}
	return c23.FdNewSpecial(s, fl, flags&SOCK_CLOEXEC != 0)
}

// Socketpair is socketpair: AF_UNIX stream sockets, connected in memory.
func Socketpair(family, typ, proto int32, sv Ptr) int32 {
	flags := typ & (SOCK_NONBLOCK | SOCK_CLOEXEC)
	typ &^= flags
	if family != AF_UNIX {
		return errno(c23.EAFNOSUPPORT)
	}
	if typ != SOCK_STREAM {
		return errno(c23.EOPNOTSUPP)
	}
	a, b := newPair()
	sa, sb := newSocket(family, typ, proto), newSocket(family, typ, proto)
	sa.connected(a)
	sb.connected(b)
	c23.Store32(sv, uint32(install(sa, flags)))
	c23.Store32(c23.Add(sv, 4), uint32(install(sb, flags)))
	return 0
}

// connected makes s a connected stream socket of c.
func (s *socket) connected(c net.Conn) {
	s.conn = c
	s.state = stConnected
	s.ra = c23.NewReadAhead(c)
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetNoDelay(s.nodelay != 0)
		if s.keepalive != 0 {
			tc.SetKeepAlive(true)
		}
	}
}

// network is net's name of the socket's kind.
func (s *socket) network() string {
	switch {
	case s.family == AF_UNIX:
		return "unix"
	case s.typ == SOCK_DGRAM && s.family == AF_INET:
		return "udp4"
	case s.typ == SOCK_DGRAM && s.v6only != 0:
		return "udp6"
	case s.typ == SOCK_DGRAM:
		return "udp"
	case s.family == AF_INET:
		return "tcp4"
	case s.v6only != 0:
		return "tcp6"
	}
	return "tcp"
}

// Bind is bind. A stream socket listens from here (so its port is taken
// and getsockname knows it), but accept takes connections only after
// listen.
func Bind(fd int32, addr Ptr, n uint32) int32 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	a, e := decode(s.family, s.typ, addr, n)
	if e != 0 {
		return errno(e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stNew {
		return errno(c23.EINVAL)
	}
	if s.typ == SOCK_DGRAM {
		pc, err := net.ListenPacket(s.network(), a.String())
		if err != nil {
			return fail(err)
		}
		s.packet(pc)
		return 0
	}
	ln, err := net.Listen(s.network(), addrString(a))
	if err != nil {
		return fail(err)
	}
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false) // as C, the file stays
	}
	s.ln = ln
	s.laddr = ln.Addr()
	s.state = stBound
	return 0
}

func addrString(a net.Addr) string {
	if ua, ok := a.(*net.UnixAddr); ok {
		return ua.Name
	}
	return a.String()
}

// packet makes s a datagram socket of pc, receiving into its queue.
func (s *socket) packet(pc net.PacketConn) {
	s.pc = pc
	s.state = stBound
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := pc.ReadFrom(buf)
			s.mu.Lock()
			if err != nil {
				s.dqErr = err
				s.cond.Broadcast()
				s.mu.Unlock()
				c23.PollNotify()
				return
			}
			if s.peer == nil || sameAddr(from, s.peer) {
				if len(s.dq) < 256 {
					s.dq = append(s.dq, dgram{append([]byte(nil), buf[:n]...), from})
				}
			}
			s.cond.Broadcast()
			s.mu.Unlock()
			c23.PollNotify()
		}
	}()
}

func sameAddr(a, b net.Addr) bool {
	ua, ok1 := a.(*net.UDPAddr)
	ub, ok2 := b.(*net.UDPAddr)
	if ok1 && ok2 {
		return ua.AddrPort() == ub.AddrPort() || ua.Port == ub.Port && ua.IP.Equal(ub.IP)
	}
	return a.String() == b.String()
}

// Listen is listen.
func Listen(fd int32, backlog int32) int32 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.typ != SOCK_STREAM {
		return errno(c23.EOPNOTSUPP)
	}
	switch s.state {
	case stListening:
		return 0
	case stNew: // an ephemeral port on the wildcard address
		addr := ":0"
		if s.family == AF_UNIX {
			return errno(c23.EINVAL)
		}
		ln, err := net.Listen(s.network(), addr)
		if err != nil {
			return fail(err)
		}
		s.ln = ln
	case stBound:
	default:
		return errno(c23.EINVAL)
	}
	s.state = stListening
	ln := s.ln
	go func() {
		for {
			c, err := ln.Accept()
			s.mu.Lock()
			if err != nil {
				s.lnErr = err
				s.cond.Broadcast()
				s.mu.Unlock()
				c23.PollNotify()
				return
			}
			s.queue = append(s.queue, c)
			s.cond.Broadcast()
			s.mu.Unlock()
			c23.PollNotify()
		}
	}()
	return 0
}

// Accept is accept.
func Accept(fd int32, addr, n Ptr) int32 { return Accept4(fd, addr, n, 0) }

// Accept4 is accept4: flags are SOCK_NONBLOCK and SOCK_CLOEXEC.
func Accept4(fd int32, addr, n Ptr, flags int32) int32 {
	s, nonblock := sockOf(fd)
	if s == nil {
		return -1
	}
	if flags&^(SOCK_NONBLOCK|SOCK_CLOEXEC) != 0 {
		return errno(c23.EINVAL)
	}
	s.mu.Lock()
	if s.state != stListening {
		s.mu.Unlock()
		return errno(c23.EINVAL)
	}
	for len(s.queue) == 0 && s.lnErr == nil {
		if nonblock {
			s.mu.Unlock()
			return errno(c23.EAGAIN)
		}
		s.cond.Wait()
	}
	if len(s.queue) == 0 {
		s.mu.Unlock()
		return errno(c23.EINVAL)
	}
	c := s.queue[0]
	s.queue = s.queue[1:]
	ns := newSocket(s.family, s.typ, s.proto)
	ns.nodelay, ns.keepalive = s.nodelay, s.keepalive
	s.mu.Unlock()
	ns.connected(c)
	if Addr(addr) != 0 {
		encodeTo(c.RemoteAddr(), s.family, addr, n)
	}
	return install(ns, flags)
}

// Addr is c23.Addr.
func Addr(p Ptr) uintptr { return c23.Addr(p) }

// Connect is connect. In non-blocking mode a stream socket connects in
// the background: EINPROGRESS, then poll's POLLOUT and SO_ERROR.
func Connect(fd int32, addr Ptr, n uint32) int32 {
	s, nonblock := sockOf(fd)
	if s == nil {
		return -1
	}
	a, e := decode(s.family, s.typ, addr, n)
	if e != 0 {
		return errno(e)
	}
	s.mu.Lock()
	if s.typ == SOCK_DGRAM {
		defer s.mu.Unlock()
		if s.pc == nil {
			pc, err := net.ListenPacket(s.network(), wildcard(s.family))
			if err != nil {
				return fail(err)
			}
			s.packet(pc)
		}
		s.peer = a
		return 0
	}
	switch s.state {
	case stConnected:
		s.mu.Unlock()
		return errno(c23.EISCONN)
	case stConnecting:
		s.mu.Unlock()
		return errno(c23.EALREADY)
	case stListening:
		s.mu.Unlock()
		return errno(c23.EISCONN)
	}
	d := net.Dialer{Timeout: s.sndtimeo}
	if s.state == stBound { // bound to connect from that address
		d.LocalAddr = s.laddr
		s.ln.Close()
		s.ln = nil
	}
	s.state = stConnecting
	network := s.network()
	s.mu.Unlock()

	dial := func() int32 {
		c, err := d.DialContext(context.Background(), network, addrString(a))
		s.mu.Lock()
		defer s.mu.Unlock()
		defer c23.PollNotify()
		defer s.cond.Broadcast()
		if s.state == stClosed {
			if c != nil {
				c.Close()
			}
			return c23.EBADF
		}
		if err != nil {
			s.state = stNew
			return errnoOf(err)
		}
		s.connected(c)
		return 0
	}
	if nonblock {
		go func() {
			if e := dial(); e != 0 {
				s.mu.Lock()
				s.soError = e
				s.mu.Unlock()
			}
		}()
		return errno(c23.EINPROGRESS)
	}
	if e := dial(); e != 0 {
		return errno(e)
	}
	return 0
}

func wildcard(family int32) string {
	if family == AF_INET6 {
		return "[::]:0"
	}
	return "0.0.0.0:0"
}

// Shutdown is shutdown.
func Shutdown(fd, how int32) int32 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	if how < SHUT_RD || how > SHUT_RDWR {
		return errno(c23.EINVAL)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return errno(c23.ENOTCONN)
	}
	if how != SHUT_WR {
		s.shutRd = true
	}
	if how != SHUT_RD {
		s.shutWr = true
		if cw, ok := s.conn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}
	c23.PollNotify()
	return 0
}

// The c23.Special methods.

func (s *socket) Read(b []byte, nonblock bool) (int, error) {
	n, _, err := s.recv(b, nonblock, false, false)
	return n, err
}

func (s *socket) Write(b []byte, nonblock bool) (int, error) {
	return s.send(b, nil)
}

func (s *socket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = stClosed
	var err error
	if s.conn != nil {
		err = s.conn.Close()
	}
	if s.ln != nil {
		s.ln.Close()
	}
	if s.pc != nil {
		s.pc.Close()
	}
	for _, c := range s.queue {
		c.Close()
	}
	s.queue = nil
	s.cond.Broadcast()
	return err
}

func (s *socket) Mode() uint32 { return sIFSOCK | 0o777 }

func (s *socket) Ready() int16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.typ == SOCK_DGRAM:
		r := int16(c23.POLLOUT)
		if len(s.dq) > 0 {
			r |= c23.POLLIN
		}
		return r
	case s.state == stListening:
		if len(s.queue) > 0 || s.lnErr != nil {
			return c23.POLLIN
		}
		return 0
	case s.state == stConnecting:
		return 0
	case s.state == stConnected:
		r := int16(c23.POLLOUT)
		if s.shutRd {
			return r | c23.POLLIN
		}
		if ok, end := s.ra.Readable(); ok {
			r |= c23.POLLIN
			if end && s.shutWr {
				r |= c23.POLLHUP
			}
		}
		return r
	case s.soError != 0:
		return c23.POLLOUT | c23.POLLERR | c23.POLLHUP
	}
	return c23.POLLOUT | c23.POLLHUP // not connected
}

// recv receives into b: from the connection, or a datagram (from is its
// sender).
func (s *socket) recv(b []byte, nonblock, peek, waitall bool) (int, net.Addr, error) {
	s.mu.Lock()
	if s.typ == SOCK_DGRAM {
		defer s.mu.Unlock()
		if s.pc == nil {
			if nonblock {
				return 0, nil, c23.ErrnoError(c23.EAGAIN)
			}
			for s.pc == nil && s.state != stClosed {
				s.cond.Wait()
			}
		}
		var expired bool
		if s.rcvtimeo > 0 && len(s.dq) == 0 && !nonblock {
			t := time.AfterFunc(s.rcvtimeo, func() {
				s.mu.Lock()
				expired = true
				s.cond.Broadcast()
				s.mu.Unlock()
			})
			defer t.Stop()
		}
		for len(s.dq) == 0 && s.dqErr == nil {
			if nonblock || expired {
				return 0, nil, c23.ErrnoError(c23.EAGAIN)
			}
			s.cond.Wait()
		}
		if len(s.dq) == 0 {
			return 0, nil, s.dqErr
		}
		d := s.dq[0]
		if !peek {
			s.dq = s.dq[1:]
		}
		return copy(b, d.b), d.from, nil
	}
	if s.conn == nil {
		s.mu.Unlock()
		return 0, nil, c23.ErrnoError(c23.ENOTCONN)
	}
	if s.shutRd {
		s.mu.Unlock()
		return 0, nil, io.EOF
	}
	ra, peer := s.ra, s.conn.RemoteAddr()
	var deadline time.Time
	if s.rcvtimeo > 0 {
		deadline = time.Now().Add(s.rcvtimeo)
	}
	s.mu.Unlock()
	n, err := ra.Recv(b, nonblock, peek, deadline)
	for waitall && !peek && err == nil && n < len(b) {
		var k int
		k, err = ra.Recv(b[n:], nonblock, false, deadline)
		n += k
	}
	if n > 0 {
		err = nil
	}
	return n, peer, err
}

// send sends b: on the connection, or as a datagram to to (or the
// connected peer).
func (s *socket) send(b []byte, to net.Addr) (int, error) {
	s.mu.Lock()
	if s.typ == SOCK_DGRAM {
		if to == nil {
			to = s.peer
		}
		if to == nil {
			s.mu.Unlock()
			return 0, c23.ErrnoError(c23.EDESTADDRREQ)
		}
		if s.pc == nil {
			pc, err := net.ListenPacket(s.network(), wildcard(s.family))
			if err != nil {
				s.mu.Unlock()
				return 0, err
			}
			s.packet(pc)
		}
		pc := s.pc
		s.mu.Unlock()
		if s.sndtimeo > 0 {
			pc.SetWriteDeadline(time.Now().Add(s.sndtimeo))
		}
		return pc.WriteTo(b, to)
	}
	c, shut := s.conn, s.shutWr
	timeo := s.sndtimeo
	s.mu.Unlock()
	if c == nil {
		return 0, c23.ErrnoError(c23.ENOTCONN)
	}
	if shut {
		return 0, c23.ErrnoError(c23.EPIPE)
	}
	if timeo > 0 {
		c.SetWriteDeadline(time.Now().Add(timeo))
	}
	n, err := c.Write(b)
	if err != nil && n == 0 && errors.Is(err, os.ErrDeadlineExceeded) {
		err = c23.ErrnoError(c23.EAGAIN)
	}
	return n, err
}

// Send is send.
func Send(fd int32, buf Ptr, n uint64, flags int32) int64 {
	return Sendto(fd, buf, n, flags, Ptr{}, 0)
}

// Sendto is sendto: a stream socket ignores the address.
func Sendto(fd int32, buf Ptr, n uint64, flags int32, addr Ptr, alen uint32) int64 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	var to net.Addr
	if Addr(addr) != 0 && s.typ == SOCK_DGRAM {
		a, e := decode(s.family, s.typ, addr, alen)
		if e != 0 {
			return int64(errno(e))
		}
		to = a
	}
	k, err := s.send(c23.BytesAt(buf, n), to)
	if err != nil && k == 0 {
		return int64(fail(err))
	}
	return int64(k)
}

// Recv is recv.
func Recv(fd int32, buf Ptr, n uint64, flags int32) int64 {
	return Recvfrom(fd, buf, n, flags, Ptr{}, Ptr{})
}

// Recvfrom is recvfrom.
func Recvfrom(fd int32, buf Ptr, n uint64, flags int32, addr, alen Ptr) int64 {
	s, nonblock := sockOf(fd)
	if s == nil {
		return -1
	}
	k, from, err := s.recv(c23.BytesAt(buf, n), nonblock || flags&MSG_DONTWAIT != 0, flags&MSG_PEEK != 0, flags&MSG_WAITALL != 0)
	if err != nil && err != io.EOF {
		return int64(fail(err))
	}
	if Addr(addr) != 0 && from != nil {
		encodeTo(from, s.family, addr, alen)
	}
	return int64(k)
}

// struct msghdr (glibc's layout): void *msg_name; socklen_t msg_namelen;
// struct iovec *msg_iov; size_t msg_iovlen; void *msg_control; size_t
// msg_controllen; int msg_flags. No ancillary data is sent or received.
var (
	ws        = int(c23.WordSize)
	mNameLen  = ws
	mIov      = 2 * ws
	mIovLen   = 3 * ws
	mCtl      = 4 * ws
	mCtlLen   = 5 * ws
	mFlags    = 6 * ws
	iovecSize = 2 * ws
)

func iovecs(msg Ptr) [][]byte {
	iov := c23.LoadPtr(c23.Add(msg, mIov))
	n := int(c23.LoadLong(c23.Add(msg, mIovLen)))
	var bs [][]byte
	for i := range n {
		e := c23.Add(iov, i*iovecSize)
		base := c23.LoadPtr(e)
		l := uint64(c23.LoadLong(c23.Add(e, ws)))
		if l > 0 {
			bs = append(bs, c23.BytesAt(base, l))
		}
	}
	return bs
}

// Sendmsg is sendmsg.
func Sendmsg(fd int32, msg Ptr, flags int32) int64 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	var b []byte
	for _, v := range iovecs(msg) {
		b = append(b, v...)
	}
	var to net.Addr
	if name := c23.LoadPtr(msg); Addr(name) != 0 && s.typ == SOCK_DGRAM {
		a, e := decode(s.family, s.typ, name, c23.Load32(c23.Add(msg, mNameLen)))
		if e != 0 {
			return int64(errno(e))
		}
		to = a
	}
	k, err := s.send(b, to)
	if err != nil && k == 0 {
		return int64(fail(err))
	}
	return int64(k)
}

// Recvmsg is recvmsg.
func Recvmsg(fd int32, msg Ptr, flags int32) int64 {
	s, nonblock := sockOf(fd)
	if s == nil {
		return -1
	}
	vs := iovecs(msg)
	total := 0
	for _, v := range vs {
		total += len(v)
	}
	b := make([]byte, total)
	k, from, err := s.recv(b, nonblock || flags&MSG_DONTWAIT != 0, flags&MSG_PEEK != 0, flags&MSG_WAITALL != 0)
	if err != nil && err != io.EOF {
		return int64(fail(err))
	}
	rest := b[:k]
	for _, v := range vs {
		rest = rest[copy(v, rest):]
	}
	if name := c23.LoadPtr(msg); Addr(name) != 0 {
		if from != nil {
			l := encode(from, s.family)
			cap := int(c23.Load32(c23.Add(msg, mNameLen)))
			copy(c23.BytesAt(name, uint64(min(cap, len(l)))), l)
			c23.Store32(c23.Add(msg, mNameLen), uint32(len(l)))
		} else {
			c23.Store32(c23.Add(msg, mNameLen), 0)
		}
	}
	c23.StoreLong(c23.Add(msg, mCtlLen), 0)
	c23.Store32(c23.Add(msg, mFlags), 0)
	return int64(k)
}

// Getsockname is getsockname.
func Getsockname(fd int32, addr, alen Ptr) int32 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	s.mu.Lock()
	var a net.Addr
	switch {
	case s.conn != nil:
		a = s.conn.LocalAddr()
	case s.ln != nil:
		a = s.ln.Addr()
	case s.pc != nil:
		a = s.pc.LocalAddr()
	}
	s.mu.Unlock()
	if a == nil { // unbound: the wildcard address, port 0
		a = unbound(s.family)
	}
	encodeTo(a, s.family, addr, alen)
	return 0
}

func unbound(family int32) net.Addr {
	switch family {
	case AF_INET:
		return &net.TCPAddr{IP: net.IPv4zero}
	case AF_INET6:
		return &net.TCPAddr{IP: net.IPv6zero}
	}
	return &net.UnixAddr{Net: "unix"}
}

// Getpeername is getpeername.
func Getpeername(fd int32, addr, alen Ptr) int32 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	s.mu.Lock()
	var a net.Addr
	if s.conn != nil {
		a = s.conn.RemoteAddr()
	} else if s.peer != nil {
		a = s.peer
	}
	s.mu.Unlock()
	if a == nil {
		return errno(c23.ENOTCONN)
	}
	encodeTo(a, s.family, addr, alen)
	return 0
}

// Getsockopt is getsockopt, of the options setsockopt knows, SO_TYPE,
// SO_ERROR (which it clears), SO_ACCEPTCONN, SO_DOMAIN and SO_PROTOCOL.
func Getsockopt(fd, level, name int32, val, vlen Ptr) int32 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var v int32
	switch {
	case level == SOL_SOCKET && name == SO_TYPE:
		v = s.typ
	case level == SOL_SOCKET && name == SO_DOMAIN:
		v = s.family
	case level == SOL_SOCKET && name == SO_PROTOCOL:
		v = s.proto
		if v == 0 && s.family != AF_UNIX {
			v = If(s.typ == SOCK_STREAM, int32(IPPROTO_TCP), IPPROTO_UDP)
		}
	case level == SOL_SOCKET && name == SO_ERROR:
		v, s.soError = s.soError, 0
	case level == SOL_SOCKET && name == SO_ACCEPTCONN:
		v = If[int32](s.state == stListening, 1, 0)
	case level == SOL_SOCKET && (name == SO_RCVTIMEO || name == SO_SNDTIMEO):
		d := If(name == SO_RCVTIMEO, s.rcvtimeo, s.sndtimeo)
		if c23.Load32(vlen) < 16 {
			return errno(c23.EINVAL)
		}
		c23.Store64(val, uint64(d/time.Second))
		c23.Store64(c23.Add(val, 8), uint64(d%time.Second/time.Microsecond))
		c23.Store32(vlen, 16)
		return 0
	case level == SOL_SOCKET && name == SO_LINGER:
		if c23.Load32(vlen) < 8 {
			return errno(c23.EINVAL)
		}
		c23.Store32(val, uint32(s.linger[0]))
		c23.Store32(c23.Add(val, 4), uint32(s.linger[1]))
		c23.Store32(vlen, 8)
		return 0
	default:
		p := s.opt(level, name)
		if p == nil {
			return errno(c23.ENOPROTOOPT)
		}
		v = *p
	}
	if c23.Load32(vlen) < 4 {
		return errno(c23.EINVAL)
	}
	c23.Store32(val, uint32(v))
	c23.Store32(vlen, 4)
	return 0
}

// opt returns where an int option is kept, or nil.
func (s *socket) opt(level, name int32) *int32 {
	switch level {
	case SOL_SOCKET:
		switch name {
		case SO_REUSEADDR:
			return &s.reuseaddr
		case SO_REUSEPORT:
			return &s.reuseport
		case SO_KEEPALIVE:
			return &s.keepalive
		case SO_BROADCAST:
			return &s.broadcast
		case SO_RCVBUF:
			return &s.rcvbuf
		case SO_SNDBUF:
			return &s.sndbuf
		}
	case IPPROTO_TCP:
		if s.typ != SOCK_STREAM || s.family == AF_UNIX {
			return nil
		}
		switch name {
		case TCP_NODELAY:
			return &s.nodelay
		case TCP_KEEPIDLE:
			return &s.keepidle
		case TCP_KEEPINTVL:
			return &s.keepintvl
		case TCP_KEEPCNT:
			return &s.keepcnt
		}
	case IPPROTO_IPV6:
		if s.family == AF_INET6 && name == IPV6_V6ONLY {
			return &s.v6only
		}
	}
	return nil
}

// Setsockopt is setsockopt. Options Go can apply to its connections it
// applies; others are kept for getsockopt; unknown ones are ignored.
func Setsockopt(fd, level, name int32, val Ptr, vlen uint32) int32 {
	s, _ := sockOf(fd)
	if s == nil {
		return -1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case level == SOL_SOCKET && (name == SO_RCVTIMEO || name == SO_SNDTIMEO):
		if vlen < 16 {
			return errno(c23.EINVAL)
		}
		sec, usec := int64(c23.Load64(val)), int64(c23.Load64(c23.Add(val, 8)))
		if sec < 0 || usec < 0 || usec >= 1e6 {
			return errno(c23.EDOM)
		}
		d := time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond
		if name == SO_RCVTIMEO {
			s.rcvtimeo = d
		} else {
			s.sndtimeo = d
		}
		return 0
	case level == SOL_SOCKET && name == SO_LINGER:
		if vlen < 8 {
			return errno(c23.EINVAL)
		}
		s.linger = [2]int32{int32(c23.Load32(val)), int32(c23.Load32(c23.Add(val, 4)))}
		if tc, ok := s.conn.(*net.TCPConn); ok {
			tc.SetLinger(If(s.linger[0] != 0, int(s.linger[1]), -1))
		}
		return 0
	}
	p := s.opt(level, name)
	if p == nil {
		if level == SOL_SOCKET || level == IPPROTO_TCP || level == IPPROTO_IP || level == IPPROTO_IPV6 {
			return 0
		}
		return errno(c23.ENOPROTOOPT)
	}
	if vlen < 4 {
		return errno(c23.EINVAL)
	}
	v := int32(c23.Load32(val))
	if name == SO_RCVBUF || name == SO_SNDBUF {
		v *= 2 // as Linux reports it
	} else if v != 0 && level != IPPROTO_TCP {
		v = 1
	}
	*p = v
	if tc, ok := s.conn.(*net.TCPConn); ok {
		switch {
		case level == IPPROTO_TCP && name == TCP_NODELAY:
			tc.SetNoDelay(v != 0)
		case level == SOL_SOCKET && name == SO_KEEPALIVE:
			tc.SetKeepAlive(v != 0)
		case level == SOL_SOCKET && name == SO_RCVBUF:
			tc.SetReadBuffer(int(v / 2))
		case level == SOL_SOCKET && name == SO_SNDBUF:
			tc.SetWriteBuffer(int(v / 2))
		}
	}
	return 0
}

func If[T any](c bool, a, b T) T {
	if c {
		return a
	}
	return b
}

// fail sets errno to err's and returns -1.
func fail(err error) int32 {
	c23.SetErrno(errnoOf(err))
	return -1
}

// errnoOf is err's errno: a system error's, the same on every system (as
// Linux numbers them), or one for what net reports without one.
func errnoOf(err error) int32 {
	var en syscall.Errno
	if errors.As(err, &en) {
		if e, ok := sysErrno(en); ok {
			return e
		}
	}
	var ce interface{ Errno() int32 }
	if errors.As(err, &ce) {
		return ce.Errno()
	}
	var dns *net.DNSError
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		return c23.EAGAIN
	case errors.Is(err, net.ErrClosed):
		return c23.EBADF
	case errors.As(err, &dns):
		return c23.EHOSTUNREACH
	case errors.Is(err, io.EOF):
		return c23.EPIPE
	}
	// Leave it to internal/c23, which knows its own and fs's errors.
	c23.SetErrnoOf(err)
	return c23.Errno()
}

// sysErrno maps a system's error number to Linux's.
func sysErrno(en syscall.Errno) (int32, bool) {
	for _, m := range errnoMap {
		if en == m.sys {
			return m.c, true
		}
	}
	return 0, false
}

var errnoMap = []struct {
	sys syscall.Errno
	c   int32
}{
	{syscall.ECONNREFUSED, c23.ECONNREFUSED},
	{syscall.ECONNRESET, c23.ECONNRESET},
	{syscall.ECONNABORTED, c23.ECONNABORTED},
	{syscall.EADDRINUSE, c23.EADDRINUSE},
	{syscall.EADDRNOTAVAIL, c23.EADDRNOTAVAIL},
	{syscall.ENETUNREACH, c23.ENETUNREACH},
	{syscall.EHOSTUNREACH, c23.EHOSTUNREACH},
	{syscall.ETIMEDOUT, c23.ETIMEDOUT},
	{syscall.EPIPE, c23.EPIPE},
	{syscall.EACCES, c23.EACCES},
	{syscall.EPERM, c23.EPERM},
	{syscall.ENOENT, c23.ENOENT},
	{syscall.EINVAL, c23.EINVAL},
	{syscall.EAFNOSUPPORT, c23.EAFNOSUPPORT},
	{syscall.EMSGSIZE, c23.EMSGSIZE},
	{syscall.ENOTCONN, c23.ENOTCONN},
	{syscall.EAGAIN, c23.EAGAIN},
}

// Sockaddrs.

// decode reads the sockaddr of n bytes at p, of family (a datagram
// socket's if typ is SOCK_DGRAM), as net's address.
func decode(family, typ int32, p Ptr, n uint32) (net.Addr, int32) {
	if n < 2 {
		return nil, c23.EINVAL
	}
	fam := int32(c23.Load16(p))
	if fam != family && !(family == AF_INET6 && fam == AF_INET) {
		return nil, c23.EAFNOSUPPORT
	}
	switch fam {
	case AF_INET:
		if n < 16 {
			return nil, c23.EINVAL
		}
		b := c23.BytesAt(p, 8)
		ap := netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[4:8])), uint16(b[2])<<8|uint16(b[3]))
		if typ == SOCK_DGRAM {
			return net.UDPAddrFromAddrPort(ap), 0
		}
		return net.TCPAddrFromAddrPort(ap), 0
	case AF_INET6:
		if n < 28 {
			return nil, c23.EINVAL
		}
		b := c23.BytesAt(p, 28)
		ip := netip.AddrFrom16([16]byte(b[8:24]))
		if scope := c23.Load32(c23.Add(p, 24)); scope != 0 {
			ip = ip.WithZone(strconv.Itoa(int(scope)))
		}
		ap := netip.AddrPortFrom(ip, uint16(b[2])<<8|uint16(b[3]))
		if typ == SOCK_DGRAM {
			return net.UDPAddrFromAddrPort(ap), 0
		}
		return net.TCPAddrFromAddrPort(ap), 0
	case AF_UNIX:
		b := c23.BytesAt(p, uint64(min(n, 110)))[2:]
		for i, c := range b {
			if c == 0 {
				b = b[:i]
				break
			}
		}
		if len(b) == 0 {
			return nil, c23.EINVAL
		}
		return &net.UnixAddr{Name: string(b), Net: "unix"}, 0
	}
	return nil, c23.EAFNOSUPPORT
}

// encode returns a's sockaddr, of family (AF_INET6 shows an IPv4
// address as IPv4-mapped).
func encode(a net.Addr, family int32) []byte {
	var ap netip.AddrPort
	switch a := a.(type) {
	case *net.TCPAddr:
		ap = a.AddrPort()
	case *net.UDPAddr:
		ap = a.AddrPort()
	case *net.UnixAddr:
		b := make([]byte, 2, 110)
		b[0] = AF_UNIX
		if a.Name != "" && a.Name != "@" {
			b = append(append(b, a.Name...), 0)
		}
		return b
	default:
		return nil
	}
	ip, port := ap.Addr(), ap.Port()
	if family == AF_INET6 || ip.Is6() && !ip.Is4In6() {
		b := make([]byte, 28)
		b[0] = AF_INET6
		b[2], b[3] = byte(port>>8), byte(port)
		a16 := ip.As16()
		copy(b[8:24], a16[:])
		if z := ip.Zone(); z != "" {
			if n, err := strconv.Atoi(z); err == nil {
				b[24], b[25], b[26], b[27] = byte(n), byte(n>>8), byte(n>>16), byte(n>>24)
			} else if ifi, err := net.InterfaceByName(z); err == nil {
				n := ifi.Index
				b[24], b[25], b[26], b[27] = byte(n), byte(n>>8), byte(n>>16), byte(n>>24)
			}
		}
		return b
	}
	b := make([]byte, 16)
	b[0] = AF_INET
	b[2], b[3] = byte(port>>8), byte(port)
	a4 := ip.Unmap().As4()
	copy(b[4:8], a4[:])
	return b
}

// encodeTo stores a's sockaddr at addr, at most *alen bytes of it, and
// its length at alen.
func encodeTo(a net.Addr, family int32, addr, alen Ptr) {
	b := encode(a, family)
	if Addr(alen) == 0 {
		return
	}
	n := int(c23.Load32(alen))
	if Addr(addr) != 0 && n > 0 {
		copy(c23.BytesAt(addr, uint64(min(n, len(b)))), b)
	}
	c23.Store32(alen, uint32(len(b)))
}

// arpa/inet.h and netinet/in.h's byte order: the target is little-endian.

func Htons(v uint16) uint16 { return bits.ReverseBytes16(v) }
func Ntohs(v uint16) uint16 { return bits.ReverseBytes16(v) }
func Htonl(v uint32) uint32 { return bits.ReverseBytes32(v) }
func Ntohl(v uint32) uint32 { return bits.ReverseBytes32(v) }

// A pair is one end of socketpair's connection: two pipes, either way.
type pair struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func newPair() (net.Conn, net.Conn) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	return &pair{r1, w2}, &pair{r2, w1}
}

func (p *pair) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pair) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pair) CloseWrite() error           { return p.w.Close() }
func (p *pair) Close() error {
	p.w.Close()
	return p.r.Close()
}
func (p *pair) LocalAddr() net.Addr                { return &net.UnixAddr{Net: "unix"} }
func (p *pair) RemoteAddr() net.Addr               { return &net.UnixAddr{Net: "unix"} }
func (p *pair) SetDeadline(t time.Time) error      { return nil }
func (p *pair) SetReadDeadline(t time.Time) error  { return nil }
func (p *pair) SetWriteDeadline(t time.Time) error { return nil }
