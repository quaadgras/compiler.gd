// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The loader of a GOOS=ape program on darwin.
//
// The script at the start of the program's file keeps the loader in a
// file of its own (darwin checks a new executable's signature when it
// first runs, which takes a while: the loader is the same for every
// program, so it is checked once) and runs it as
//
//	loader file off argv0 [args]
//
// where the program's image, a Mach-O executable, is at off in file
// (decimal). The loader does for it what the kernel and dyld do for a
// Mach-O that is a file of its own: it gives the system the image's
// signature, maps the image's segments from file (somewhere else than
// they were linked for, where the loader is, so it rebases the image),
// binds what the image imports from the system's libraries, and calls
// its entry point with
// argv0 and args. The entry point (the runtime's) is told file's name in
// place of an environment: by a third argument that is nil and a fourth
// that is the name.
//
// It is built by mkloader.sh into ../zloader.go.

#include <stddef.h>
#include <stdint.h>

// (Declared here, not included: the loader is built without the SDK.)
int open(const char *, int, ...);
int close(int);
int fcntl(int, int, ...);
long pread(int, void *, size_t, long long);
long write(int, const void *, size_t);
void *mmap(void *, size_t, int, int, int, long long);
int mprotect(void *, size_t, int);
void *dlopen(const char *, int);
void *dlsym(void *, const char *);
void _exit(int) __attribute__((noreturn));

#define PROT_READ 1
#define PROT_WRITE 2
#define MAP_PRIVATE 2
#define MAP_FIXED 0x10
#define MAP_ANON 0x1000
#define MAP_FAILED ((void *)-1)
#define RTLD_LAZY 1
#define RTLD_DEFAULT ((void *)-2)

#define MH_MAGIC_64 0xfeedfacf
#define LC_SEGMENT_64 0x19
#define LC_LOAD_DYLIB 0xc
#define LC_MAIN 0x80000028
#define LC_DYLD_INFO_ONLY 0x80000022
#define LC_CODE_SIGNATURE 0x1d
#define SG_READ_ONLY 0x10
#define F_ADDFILESIGS 61

struct mach_header_64 {
	uint32_t magic, cputype, cpusubtype, filetype, ncmds, sizeofcmds, flags, reserved;
};

struct load_command {
	uint32_t cmd, cmdsize;
};

struct segment_command_64 {
	uint32_t cmd, cmdsize;
	char segname[16];
	uint64_t vmaddr, vmsize, fileoff, filesize;
	uint32_t maxprot, initprot, nsects, flags;
};

struct dylib_command {
	uint32_t cmd, cmdsize, name, timestamp, current_version, compatibility_version;
};

struct entry_point_command {
	uint32_t cmd, cmdsize;
	uint64_t entryoff, stacksize;
};

struct linkedit_data_command {
	uint32_t cmd, cmdsize, dataoff, datasize;
};

struct fsignatures {
	long long file_start;
	void *blob_start;
	size_t blob_size;
	size_t pad[5]; // of later systems' fields
};

struct dyld_info_command {
	uint32_t cmd, cmdsize;
	uint32_t rebase_off, rebase_size, bind_off, bind_size, weak_bind_off, weak_bind_size;
	uint32_t lazy_bind_off, lazy_bind_size, export_off, export_size;
};

#define PAGE 0x4000UL
#define MAXCMDS 0x4000 // bytes of load commands
#define MAXSEGS 16
#define MAXLIBS 64

static size_t len(const char *s) {
	size_t n = 0;
	while (s[n])
		n++;
	return n;
}

__attribute__((noreturn)) static void die(const char *file, const char *msg, const char *what) {
	static const char pre[] = ": GOOS=ape loader: ";
	write(2, file, len(file));
	write(2, pre, sizeof pre - 1);
	write(2, msg, len(msg));
	if (what)
		write(2, what, len(what));
	write(2, "\n", 1);
	_exit(127);
}

static int readat(int fd, void *p, size_t n, long long off) {
	char *b = p;
	while (n > 0) {
		long r = pread(fd, b, n, off);
		if (r <= 0)
			return -1;
		b += r, off += r, n -= r;
	}
	return 0;
}

static uint64_t uleb(const uint8_t **p, const uint8_t *end) {
	uint64_t v = 0;
	for (int shift = 0; *p < end; shift += 7) {
		uint8_t b = *(*p)++;
		v |= (uint64_t)(b & 0x7f) << shift;
		if (!(b & 0x80))
			break;
	}
	return v;
}

static int64_t sleb(const uint8_t **p, const uint8_t *end) {
	int64_t v = 0;
	int shift = 0;
	uint8_t b = 0;
	while (*p < end) {
		b = *(*p)++;
		v |= (int64_t)(b & 0x7f) << shift;
		shift += 7;
		if (!(b & 0x80))
			break;
	}
	if (shift < 64 && (b & 0x40))
		v |= -((int64_t)1 << shift);
	return v;
}

// mapped reports whether s is in memory (__PAGEZERO and __DWARF are not).
static int mapped(struct segment_command_64 *s) {
	return s->initprot != 0 && s->vmsize != 0;
}

// within reports whether size bytes at off of the image are in s.
static int within(struct segment_command_64 *s, uint64_t off, uint64_t size) {
	return off >= s->fileoff && size <= s->filesize && off - s->fileoff <= s->filesize - size;
}

static char cmds[MAXCMDS];

int main(int argc, char **argv) {
	if (argc < 4)
		die(argc > 0 ? argv[0] : "ape", "run by the program's file, not by hand", 0);
	const char *file = argv[1];
	long long off = 0;
	for (const char *p = argv[2]; *p; p++) {
		if (*p < '0' || *p > '9')
			die(file, "bad arguments", 0);
		off = off * 10 + (*p - '0');
	}

	int fd = open(file, 0 /* O_RDONLY */);
	if (fd < 0)
		die(file, "cannot open the file", 0);
	struct mach_header_64 mh;
	if (readat(fd, &mh, sizeof mh, off) < 0 || mh.magic != MH_MAGIC_64 || mh.sizeofcmds > MAXCMDS ||
		readat(fd, cmds, mh.sizeofcmds, off + sizeof mh) < 0)
		die(file, "no image of the program where the file says", 0);

	struct segment_command_64 *segs[MAXSEGS], *linkedit = 0;
	const char *libs[MAXLIBS];
	int nsegs = 0, nlibs = 0;
	struct dyld_info_command *info = 0;
	struct linkedit_data_command *sig = 0;
	uint64_t lo = -1, hi = 0, base = 0, entry = 0;
	int haveentry = 0;
	char *c = cmds;
	for (uint32_t i = 0; i < mh.ncmds; i++) {
		struct load_command *lc = (struct load_command *)c;
		if (c + sizeof *lc > cmds + mh.sizeofcmds || lc->cmdsize < sizeof *lc || c + lc->cmdsize > cmds + mh.sizeofcmds)
			die(file, "bad image", 0);
		switch (lc->cmd) {
		case LC_SEGMENT_64: {
			struct segment_command_64 *s = (struct segment_command_64 *)c;
			if (nsegs == MAXSEGS)
				die(file, "bad image", 0);
			segs[nsegs++] = s;
			if (!mapped(s))
				break;
			if (s->vmaddr % PAGE || s->fileoff % PAGE || s->filesize > s->vmsize)
				die(file, "bad image", 0);
			if (s->fileoff == 0 && s->filesize > 0)
				base = s->vmaddr;
			if (s->segname[2] == 'L' && s->segname[3] == 'I' && s->segname[4] == 'N')
				linkedit = s;
			if (s->vmaddr < lo)
				lo = s->vmaddr;
			if (s->vmaddr + s->vmsize > hi)
				hi = s->vmaddr + s->vmsize;
			break;
		}
		case LC_LOAD_DYLIB:
			if (nlibs == MAXLIBS)
				die(file, "bad image", 0);
			libs[nlibs++] = c + ((struct dylib_command *)c)->name;
			break;
		case LC_MAIN:
			entry = ((struct entry_point_command *)c)->entryoff;
			haveentry = 1;
			break;
		case LC_DYLD_INFO_ONLY:
			info = (struct dyld_info_command *)c;
			break;
		case LC_CODE_SIGNATURE:
			sig = (struct linkedit_data_command *)c;
			break;
		}
		c += lc->cmdsize;
	}
	if (!haveentry || base == 0 || hi <= lo)
		die(file, "bad image", 0);

	// The system only maps code from a file whose signature it has:
	// the image's, of the part of the file the image is.
	if (sig) {
		struct fsignatures fs = {off, (void *)(uintptr_t)sig->dataoff, sig->datasize};
		if (fcntl(fd, F_ADDFILESIGS, &fs) < 0)
			die(file, "the system does not take the program's signature", 0);
	}

	// Map the segments where there is room for them all: the loader
	// is where the image was linked to be.
	hi = (hi + PAGE - 1) & -PAGE;
	void *room = mmap(0, hi - lo, 0, MAP_PRIVATE | MAP_ANON, -1, 0);
	if (room == MAP_FAILED)
		die(file, "cannot map the program", 0);
	uint64_t slide = (uint64_t)room - lo;
	for (int i = 0; i < nsegs; i++) {
		struct segment_command_64 *s = segs[i];
		if (!mapped(s))
			continue;
		uint64_t addr = s->vmaddr + slide;
		uint64_t end = (addr + s->vmsize + PAGE - 1) & -PAGE;
		uint64_t fend = (addr + s->filesize + PAGE - 1) & -PAGE;
		// Writable while its addresses are set.
		int prot = s->initprot | (s->flags & SG_READ_ONLY ? PROT_WRITE : 0);
		if (fend > addr && mmap((void *)addr, fend - addr, prot, MAP_PRIVATE | MAP_FIXED, fd, off + s->fileoff) == MAP_FAILED)
			die(file, "cannot map the program: ", s->segname);
		if (end > fend && mmap((void *)fend, end - fend, prot, MAP_PRIVATE | MAP_FIXED | MAP_ANON, -1, 0) == MAP_FAILED)
			die(file, "cannot map the program: ", s->segname);
		// What follows the file's part in its last page is zero.
		if (s->filesize < s->vmsize && (s->initprot & PROT_WRITE))
			for (char *z = (char *)(addr + s->filesize); z < (char *)fend; z++)
				*z = 0;
	}
	close(fd);

	if (info && (!linkedit ||
		!within(linkedit, info->rebase_off, info->rebase_size) ||
		!within(linkedit, info->bind_off, info->bind_size)))
		die(file, "bad image", 0);

	// Rebase: add slide to the addresses in the image's data.
	if (info && info->rebase_size > 0 && slide != 0) {
		const uint8_t *p = (const uint8_t *)(linkedit->vmaddr + slide + (info->rebase_off - linkedit->fileoff));
		const uint8_t *end = p + info->rebase_size;
		uint64_t addr = 0, segend = 0;
		int done = 0;
		while (p < end && !done) {
			uint8_t op = *p & 0xf0, imm = *p & 0x0f;
			p++;
			uint64_t count = 0, skip = 0;
			switch (op) {
			case 0x00: // DONE
				done = 1;
				break;
			case 0x10: // SET_TYPE_IMM
				if (imm != 1) // REBASE_TYPE_POINTER
					die(file, "bad image", 0);
				break;
			case 0x20: // SET_SEGMENT_AND_OFFSET_ULEB
				if (imm >= nsegs || !mapped(segs[imm]))
					die(file, "bad image", 0);
				addr = segs[imm]->vmaddr + slide + uleb(&p, end);
				segend = segs[imm]->vmaddr + slide + segs[imm]->vmsize;
				break;
			case 0x30: // ADD_ADDR_ULEB
				addr += uleb(&p, end);
				break;
			case 0x40: // ADD_ADDR_IMM_SCALED
				addr += imm * 8;
				break;
			case 0x50: // DO_REBASE_IMM_TIMES
				count = imm;
				break;
			case 0x60: // DO_REBASE_ULEB_TIMES
				count = uleb(&p, end);
				break;
			case 0x70: // DO_REBASE_ADD_ADDR_ULEB
				count = 1;
				skip = uleb(&p, end);
				break;
			case 0x80: // DO_REBASE_ULEB_TIMES_SKIPPING_ULEB
				count = uleb(&p, end);
				skip = uleb(&p, end);
				break;
			default:
				die(file, "bad image", 0);
			}
			for (; count > 0; count--) {
				if (addr + 8 > segend)
					die(file, "bad image", 0);
				*(uint64_t *)addr += slide;
				addr += 8 + skip;
			}
		}
	}

	// Bind: set the addresses of what the image imports.
	if (info && info->bind_size > 0) {
		void *handles[MAXLIBS];
		for (int i = 0; i < nlibs; i++)
			if (!(handles[i] = dlopen(libs[i], RTLD_LAZY)))
				die(file, "cannot load ", libs[i]);
		const uint8_t *p = (const uint8_t *)(linkedit->vmaddr + slide + (info->bind_off - linkedit->fileoff));
		const uint8_t *end = p + info->bind_size;
		const char *name = 0;
		int64_t ordinal = 0, addend = 0;
		uint64_t addr = 0, segend = 0;
		int done = 0;
		while (p < end && !done) {
			uint8_t op = *p & 0xf0, imm = *p & 0x0f;
			p++;
			uint64_t count = 1, skip = 0;
			int bind = 0;
			switch (op) {
			case 0x00: // DONE
				done = 1;
				break;
			case 0x10: // SET_DYLIB_ORDINAL_IMM
				ordinal = imm;
				break;
			case 0x20: // SET_DYLIB_ORDINAL_ULEB
				ordinal = uleb(&p, end);
				break;
			case 0x30: // SET_DYLIB_SPECIAL_IMM
				ordinal = imm ? (int8_t)(0xf0 | imm) : 0;
				break;
			case 0x40: // SET_SYMBOL_TRAILING_FLAGS_IMM
				name = (const char *)p;
				while (p < end && *p)
					p++;
				p++;
				break;
			case 0x50: // SET_TYPE_IMM
				if (imm != 1) // BIND_TYPE_POINTER
					die(file, "bad image", 0);
				break;
			case 0x60: // SET_ADDEND_SLEB
				addend = sleb(&p, end);
				break;
			case 0x70: // SET_SEGMENT_AND_OFFSET_ULEB
				if (imm >= nsegs || !mapped(segs[imm]))
					die(file, "bad image", 0);
				addr = segs[imm]->vmaddr + slide + uleb(&p, end);
				segend = segs[imm]->vmaddr + slide + segs[imm]->vmsize;
				break;
			case 0x80: // ADD_ADDR_ULEB
				addr += uleb(&p, end);
				break;
			case 0x90: // DO_BIND
				bind = 1;
				break;
			case 0xa0: // DO_BIND_ADD_ADDR_ULEB
				bind = 1;
				skip = uleb(&p, end);
				break;
			case 0xb0: // DO_BIND_ADD_ADDR_IMM_SCALED
				bind = 1;
				skip = imm * 8;
				break;
			case 0xc0: // DO_BIND_ULEB_TIMES_SKIPPING_ULEB
				bind = 1;
				count = uleb(&p, end);
				skip = uleb(&p, end);
				break;
			default:
				die(file, "bad image", 0);
			}
			if (!bind)
				continue;
			if (!name || name[0] != '_')
				die(file, "bad image", 0);
			void *h = ordinal >= 1 && ordinal <= nlibs ? handles[ordinal - 1] : RTLD_DEFAULT;
			void *v = dlsym(h, name + 1);
			if (!v)
				v = dlsym(RTLD_DEFAULT, name + 1);
			if (!v)
				die(file, "the system has no ", name + 1);
			for (; count > 0; count--) {
				if (addr + 8 > segend)
					die(file, "bad image", 0);
				*(uint64_t *)addr = (uint64_t)v + addend;
				addr += 8 + skip;
			}
		}
	}
	for (int i = 0; i < nsegs; i++)
		if (mapped(segs[i]) && segs[i]->flags & SG_READ_ONLY)
			mprotect((void *)(segs[i]->vmaddr + slide), (segs[i]->vmsize + PAGE - 1) & -PAGE, segs[i]->initprot & ~PROT_WRITE);

	((void (*)(int, char **, void *, const char *))(base + slide + entry))(argc - 3, argv + 3, 0, file);
	_exit(127);
}
