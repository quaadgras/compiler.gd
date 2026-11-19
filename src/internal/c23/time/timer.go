// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	c23 "internal/c23"
	"os"
	"sync"
	"time"
)

// POSIX's timers (timer_create and its kin), clock_nanosleep,
// clock_settime and clock_getcpuclockid.
//
// A timer is a time.Timer, on Go's monotonic clock whatever its clock
// (a CPU-time clock's time is the time since its process or thread
// started, internal/c23's ClockNow): an absolute time on
// CLOCK_REALTIME is converted when the timer is set, so a change of the
// system's time does not move it. At each expiry it sends its signal to
// the process (SIGEV_SIGNAL, with si_code SI_TIMER and si_value the
// sigevent's), calls its function on a new thread (SIGEV_THREAD), or
// does nothing (SIGEV_NONE). Signals are not queued: an expiry while the
// timer's signal is still pending is an overrun.

// sigev_notify's values, and timer_settime's flag.
const (
	SIGEV_SIGNAL  = 0
	SIGEV_NONE    = 1
	SIGEV_THREAD  = 2
	TIMER_ABSTIME = 1

	SI_TIMER = -2
	SIGALRM  = 14

	delaytimerMax = 1<<31 - 1 // DELAYTIMER_MAX
)

type timer struct {
	id     uintptr
	clock  int32
	notify int32
	signo  int32
	value  c23.Ptr // sigev_value, as its pointer member
	fn     c23.Ptr // sigev_notify_function

	mu       sync.Mutex
	t        *time.Timer
	at       time.Time // when it expires next, if armed
	interval time.Duration
	seq      int // which arming the timer's function is of

	// The overruns of its last signal, while it is pending, and of the
	// one before, which was delivered (or of the last, delivered).
	overrun, delivered int64
}

var (
	timersMu sync.Mutex
	timers   = map[uintptr]*timer{}
	timerN   uintptr
)

func timerOf(id c23.Ptr) *timer {
	timersMu.Lock()
	defer timersMu.Unlock()
	return timers[c23.Addr(id)]
}

// The offsets in struct sigevent {union sigval sigev_value; int
// sigev_signo; int sigev_notify; void (*sigev_notify_function)(union
// sigval); void *sigev_notify_attributes; ...}.
var (
	sevSignoOff  = int(c23.WordSize)
	sevNotifyOff = int(c23.WordSize) + 4
	sevFnOff     = int(c23.WordSize) + 8
)

// TimerCreate is timer_create: a timer on CLOCK_REALTIME,
// CLOCK_MONOTONIC, CLOCK_BOOTTIME or a CPU-time clock, which notifies as
// evp says (if evp is null, SIGALRM with the timer's id as si_value).
// The timer's id, stored at timerid, is a timer_t that is not a pointer
// to anything.
func TimerCreate(clock int32, evp, timerid c23.Ptr) int32 {
	if _, ok := c23.ClockOrigin(clock); !ok {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	tm := &timer{clock: clock, notify: SIGEV_SIGNAL, signo: SIGALRM}
	if c23.Addr(evp) != 0 {
		tm.value = c23.LoadPtr(evp)
		tm.signo = int32(c23.Load32(c23.Add(evp, sevSignoOff)))
		tm.notify = int32(c23.Load32(c23.Add(evp, sevNotifyOff)))
		switch tm.notify {
		case SIGEV_SIGNAL:
			if tm.signo <= 0 || tm.signo >= c23.Nsig {
				c23.SetErrno(c23.EINVAL)
				return -1
			}
		case SIGEV_THREAD:
			tm.fn = c23.LoadPtr(c23.Add(evp, sevFnOff))
			c23.FuncOf[func(c23.Ptr)](tm.fn)
		case SIGEV_NONE:
		default:
			c23.SetErrno(c23.EINVAL)
			return -1
		}
	}
	timersMu.Lock()
	timerN++
	tm.id = timerN
	timers[tm.id] = tm
	timersMu.Unlock()
	if c23.Addr(evp) == 0 {
		tm.value = c23.FromInt(tm.id)
	}
	c23.StorePtr(timerid, c23.FromInt(tm.id))
	return 0
}

// TimerDelete is timer_delete: the timer is disarmed, and its id no
// longer one.
func TimerDelete(id c23.Ptr) int32 {
	timersMu.Lock()
	tm := timers[c23.Addr(id)]
	delete(timers, c23.Addr(id))
	timersMu.Unlock()
	if tm == nil {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	tm.mu.Lock()
	tm.disarm()
	tm.mu.Unlock()
	return 0
}

// disarm stops tm; tm.mu is held.
func (tm *timer) disarm() {
	if tm.t != nil {
		tm.t.Stop()
		tm.t = nil
	}
	tm.seq++
	tm.at, tm.interval = time.Time{}, 0
}

// left is the time until tm expires, 0 if it is disarmed; tm.mu is held.
func (tm *timer) left() time.Duration {
	if tm.at.IsZero() {
		return 0
	}
	return max(time.Until(tm.at), 1) // armed: not zero
}

// storeItimerspec stores interval and value as struct itimerspec
// {struct timespec it_interval, it_value;} at p.
func storeItimerspec(p c23.Ptr, interval, value time.Duration) {
	c23.StoreTimespec(p, interval)
	c23.StoreTimespec(c23.Add(p, 16), value)
}

// TimerSettime is timer_settime: it arms the timer to expire at value's
// it_value (a time on its clock if flags has TIMER_ABSTIME, at once if
// that has passed; a time from now if not), then every it_interval if
// that is not zero; a zero it_value disarms it. What was left of the
// previous arming is stored at ovalue (if not null).
func TimerSettime(id c23.Ptr, flags int32, value, ovalue c23.Ptr) int32 {
	tm := timerOf(id)
	if tm == nil {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	interval, ok1 := c23.LoadTimespec(value)
	v, ok2 := c23.LoadTimespec(c23.Add(value, 16))
	if !ok1 || !ok2 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	var at time.Time
	if v != 0 {
		if flags&TIMER_ABSTIME != 0 {
			now, _ := c23.ClockNow(tm.clock)
			at = time.Now().Add(v - now)
		} else {
			at = time.Now().Add(v)
		}
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if c23.Addr(ovalue) != 0 {
		storeItimerspec(ovalue, tm.interval, tm.left())
	}
	tm.disarm()
	if !at.IsZero() {
		tm.at, tm.interval = at, interval
		tm.arm()
	}
	return 0
}

// TimerGettime is timer_gettime: the time until the timer expires (0 if
// it is disarmed) and its interval.
func TimerGettime(id, value c23.Ptr) int32 {
	tm := timerOf(id)
	if tm == nil {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	storeItimerspec(value, tm.interval, tm.left())
	return 0
}

// TimerGetoverrun is timer_getoverrun: how many times the timer expired
// while its last signal was pending, or late (an expiry the timer's
// goroutine ran after the next was due counts the ones it missed).
func TimerGetoverrun(id c23.Ptr) int32 {
	tm := timerOf(id)
	if tm == nil {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	n := tm.overrun
	if tm.notify == SIGEV_SIGNAL && tm.pending() {
		n = tm.delivered
	}
	return int32(min(n, delaytimerMax))
}

// pending reports whether tm's signal is pending.
func (tm *timer) pending() bool {
	return c23.SigPendingFrom(tm.signo, func(i c23.SigInfo) bool { return i.Code == SI_TIMER && i.Timer == tm.id })
}

// arm starts tm's time.Timer for its next expiry; tm.mu is held.
func (tm *timer) arm() {
	seq := tm.seq
	tm.t = time.AfterFunc(time.Until(tm.at), func() { tm.expire(seq) })
}

// expire is tm's expiry of arming seq.
func (tm *timer) expire(seq int) {
	tm.mu.Lock()
	if tm.seq != seq {
		tm.mu.Unlock()
		return
	}
	missed := int64(0)
	if tm.interval > 0 {
		// The next expiry, skipping those already past, which are
		// overruns.
		now := time.Now()
		tm.at = tm.at.Add(tm.interval)
		if tm.at.Before(now) {
			k := now.Sub(tm.at)/tm.interval + 1
			tm.at = tm.at.Add(k * tm.interval)
			missed = int64(k)
		}
		tm.arm()
	} else {
		tm.at, tm.t = time.Time{}, nil
	}
	switch tm.notify {
	case SIGEV_SIGNAL:
		info := c23.SigInfo{Code: SI_TIMER, Pid: int32(os.Getpid()), Uid: int32(os.Getuid()), Value: tm.value, Timer: tm.id}
		if tm.pending() {
			tm.overrun += 1 + missed
			tm.mu.Unlock()
			return
		}
		tm.delivered, tm.overrun = tm.overrun, missed
		tm.mu.Unlock()
		c23.SigSendProcess(tm.signo, info, nil)
	case SIGEV_THREAD:
		tm.overrun = missed
		tm.mu.Unlock()
		f := c23.FuncOf[func(c23.Ptr)](tm.fn)
		v := c23.Calloc(1, uint64(c23.WordSize)) // union sigval
		c23.StorePtr(v, tm.value)
		c23.SpawnThread(c23.New[uint64](), true, func(*c23.Thread) { f(v) })
	default:
		tm.overrun = missed
		tm.mu.Unlock()
	}
}

// ClockNanosleep is clock_nanosleep, a cancellation point: it sleeps for
// the time at rqtp, or until it if flags has TIMER_ABSTIME, on clock id
// (CLOCK_REALTIME, CLOCK_MONOTONIC, CLOCK_BOOTTIME or the process's
// CPU-time clock; a thread's is EINVAL, as on Linux). It is not
// interrupted by signals, so it does not store what is left at rmtp.
// The result is an error number.
func ClockNanosleep(id, flags int32, rqtp, rmtp c23.Ptr) int32 {
	if id == CLOCK_THREAD_CPUTIME_ID || id < 0 && id&7 != 2 {
		return c23.EINVAL
	}
	if _, ok := c23.ClockOrigin(id); !ok {
		return c23.EINVAL
	}
	d, ok := c23.LoadTimespec(rqtp)
	if !ok {
		return c23.EINVAL
	}
	if flags&TIMER_ABSTIME != 0 {
		now, _ := c23.ClockNow(id)
		d -= now
	}
	c23.CancelSleep(d)
	return 0
}

// ClockSettime is clock_settime: the program may not set the system's
// clock (EPERM); the other clocks cannot be set (EINVAL).
func ClockSettime(id int32, ts c23.Ptr) int32 {
	if id == CLOCK_REALTIME {
		if _, ok := c23.LoadTimespec(ts); !ok {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		c23.SetErrno(c23.EPERM)
		return -1
	}
	c23.SetErrno(c23.EINVAL)
	return -1
}

// ClockGetcpuclockid is clock_getcpuclockid: the CPU-time clock of the
// program's process (pid 0 or its own), which counts the time since it
// started. Another process's is EPERM: the program cannot know its time.
// The result is an error number.
func ClockGetcpuclockid(pid int32, clock c23.Ptr) int32 {
	if pid != 0 && pid != int32(os.Getpid()) {
		return c23.EPERM
	}
	c23.Store32(clock, uint32(c23.ProcessCPUClock(pid)))
	return 0
}
