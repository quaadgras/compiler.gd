// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"cmd/cc/host"
	"fmt"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// The tests translate with the assertions that Go lays each record out as
// C does (cc -debug), which the Go compiler then checks.
func init() { os.Setenv("CCDEBUG", "layout") }

func translate(t *testing.T, in, out string, withMain bool) {
	t.Helper()
	src, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	gosrc, err := host.Translate(in, src, "main", withMain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, gosrc, 0o666); err != nil {
		t.Fatal(err)
	}
}

// module translates in into a temporary module, beside copies of the Go
// files goFiles, and returns the module's directory.
func module(t *testing.T, in string, withMain bool, goFiles ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module cctest\n\ngo 1.27\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	translate(t, in, filepath.Join(dir, "c.go"), withMain)
	for _, f := range goFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func goRun(t *testing.T, dir string) string {
	t.Helper()
	cmd := testenv.Command(t, testenv.GoToolPath(t), "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	return string(out)
}

// TestBasic compares the output of ordinary C with gcc's.
func TestBasic(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/basic.c", true)
	bin := filepath.Join(t.TempDir(), "basic")
	if out, err := exec.Command(gcc, "-w", "-o", bin, "testdata/basic.c").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	want, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestC23 compares the output of C23 that gcc also compiles with gcc's.
func TestC23(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/c23.c", true)
	bin := filepath.Join(t.TempDir(), "c23")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, "testdata/c23.c").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	want, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestIntcap compares C that converts pointers to intptr_t and uintptr_t
// and back, which keep the capability (internal/c23's intcap.go), with
// gcc's output.
func TestIntcap(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/intcap.c", true)
	bin := filepath.Join(t.TempDir(), "intcap")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, "testdata/intcap.c").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	want, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestThreads compares <threads.h> and the C23 library functions of
// testdata/threads.c with gcc's (and its C library's).
func TestThreads(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/threads.c", true)
	bin := filepath.Join(t.TempDir(), "threads")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, "testdata/threads.c", "-lm", "-latomic").CombinedOutput(); err != nil {
		t.Skipf("gcc: %v\n%s", err, out) // no C11 threads or libatomic
	}
	want, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestSock compares sockets, name resolution, poll and select
// (testdata/sock.c) with gcc's.
func TestSock(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/sock.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	bin := filepath.Join(t.TempDir(), "sock")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir() // it makes a socket file
	want, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, want)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRegex compares regex.h's matching (and termios.h's, without a
// terminal) of testdata/regex.c with gcc's C library's.
func TestRegex(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/regex.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	want := hostWant(t, gcc, "regex", func() string {
		bin := filepath.Join(t.TempDir(), "regex")
		if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		want, err := exec.Command(bin).Output()
		if err != nil {
			t.Fatalf("%v\n%s", err, want)
		}
		return string(want)
	})
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPxMisc checks ndbm.h, devctl.h, ppoll, sig2str and str2sig
// (testdata/pxmisc.c) against what POSIX says: the host's musl has no
// ndbm.h, devctl.h or sig2str to compare with.
func TestPxMisc(t *testing.T) {
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/pxmisc.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	const want = `open 1
store 0 0 0
insert again 1, replace 0
fetch a ay
fetch zz 1
delete b 0, again -1
reopened: a=ay c=see
readonly store -1 error 1 cleared 0
missing 1
ppoll timeout 0
ppoll ready 1 1
devctl 0 3 0
devctl tcgets 1
1 HUP 1
15 TERM 15
17 CHLD 17
34 RTMIN 34
40 RTMIN+6 40
60 RTMAX-4 60
64 RTMAX 64
str2sig 9 0 9, bad -1, SIGINT -1
`
	if got := goRun(t, dir); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestPxLocale compares locale objects and the _l functions, wide
// characters' widths, and POSIX's string, random number, crypt, time,
// Bessel and inet functions (testdata/pxlocale.c) with the host's C
// library.
func TestPxLocale(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoBuild(t)
	src, err := filepath.Abs("testdata/pxlocale.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	want := hostWant(t, gcc, "pxlocale", func() string {
		bin := filepath.Join(t.TempDir(), "pxlocale")
		// glibc's crypt is libcrypt's, musl's libc's.
		args := []string{"-std=gnu2x", "-w", "-pthread", "-o", bin, src, "-lm"}
		if out, err := exec.Command(gcc, append(args, "-lcrypt")...).CombinedOutput(); err != nil {
			if out2, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
				t.Fatalf("gcc: %v\n%s%s", err, out, out2)
			}
		}
		cmd := exec.Command(bin)
		cmd.Dir = t.TempDir() // the program makes files
		want, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(want)
	})
	ccbin := filepath.Join(t.TempDir(), "pxlocale")
	build := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", ccbin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(ccbin)
	cmd.Dir = t.TempDir()
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPxThreads compares POSIX's threads, timers and signals
// (testdata/pxthreads.c: attributes, read-write locks, barriers, spin
// locks, cancellation, robust mutexes, timer_create, clock_nanosleep,
// killpg, sigset and sigsetjmp) with the host's C library, on Linux.
func TestPxThreads(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the test compares with Linux's C library")
	}
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/pxthreads.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	want := hostWant(t, gcc, "pxthreads", func() string {
		bin := filepath.Join(t.TempDir(), "pxthreads")
		if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-pthread", "-o", bin, src, "-lrt").CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		want, err := exec.Command(bin).Output()
		if err != nil {
			t.Fatalf("%v\n%s", err, want)
		}
		return string(want)
	})
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestFenv compares the floating-point environment (Annex F: rounding
// directions, exceptions, the library's conversions) with gcc's, on the
// architectures that have one.
func TestFenv(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("no rounding directions or exceptions")
	}
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/fenv.c", true)
	bin := filepath.Join(t.TempDir(), "fenv")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-frounding-math", "-w", "-o", bin, "testdata/fenv.c", "-lm").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	want, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestFenvRound checks C23's FENV_ROUND, which gcc does not have: the
// values are gcc's with fesetround instead.
func TestFenvRound(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("no rounding directions")
	}
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/fenvround.c", true)
	want := "0x1.5555555555556p-2 0x1.bb67ae8584cabp+0 0.3 0x1.555556p-2\n" +
		"-0x1.5555555555555p-2 0x0p+0\n" +
		"0x1.5555555555555p-2 0.2\n"
	if got := goRun(t, dir); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestLibc compares the output of C's library, which internal/c23
// implements, with gcc's and its C library's.
func TestLibc(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/libc.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	bin := filepath.Join(t.TempDir(), "libc")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, src, "-lm").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir() // the program writes files
	want, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestUtil compares the common extensions of glibc and musl (getopt_long,
// err.h, glob.h, search.h, iconv.h, getline, fmemopen and others) of
// testdata/util.c with gcc's (and its C library's). Both programs run in
// a directory of their own, where they write files, and only their
// standard output is compared.
func TestUtil(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoBuild(t)
	src, err := filepath.Abs("testdata/util.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	bin := filepath.Join(t.TempDir(), "util")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir()
	want, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	ccbin := filepath.Join(t.TempDir(), "util")
	build := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", ccbin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd = exec.Command(ccbin)
	cmd.Dir = t.TempDir()
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestFsinfo compares file systems' and the system's information, file
// tree walks, wordexp and the small headers of glibc and musl
// (testdata/fsinfo.c) with the host's C library.
func TestFsinfo(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	testenv.MustHaveGoBuild(t)
	src, err := filepath.Abs("testdata/fsinfo.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	want := hostWant(t, gcc, "fsinfo", func() string {
		bin := filepath.Join(t.TempDir(), "fsinfo")
		if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-o", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		cmd := exec.Command(bin)
		cmd.Dir = t.TempDir() // the program makes files
		want, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(want)
	})
	ccbin := filepath.Join(t.TempDir(), "fsinfo")
	build := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", ccbin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(ccbin)
	cmd.Dir = t.TempDir()
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPxFiles compares the rest of POSIX's files and system interfaces
// (testdata/pxfiles.c: the *at functions, dirent.h's, dup3 and pipe2,
// stream locks, limits, shared memory objects, the user, group and
// network databases, pseudo-terminals) with the host's C library. Both
// programs run in a directory of their own, where they make files.
func TestPxFiles(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoBuild(t)
	src, err := filepath.Abs("testdata/pxfiles.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	want := hostWant(t, gcc, "pxfiles", func() string {
		bin := filepath.Join(t.TempDir(), "pxfiles")
		if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-pthread", "-o", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		cmd := exec.Command(bin)
		cmd.Dir = t.TempDir()
		want, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v\n%s", err, want)
		}
		return string(want)
	})
	ccbin := filepath.Join(t.TempDir(), "pxfiles")
	build := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", ccbin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(ccbin)
	cmd.Dir = t.TempDir()
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestProc compares processes, signals, scheduling and semaphores
// (sys/wait.h, spawn.h, the exec family, sigaction, sched.h,
// semaphore.h) with the host's C library.
func TestProc(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/proc.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	bin := filepath.Join(t.TempDir(), "proc")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-pthread", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir() // the program writes files
	want, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, want)
	}
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestEvents compares epoll, eventfd, timerfd, signalfd, waitid and
// wait4, sigtimedwait and its kin, pthread_kill, sigaltstack and
// setitimer (testdata/events.c) with the host's C library, on Linux.
func TestEvents(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the host's C library has epoll only on Linux")
	}
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/events.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	want := hostWant(t, gcc, "events", func() string {
		bin := filepath.Join(t.TempDir(), "events")
		if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-pthread", "-o", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		cmd := exec.Command(bin)
		cmd.Dir = t.TempDir() // the program writes a file
		want, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v\n%s", err, want)
		}
		return string(want)
	})
	if got := goRun(t, dir); got != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPxIPC compares message queues (mqueue.h), XSI IPC (sys/msg.h,
// sys/sem.h, sys/shm.h) and aio.h (testdata/pxipc.c) with the host's C
// library, on Linux: within one process, with threads as the other
// parties.
func TestPxIPC(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the host's message queues are Linux's")
	}
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("no gcc")
	}
	testenv.MustHaveGoRun(t)
	src, err := filepath.Abs("testdata/pxipc.c")
	if err != nil {
		t.Fatal(err)
	}
	dir := module(t, src, true)
	bin := filepath.Join(t.TempDir(), "pxipc")
	if out, err := exec.Command(gcc, "-std=gnu2x", "-w", "-pthread", "-o", bin, src, "-lrt").CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir() // the program writes files
	want, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, want)
	}
	cmd = testenv.Command(t, testenv.GoToolPath(t), "build", "-o", filepath.Join(dir, "pxipc"), ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd = exec.Command(filepath.Join(dir, "pxipc"))
	cmd.Dir = t.TempDir()
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Errorf("output differs from gcc's\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestInterop checks what Go and C may do with each other's pointers: an
// ordinary cgo package (testdata/interop), which go build compiles with
// cmd/cc because cgo is off, natively and on wasm (with node).
func TestInterop(t *testing.T) {
	// It tested what capabilities caught (C reading past Go's slice, an
	// integer dereferenced, a pointer kept in untyped bytes). They are
	// gone: C's pointers are Go's, checked as unsafe.Pointer is
	// (doc/gd/cc.md). To be written again for that.
	t.Skip("tested the capabilities' faults, which are gone")

	testenv.MustHaveGoRun(t)
	for _, target := range []struct {
		name string
		env  []string
		word int // the size of a pointer and of C's long (cmd/cc's target.go)
	}{
		{"native", nil, 8},
		{"wasm-linear", []string{"GOOS=js", "GOARCH=wasm", "GOWASM=linear"}, 8},
		{"wasm-split", []string{"GOOS=js", "GOARCH=wasm"}, 4},
		{"wasm-split-ptr64", []string{"GOOS=js", "GOARCH=wasm", "GOWASM=gc,split,ptr64"}, 8},
	} {
		t.Run(target.name, func(t *testing.T) {
			wasm := target.env != nil
			if wasm {
				if _, err := exec.LookPath("node"); err != nil {
					t.Skip("no node")
				}
			}
			testInterop(t, target.env, strings.HasPrefix(target.name, "wasm-split"), target.word)
		})
	}
}

// wasm reports whether env is that of a GOARCH=wasm variant.
func wasm(env []string) bool { return env != nil }

func testInterop(t *testing.T, env []string, split bool, word int) {
	dir := t.TempDir()
	files := map[string]string{"go.mod": "module cctest\n\ngo 1.27\n"}
	ents, err := os.ReadDir("testdata/interop")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join("testdata/interop", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	cmd := testenv.Command(t, testenv.GoToolPath(t), "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "NODE_OPTIONS=")
	if env != nil {
		cmd.Env = append(cmd.Env, env...)
		cmd.Env = append(cmd.Env, "PATH="+filepath.Join(testenv.GOROOT(t), "lib", "wasm")+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, b)
	}
	out := string(b)
	// What depends on the word: C's long (a sum that wraps with 32 bits),
	// struct node and main.goThing (an int64 and a slice).
	sum := int64(5000050000)
	if word == 4 {
		sum = int64(int32(sum))
	}
	thing := (8 + 3*word + 7) / 8 * 8
	if !wasm(env) && word == 8 {
		// gd: a slice is two words with 8-byte words but on wasm,
		// aligned as the slice is (doc/gd/slice16.md).
		thing = int(unsafe.Sizeof(struct {
			n    int64
			data []int32
		}{}))
	}
	for _, want := range []string{
		"C's list in Go: 1 2 3 4 5\n",
		"Go's list summed by C: 60\n",
		fmt.Sprintf("after 3 collections: Go 5000050000 C %d\n", sum),
		"Go's stack pair summed by C: 3\n",
		"C's locals: 213\n",
		"allocations per call: sumPair 0 locals 0\n",
		"30 C reads arr[2] of Go's slice                 ok",
		"C reads arr[3] of Go's slice                 trapped: c23: access of 4 bytes out of bounds of {int32, size 12, off 12}",
		"Go dereferences it                           trapped: runtime error: invalid memory address",
		"30 C brings it back in bounds (arr[2])          ok",
		"C dereferences it out of bounds              trapped",
		"30 ...stored in Go's heap object instead        ok",
		"30 ...stored in Go's stack, which then moves    ok",
		"30 C keeps a slice's array in Go's int* field   ok",
		"...and indexes past it                       trapped: c23: access of 4 bytes out of bounds of {int32, size 12, off 12}",
		"60 C one-based indexing                         ok",
		"C writes an integer over a pointer word      trapped: c23: scalar store over a pointer word",
		"C dereferences an integer                    trapped: c23: access through a pointer with no capability",
		"C keeps a pointer in untyped bytes           ok",
		"variant's list, after a collection: 10 type 2 bytes 8\n",
		fmt.Sprintf("C reads the double as a pointer              trapped: c23: access of 4 bytes out of bounds of {C.struct_node, size %d", 2*word),
		"objects in a pool, after a collection: 9\n",
		"Go sees the pool's objects only as bytes     trapped: runtime error: invalid memory address",
		"C puns a pointer word of a stack local       trapped: c23: scalar store over a pointer word",
		"Go follows it                                trapped: runtime error: invalid memory address",
		"42 C uses it as the int it is                   ok",
		"C writes a plain field of a Go value         ok",
		"C writes the length of a Go slice            trapped: c23: store into a word of a Go value with invariants (main.goThing)",
		fmt.Sprintf("C writes past a Go value                     trapped: c23: access of %d bytes out of bounds of {main.goThing, size %d, off %d}", word, thing, thing),
		"Go's value after: 7 2\n",
		"C frees what malloc and the like returned    ok",
		"C frees a pointer into its object            trapped: c23: free of a pointer into an object, not to its start: {c23.Raw, size 16, off 1}",
		"C frees a local                              trapped: c23: free of an object that is not an allocation of its own: {int32, size 4, off 0}",
		"C frees a pointer that went through a long   ok",
		"C reallocs a pointer gone through a long     trapped: c23: realloc of a pointer with no capability",
		"C reallocs a pointer into its object         trapped: c23: realloc of a pointer into an object, not to its start: {c23.Raw, size 16, off 8}",
		"C frees Go's slice                           trapped: c23: free of Go's memory: {int32, size 12, off 0}",
		"Go frees C's string                          ok",
		"Go frees its own slice                       trapped: c23: free of Go's memory",
	} {
		if split {
			// No panic to recover from (deref_split.go).
			if strings.Contains(want, "trapped: runtime error") {
				want = want[:strings.Index(want, "trapped:")] + "not tried"
			}
		}
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestInternalC23 checks that only Go that cc generated may import
// internal/c23.
func TestInternalC23(t *testing.T) {
	testenv.MustHaveGoRun(t)
	dir := module(t, "testdata/basic.c", true)
	if out := goRun(t, dir); !strings.Contains(out, "fib(20) = 6765") {
		t.Fatalf("generated code did not run:\n%s", out)
	}
	user := "package main\n\nimport \"internal/c23\"\n\nvar _ = c23.Malloc\n"
	if err := os.WriteFile(filepath.Join(dir, "user.go"), []byte(user), 0o666); err != nil {
		t.Fatal(err)
	}
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "use of internal package internal/c23 not allowed") {
		t.Fatalf("user code imported internal/c23: err %v\n%s", err, out)
	}
}

// TestBuildTag checks which import "C" packages go build compiles with
// cmd/cc when cgo is off: those it can, as if cgo were on (cgo files in,
// !cgo files out); the others as before, without their cgo files.
func TestBuildTag(t *testing.T) {
	testenv.MustHaveGoRun(t)
	for _, tc := range []struct {
		name, preamble, want string
	}{
		{"cc", `#include <stdio.h>
#include "add.h"`, "cgo: 5\n"},
		{"ldflags", `#cgo LDFLAGS: -lm
#include "add.h"`, "no cgo\n"},
		{"system header", `#include <X11/Xlib.h>
#include "add.h"`, "no cgo\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var pre strings.Builder
			for _, l := range strings.Split(tc.preamble, "\n") {
				pre.WriteString("// " + l + "\n")
			}
			files := map[string]string{
				"go.mod":   "module m\n\ngo 1.27\n",
				"add.h":    "int add(int a, int b);\n",
				"add.c":    "#include \"add.h\"\nint add(int a, int b) { return a + b; }\n",
				"cgo.go":   "package main\n\n" + pre.String() + "import \"C\"\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"cgo:\", C.add(2, 3)) }\n",
				"nocgo.go": "//go:build !cgo\n\npackage main\n\nfunc main() { println(\"no cgo\") }\n",
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o666); err != nil {
					t.Fatal(err)
				}
			}
			cmd := testenv.Command(t, testenv.GoToolPath(t), "run", ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go run: %v\n%s", err, out)
			}
			if string(out) != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
		})
	}
}

// hostWant returns the output that a test program of testdata/name.c
// should have: that of run, which builds it with gcc and runs it, where
// the host's C library is musl, and otherwise musl's recorded in
// testdata/name.musl. The programs follow musl where POSIX leaves the
// behavior to the implementation, and use what POSIX 2024 added, which
// glibc lacks in part. On musl, the recorded output must be run's: set
// CC_UPDATE_MUSL=1 to write it.
func hostWant(t *testing.T, gcc, name string, run func() string) string {
	t.Helper()
	file := filepath.Join("testdata", name+".musl")
	machine, err := exec.Command(gcc, "-dumpmachine").Output()
	if err != nil || !strings.Contains(string(machine), "musl") {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := run()
	if os.Getenv("CC_UPDATE_MUSL") != "" {
		if err := os.WriteFile(file, []byte(want), 0o666); err != nil {
			t.Fatal(err)
		}
	} else if b, err := os.ReadFile(file); err != nil || string(b) != want {
		t.Errorf("%s differs from musl's output; run with CC_UPDATE_MUSL=1", file)
	}
	return want
}
