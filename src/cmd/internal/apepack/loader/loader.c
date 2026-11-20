// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The loader of a GOOS=ape program on its ELF hosts (Linux, FreeBSD).
//
// The script at the start of the program's file writes the loader to a
// file in a new directory and runs it as
//
//	loader file off ehdr argv0 [args]
//
// where the program's image is at off in file and the image's ELF header
// at ehdr in file (both decimal). The loader removes its own file and
// directory, maps the image's segments from file as the kernel would map
// an ELF executable's, and starts the program with the stack the kernel
// would have given it for argv0 and args, and one more auxiliary vector
// entry, AT_APE, which points at file's name (os.Executable's answer).
//
// It is built by mkloader.sh into ../zloader.go: freestanding, with no
// data, at an address no image is at.

typedef unsigned long u64;
typedef long i64;
typedef unsigned int u32;
typedef unsigned short u16;
typedef unsigned char u8;

#define AT_NULL 0
#define AT_PAGESZ 6
#define AT_EXECPATH_FREEBSD 15
#define AT_EXECFN_LINUX 31
#define AT_APE 0x657061 // "ape"

#define NARG 4 // the loader's own arguments, argv[0] too: an even number

#define PROT_READ 1
#define PROT_WRITE 2
#define PROT_EXEC 4
#define MAP_PRIVATE 2
#define MAP_FIXED 0x10

#define PT_LOAD 1
#define PF_X 1
#define PF_W 2
#define PF_R 4

struct ehdr {
	u8 ident[16];
	u16 type, machine;
	u32 version;
	u64 entry, phoff, shoff;
	u32 flags;
	u16 ehsize, phentsize, phnum, shentsize, shnum, shstrndx;
};

struct phdr {
	u32 type, flags;
	u64 offset, vaddr, paddr, filesz, memsz, align;
};

struct start {
	u64 entry;
	u64 *sp;
};

// sys numbers, by system.
enum { Sopen, Sclose, Smmap, Smprotect, Spread, Sunlink, Srmdir, Sexit, Swrite, Snum };

#if defined(__x86_64__)

// The kernel starts the loader with the address of argc in SP (Linux,
// which has DI zero) or in DI (FreeBSD), and the program is started the
// same way.
__asm__(
	".globl _start\n"
	"_start:\n"
	"	mov %rdi, %rbx\n" // FreeBSD?
	"	mov %rdi, %rsi\n"
	"	test %rdi, %rdi\n"
	"	jnz 1f\n"
	"	mov %rsp, %rdi\n"
	"1:	and $-16, %rsp\n"
	"	call load\n"
	"	mov %rdx, %rsp\n"
	"	xor %edi, %edi\n"
	"	test %rbx, %rbx\n"
	"	jz 2f\n"
	"	mov %rdx, %rdi\n"
	"	and $-16, %rsp\n"
	"	sub $8, %rsp\n"
	"2:	xor %edx, %edx\n"
	"	xor %ebx, %ebx\n"
	"	jmp *%rax\n");

static const u16 sysnum[2][Snum] = {
	{2, 3, 9, 10, 17, 87, 84, 60, 1},     // Linux
	{5, 6, 477, 74, 475, 10, 137, 1, 4}, // FreeBSD
};

#define MAP_ANON(freebsd) ((freebsd) ? 0x1000 : 0x20)

// sys returns the system call's result, or its error negated.
static i64 sys(int freebsd, int n, u64 a, u64 b, u64 c, u64 d, u64 e, u64 f) {
	register u64 r10 __asm__("r10") = d;
	register u64 r8 __asm__("r8") = e;
	register u64 r9 __asm__("r9") = f;
	i64 r = sysnum[freebsd][n];
	// FreeBSD says a failure with the carry flag, which Linux leaves.
	__asm__ volatile("clc\n\tsyscall\n\tjnc 1f\n\tneg %%rax\n1:"
		: "+a"(r), "+D"(a), "+S"(b), "+d"(c), "+r"(r10), "+r"(r8), "+r"(r9)
		:
		: "rcx", "r11", "memory", "cc");
	return r;
}

#elif defined(__aarch64__)

// The kernel (Linux) starts the loader with the address of argc in SP.
__asm__(
	".globl _start\n"
	"_start:\n"
	"	mov x0, sp\n"
	"	mov x1, #0\n"
	"	bl load\n"
	"	mov x16, x0\n"
	"	mov sp, x1\n"
	"	mov x0, #0\n"
	"	mov x1, #0\n"
	"	mov x30, #0\n"
	"	br x16\n");

#define AT_FDCWD (-100)
#define AT_REMOVEDIR 0x200

// (open, unlink and rmdir are openat and unlinkat.)
static const u16 sysnum[1][Snum] = {
	{56, 57, 222, 226, 67, 35, 35, 93, 64},
};

#define MAP_ANON(freebsd) 0x20

static i64 sys(int freebsd, int n, u64 a, u64 b, u64 c, u64 d, u64 e, u64 f) {
	if (n == Sopen) {
		c = b, b = a, a = AT_FDCWD;
	} else if (n == Sunlink || n == Srmdir) {
		c = n == Srmdir ? AT_REMOVEDIR : 0, b = a, a = AT_FDCWD;
	}
	register u64 x8 __asm__("x8") = sysnum[0][n];
	register u64 x0 __asm__("x0") = a;
	register u64 x1 __asm__("x1") = b;
	register u64 x2 __asm__("x2") = c;
	register u64 x3 __asm__("x3") = d;
	register u64 x4 __asm__("x4") = e;
	register u64 x5 __asm__("x5") = f;
	__asm__ volatile("svc #0"
		: "+r"(x0)
		: "r"(x8), "r"(x1), "r"(x2), "r"(x3), "r"(x4), "r"(x5)
		: "memory", "cc");
	return x0;
}

#else
#error "no loader for this machine"
#endif

static u64 len(const char *s) {
	u64 n = 0;
	while (s[n])
		n++;
	return n;
}

__attribute__((noreturn)) static void die(int freebsd, const char *file, const char *msg) {
	static const char pre[] = ": GOOS=ape loader: ";
	sys(freebsd, Swrite, 2, (u64)file, len(file), 0, 0, 0);
	sys(freebsd, Swrite, 2, (u64)pre, sizeof pre - 1, 0, 0, 0);
	sys(freebsd, Swrite, 2, (u64)msg, len(msg), 0, 0, 0);
	sys(freebsd, Swrite, 2, (u64) "\n", 1, 0, 0, 0);
	for (;;)
		sys(freebsd, Sexit, 127, 0, 0, 0, 0, 0);
}

// num returns the decimal number s, or -1.
static i64 num(const char *s) {
	i64 n = 0;
	if (!*s)
		return -1;
	for (; *s; s++) {
		if (*s < '0' || *s > '9')
			return -1;
		n = n * 10 + (*s - '0');
	}
	return n;
}

// readat reads n bytes at off of fd to p.
static int readat(int freebsd, int fd, u8 *p, u64 n, u64 off) {
	while (n > 0) {
		i64 r = sys(freebsd, Spread, fd, (u64)p, n, off, 0, 0);
		if (r <= 0)
			return -1;
		p += r, off += r, n -= r;
	}
	return 0;
}

static int mapfailed(i64 r) {
	return r < 0 && r > -4096;
}

struct start load(u64 *sp, u64 freebsdsp) {
	int freebsd = freebsdsp != 0;
	u64 argc = sp[0];
	char **argv = (char **)(sp + 1);
	if (argc < NARG + 1)
		die(freebsd, argc > 0 ? argv[0] : "ape", "run by the program's file, not by hand");
	char *self = argv[0], *file = argv[1];
	i64 off = num(argv[2]), eoff = num(argv[3]);

	// The loader's file and directory are of no more use.
	sys(freebsd, Sunlink, (u64)self, 0, 0, 0, 0, 0);
	char *slash = 0;
	for (char *p = self; *p; p++)
		if (*p == '/')
			slash = p;
	if (slash) {
		*slash = 0;
		sys(freebsd, Srmdir, (u64)self, 0, 0, 0, 0, 0);
		*slash = '/';
	}

	if (off < 0 || eoff < 0)
		die(freebsd, file, "bad arguments");

	// The auxiliary vector, after argv and the environment.
	u64 *av = sp + 1 + argc + 1;
	while (*av)
		av++;
	av++;
	u64 pagesz = 4096;
	u64 *avend = av;
	for (; avend[0] != AT_NULL; avend += 2)
		if (avend[0] == AT_PAGESZ)
			pagesz = avend[1];

	int fd = sys(freebsd, Sopen, (u64)file, 0 /* O_RDONLY */, 0, 0, 0, 0);
	if (fd < 0)
		die(freebsd, file, "cannot open the file");
	struct ehdr eh;
	struct phdr ph[16];
	if (readat(freebsd, fd, (u8 *)&eh, sizeof eh, eoff) < 0 ||
		eh.ident[0] != 0x7f || eh.ident[1] != 'E' || eh.ident[2] != 'L' || eh.ident[3] != 'F' ||
		eh.phentsize != sizeof ph[0] || eh.phnum > sizeof ph / sizeof ph[0] ||
		readat(freebsd, fd, (u8 *)ph, eh.phnum * sizeof ph[0], off + eh.phoff) < 0)
		die(freebsd, file, "no image of the program where the file says");

	for (int i = 0; i < eh.phnum; i++) {
		struct phdr *p = &ph[i];
		if (p->type != PT_LOAD || p->memsz == 0)
			continue;
		int prot = (p->flags & PF_R ? PROT_READ : 0) | (p->flags & PF_W ? PROT_WRITE : 0) | (p->flags & PF_X ? PROT_EXEC : 0);
		u64 start = p->vaddr & -pagesz;
		u64 fileend = p->vaddr + p->filesz;
		u64 end = (p->vaddr + p->memsz + pagesz - 1) & -pagesz;
		u64 foff = off + p->offset - (p->vaddr - start);
		u64 mapped = start; // end of what is mapped from the file
		if (p->filesz > 0 && foff % pagesz == 0) {
			if (mapfailed(sys(freebsd, Smmap, start, fileend - start, prot, MAP_PRIVATE | MAP_FIXED, fd, foff)))
				die(freebsd, file, "cannot map the program");
			mapped = (fileend + pagesz - 1) & -pagesz;
			// The rest of the last page is not the file's.
			if (mapped > fileend && p->memsz > p->filesz && prot & PROT_WRITE)
				for (u8 *z = (u8 *)fileend; z < (u8 *)mapped; z++)
					*z = 0;
		}
		if (end > mapped) {
			if (mapfailed(sys(freebsd, Smmap, mapped, end - mapped, mapped == start ? PROT_READ | PROT_WRITE : prot, MAP_PRIVATE | MAP_FIXED | MAP_ANON(freebsd), -1, 0)))
				die(freebsd, file, "cannot map the program");
		}
		if (p->filesz > 0 && mapped == start) {
			// The file's pages are not the system's: read them.
			if (readat(freebsd, fd, (u8 *)p->vaddr, p->filesz, off + p->offset) < 0)
				die(freebsd, file, "cannot read the program");
			if (prot != (PROT_READ | PROT_WRITE) && sys(freebsd, Smprotect, start, end - start, prot, 0, 0, 0) < 0)
				die(freebsd, file, "cannot map the program");
		}
	}
	sys(freebsd, Sclose, fd, 0, 0, 0, 0, 0);

	// The stack: argc, argv, 0, the environment, 0, the auxiliary
	// vector. Without the loader's NARG arguments it is NARG words
	// shorter, with the AT_APE entry two words longer.
	for (u64 *a = av; a < avend; a += 2)
		if (a[0] == (freebsd ? AT_EXECPATH_FREEBSD : AT_EXECFN_LINUX))
			a[1] = (u64)file;
	u64 *to = sp + NARG - 2;
	*to++ = argc - NARG;
	for (u64 *from = sp + 1 + NARG; from < avend;)
		*to++ = *from++;
	*to++ = AT_APE;
	*to++ = (u64)file;
	// (to is at the AT_NULL entry.)

	struct start s = {eh.entry, sp + NARG - 2};
	return s;
}
