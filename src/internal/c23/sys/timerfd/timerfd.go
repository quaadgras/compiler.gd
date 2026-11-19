// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package timerfd is sys/timerfd.h: a timerfd is a c23.Special that
// counts the expirations of a time.Timer.
package timerfd

import (
	c23 "internal/c23"
	"sync"
	"time"
)

const (
	CLOCK_REALTIME  = 0
	CLOCK_MONOTONIC = 1
	CLOCK_BOOTTIME  = 7

	TFD_NONBLOCK = c23.O_NONBLOCK
	TFD_CLOEXEC  = c23.O_CLOEXEC

	TFD_TIMER_ABSTIME       = 1
	TFD_TIMER_CANCEL_ON_SET = 2
)

type timerfd struct {
	mu       sync.Mutex
	cond     sync.Cond
	clock    int32
	count    uint64    // the expirations not yet read
	at       time.Time // the next expiry, if armed
	interval time.Duration
	t        *time.Timer
	seq      int    // which arming the timer's function is of
	arrivals uint64 // the expirations, for epoll's edge trigger
}

// Read returns the count of expirations since the last read, waiting
// for one unless nonblock (EAGAIN).
func (tf *timerfd) Read(b []byte, nonblock bool) (int, error) {
	if len(b) < 8 {
		return 0, c23.ErrnoError(c23.EINVAL)
	}
	tf.mu.Lock()
	for tf.count == 0 {
		if nonblock {
			tf.mu.Unlock()
			return 0, c23.ErrnoError(c23.EAGAIN)
		}
		tf.cond.Wait()
	}
	v := tf.count
	tf.count = 0
	tf.mu.Unlock()
	for i := range 8 {
		b[i] = byte(v >> (8 * i))
	}
	return 8, nil
}

func (tf *timerfd) Write(b []byte, nonblock bool) (int, error) {
	return 0, c23.ErrnoError(c23.EINVAL)
}

func (tf *timerfd) Close() error {
	tf.mu.Lock()
	tf.disarm()
	tf.mu.Unlock()
	return nil
}

func (tf *timerfd) Ready() int16 {
	tf.mu.Lock()
	defer tf.mu.Unlock()
	if tf.count > 0 {
		return c23.POLLIN
	}
	return 0
}

func (tf *timerfd) Mode() uint32 { return 0o600 }

func (tf *timerfd) Arrivals() uint64 {
	tf.mu.Lock()
	defer tf.mu.Unlock()
	return tf.arrivals
}

// disarm stops the timer; tf.mu is held.
func (tf *timerfd) disarm() {
	if tf.t != nil {
		tf.t.Stop()
		tf.t = nil
	}
	tf.seq++
	tf.at, tf.interval = time.Time{}, 0
}

// arm starts the timer for tf.at; tf.mu is held.
func (tf *timerfd) arm() {
	seq := tf.seq
	tf.t = time.AfterFunc(time.Until(tf.at), func() {
		tf.mu.Lock()
		if tf.seq != seq {
			tf.mu.Unlock()
			return
		}
		n := uint64(1)
		if tf.interval > 0 {
			// The expirations past by now, and the next.
			now := time.Now()
			if late := now.Sub(tf.at); late >= tf.interval {
				n += uint64(late / tf.interval)
			}
			tf.at = tf.at.Add(time.Duration(n) * tf.interval)
			tf.arm()
		} else {
			tf.at, tf.t = time.Time{}, nil
		}
		tf.count += n
		tf.arrivals++
		tf.cond.Broadcast()
		tf.mu.Unlock()
		c23.PollNotify()
	})
}

// left is what is left until the next expiry; tf.mu is held.
func (tf *timerfd) left() time.Duration {
	if tf.at.IsZero() {
		return 0
	}
	return max(time.Until(tf.at), 1) // armed: not zero
}

// TimerfdCreate is timerfd_create. CLOCK_BOOTTIME is CLOCK_MONOTONIC.
// Timers count Go's monotonic time: one of CLOCK_REALTIME does not
// follow changes of the system's clock.
func TimerfdCreate(clock, flags int32) int32 {
	switch clock {
	case CLOCK_REALTIME, CLOCK_MONOTONIC, CLOCK_BOOTTIME:
	default:
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if flags&^(TFD_NONBLOCK|TFD_CLOEXEC) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	tf := &timerfd{clock: clock}
	tf.cond.L = &tf.mu
	return c23.FdNewSpecial(tf, flags&TFD_NONBLOCK, flags&TFD_CLOEXEC != 0)
}

func timerfdOf(fd int32) *timerfd {
	sp, _ := c23.FdSpecial(fd, c23.EINVAL)
	if sp == nil {
		return nil
	}
	tf, ok := sp.(*timerfd)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return nil
	}
	return tf
}

// loadTimespec reads a struct timespec {time_t tv_sec; long tv_nsec;}.
func loadTimespec(p c23.Ptr) (time.Duration, bool) {
	sec, nsec := int64(c23.Load64(p)), c23.LoadLong(c23.Add(p, 8))
	if sec < 0 || nsec < 0 || nsec >= 1e9 {
		return 0, false
	}
	if sec > (1<<63-1)/int64(time.Second)-1 {
		sec = (1<<63-1)/int64(time.Second) - 1
	}
	return time.Duration(sec)*time.Second + time.Duration(nsec), true
}

func storeTimespec(p c23.Ptr, d time.Duration) {
	c23.Store64(p, uint64(d/time.Second))
	c23.StoreLong(c23.Add(p, 8), int64(d%time.Second))
}

// storeItimerspec stores struct itimerspec {struct timespec it_interval,
// it_value;}.
func (tf *timerfd) storeItimerspec(p c23.Ptr) {
	storeTimespec(p, tf.interval)
	storeTimespec(c23.Add(p, 16), tf.left())
}

// TimerfdSettime is timerfd_settime. It resets the count of expirations.
// TFD_TIMER_CANCEL_ON_SET is accepted, and does nothing.
func TimerfdSettime(fd, flags int32, newValue, oldValue c23.Ptr) int32 {
	tf := timerfdOf(fd)
	if tf == nil {
		return -1
	}
	if flags&^(TFD_TIMER_ABSTIME|TFD_TIMER_CANCEL_ON_SET) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	interval, ok1 := loadTimespec(newValue)
	value, ok2 := loadTimespec(c23.Add(newValue, 16))
	if !ok1 || !ok2 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	tf.mu.Lock()
	defer tf.mu.Unlock()
	if c23.Addr(oldValue) != 0 {
		tf.storeItimerspec(oldValue)
	}
	tf.disarm()
	tf.count = 0
	if value == 0 {
		return 0
	}
	tf.interval = interval
	if flags&TFD_TIMER_ABSTIME != 0 {
		if tf.clock == CLOCK_REALTIME {
			tf.at = time.Now().Add(time.Until(time.Unix(0, 0).Add(value)))
		} else {
			tf.at = c23.MonoStart.Add(value)
		}
	} else {
		tf.at = time.Now().Add(value)
	}
	tf.arm()
	return 0
}

// TimerfdGettime is timerfd_gettime.
func TimerfdGettime(fd int32, currValue c23.Ptr) int32 {
	tf := timerfdOf(fd)
	if tf == nil {
		return -1
	}
	tf.mu.Lock()
	defer tf.mu.Unlock()
	tf.storeItimerspec(currValue)
	return 0
}
