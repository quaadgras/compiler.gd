// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strings"
	"sync"
)

// libc maps the functions of the headers cmd/cc provides to their Go.
var libc = map[string]string{
	"printf": "Printf", "fprintf": "Fprintf", "sprintf": "Sprintf", "snprintf": "Snprintf",
	"vprintf": "Vprintf", "vfprintf": "Vfprintf", "vsprintf": "Vsprintf", "vsnprintf": "Vsnprintf",
	"puts": "Puts", "fputs": "Fputs", "putchar": "Putchar", "fputc": "Fputc", "putc": "Fputc",
	"getchar": "Getchar", "fgetc": "Fgetc", "getc": "Fgetc", "fgets": "Fgets", "fwrite": "Fwrite",
	"fread": "Fread", "fflush": "Fflush",
	"malloc": "Malloc", "calloc": "Calloc", "realloc": "Realloc", "free": "Free",
	"abort": "Abort", "exit": "Exit", "_Exit": "ExitNow", "abs": "Abs", "labs": "Labs", "llabs": "Labs",
	"atoi": "Atoi", "atol": "Atol", "atoll": "Atol", "strtol": "Strtol", "strtoul": "Strtoul",
	"strtoll": "Strtol", "strtoull": "Strtoul", "strtod": "Strtod", "atof": "Atof",
	"rand": "Rand", "srand": "Srand", "getenv": "Getenv",
	"memcpy": "Memcpy", "memmove": "Memcpy", "memset": "Memset", "memcmp": "Memcmp", "memchr": "Memchr",
	"memccpy": "Memccpy",
	"strcpy":  "Strcpy", "strncpy": "Strncpy", "strcat": "Strcat", "strncat": "Strncat",
	"strcmp": "Strcmp", "strncmp": "Strncmp", "strlen": "Strlen", "strchr": "Strchr",
	"strrchr": "Strrchr", "strstr": "Strstr", "strdup": "Strdup", "strndup": "Strndup",
	"strspn": "Strspn", "strcspn": "Strcspn", "__cc_stdio": "Stdio",
	// complex.h
	"creal":  "Creal",
	"cimag":  "Cimag",
	"conj":   "Conj",
	"cabs":   "Cabs",
	"carg":   "Carg",
	"cproj":  "Cproj",
	"cexp":   "Cexp",
	"clog":   "Clog",
	"csqrt":  "Csqrt",
	"cpow":   "Cpow",
	"csin":   "Csin",
	"ccos":   "Ccos",
	"ctan":   "Ctan",
	"casin":  "Casin",
	"cacos":  "Cacos",
	"catan":  "Catan",
	"csinh":  "Csinh",
	"ccosh":  "Ccosh",
	"ctanh":  "Ctanh",
	"casinh": "Casinh",
	"cacosh": "Cacosh",
	"catanh": "Catanh",
	"crealf": "Crealf",
	"cimagf": "Cimagf",
	"conjf":  "Conjf",
	"cabsf":  "Cabsf",
	"cargf":  "Cargf",
	"cexpf":  "Cexpf",
	"clogf":  "Clogf",
	"csqrtf": "Csqrtf",
	"cpowf":  "Cpowf",
	"csinf":  "Csinf",
	"ccosf":  "Ccosf",
	"dlopen": "Dlopen", "dlsym": "Dlsym", "dlclose": "Dlclose", "dlerror": "Dlerror",
	"alloca": "Malloc",

	// pthread.h
	"pthread_create": "PthreadCreate", "pthread_join": "PthreadJoin", "pthread_detach": "PthreadDetach",
	"pthread_exit": "PthreadExit", "pthread_self": "PthreadSelf", "pthread_equal": "PthreadEqual",
	"pthread_mutex_init": "PthreadMutexInit", "pthread_mutex_destroy": "PthreadMutexDestroy",
	"pthread_mutex_lock": "PthreadMutexLock", "pthread_mutex_trylock": "PthreadMutexTrylock",
	"pthread_mutex_unlock": "PthreadMutexUnlock", "pthread_cond_init": "PthreadCondInit",
	"pthread_cond_destroy": "PthreadCondDestroy", "pthread_cond_wait": "PthreadCondWait",
	"pthread_cond_signal": "PthreadCondSignal", "pthread_cond_broadcast": "PthreadCondBroadcast",
	"pthread_mutexattr_init": "PthreadMutexattrInit", "pthread_mutexattr_destroy": "PthreadMutexattrDestroy",
	"pthread_mutexattr_settype": "PthreadMutexattrSettype", "pthread_mutexattr_gettype": "PthreadMutexattrGettype",
	"pthread_once": "PthreadOnce", "pthread_key_create": "PthreadKeyCreate", "pthread_key_delete": "PthreadKeyDelete",
	"pthread_getspecific": "PthreadGetspecific", "pthread_setspecific": "PthreadSetspecific",
	"sched_yield": "SchedYield",
	"longjmp":     "Longjmp", "_longjmp": "Longjmp", "siglongjmp": "Longjmp",
	"strnlen": "Strnlen", "strpbrk": "Strpbrk", "strcoll": "Strcoll", "strtok": "Strtok",
	"strtok_r": "StrtokR", "strerror": "Strerror", "memset_explicit": "MemsetExplicit",

	// stdio.h
	"fopen": "Fopen", "fclose": "Fclose", "tmpfile": "Tmpfile", "fseek": "Fseek", "ftell": "Ftell",
	"rewind": "Rewind", "feof": "Feof", "ferror": "Ferror", "clearerr": "Clearerr",
	"ungetc": "Ungetc", "setvbuf": "Setvbuf", "setbuf": "Setbuf", "remove": "Remove",
	"rename": "Rename", "perror": "Perror", "freopen": "Freopen", "tmpnam": "Tmpnam",
	"scanf": "Scanf", "fscanf": "Fscanf", "sscanf": "Sscanf", "vscanf": "Vscanf",
	"vfscanf": "Vfscanf", "vsscanf": "Vsscanf",

	// stdlib.h
	"strtof": "Strtof", "strtold": "Strtold", "atexit": "Atexit", "qsort": "Qsort",
	"bsearch": "Bsearch", "system": "System", "div": "Div", "ldiv": "Ldiv", "lldiv": "Ldiv",

	// locale.h, signal.h
	"setlocale": "Setlocale", "localeconv": "Localeconv", "signal": "Signal", "raise": "Raise",

	// errno.h
	"__cc_errno": "ErrnoLoc",

	// ctype.h
	"isalnum": "Isalnum", "isalpha": "Isalpha", "isblank": "Isblank", "iscntrl": "Iscntrl",
	"isdigit": "Isdigit", "isgraph": "Isgraph", "islower": "Islower", "isprint": "Isprint",
	"ispunct": "Ispunct", "isspace": "Isspace", "isupper": "Isupper", "isxdigit": "Isxdigit",
	"tolower": "Tolower", "toupper": "Toupper",

	// inttypes.h, time.h
	"imaxabs": "Labs", "strtoimax": "Strtol", "strtoumax": "Strtoul",
	"imaxdiv": "Ldiv",
	"time":    "Time", "clock": "Clock", "difftime": "Difftime", "mktime": "Mktime",
	"timegm": "Timegm", "gmtime": "Gmtime", "gmtime_r": "GmtimeR", "localtime": "Localtime",
	"localtime_r": "LocaltimeR", "asctime": "Asctime", "ctime": "Ctime", "strftime": "Strftime",
	"timespec_get": "TimespecGet", "timespec_getres": "TimespecGetres",

	// uchar.h, wchar.h
	"mbrtoc8": "Mbrtoc8", "c8rtomb": "C8rtomb", "mbrtoc16": "Mbrtoc16", "c16rtomb": "C16rtomb",
	"mbrtoc32": "Mbrtoc32", "c32rtomb": "C32rtomb", "mbrtowc": "Mbrtowc", "wcrtomb": "Wcrtomb",
	"mbrlen": "Mbrlen", "mbsinit": "Mbsinit", "btowc": "Btowc", "wctob": "Wctob",
	"wcslen": "Wcslen", "wcscmp": "Wcscmp", "wcsncmp": "Wcsncmp", "wcscpy": "Wcscpy",
	"wcscat": "Wcscat", "wcschr": "Wcschr",

	// math.h
	"__cc_fpclassify": "Fpclassify", "__cc_fpclassifyf": "Fpclassifyf", "__cc_signbit": "Signbit",
	"sin": "Sin", "cos": "Cos", "tan": "Tan", "asin": "Asin", "acos": "Acos", "atan": "Atan",
	"sinh": "Sinh", "cosh": "Cosh", "tanh": "Tanh", "asinh": "Asinh", "acosh": "Acosh",
	"atanh": "Atanh", "exp": "Exp", "exp2": "Exp2", "expm1": "Expm1", "log": "Log",
	"log2": "Log2", "log10": "Log10", "log1p": "Log1p", "logb": "Logb", "sqrt": "Sqrt",
	"cbrt": "Cbrt", "fabs": "Fabs", "floor": "Floor", "ceil": "Ceil", "round": "Round",
	"trunc": "Trunc", "rint": "Rint", "nearbyint": "Rint", "erf": "Erf", "erfc": "Erfc",
	"tgamma": "Tgamma", "lgamma": "Lgamma", "atan2": "Atan2", "pow": "Pow", "hypot": "Hypot",
	"fmod": "Fmod", "remainder": "Remainder", "fmin": "Fmin", "fmax": "Fmax", "fdim": "Fdim",
	"copysign": "Copysign", "nextafter": "Nextafter", "fma": "Fma", "ilogb": "Ilogb",
	"lround": "Lround", "lrint": "Lrint", "llround": "Lround", "llrint": "Lrint",
	"ldexp": "Ldexp", "scalbn": "Scalbn", "scalbln": "Scalbln", "frexp": "Frexp",
	"modf": "Modf", "nan": "Nan", "sinf": "Sinf", "cosf": "Cosf", "tanf": "Tanf",
	"asinf": "Asinf", "acosf": "Acosf", "atanf": "Atanf", "sinhf": "Sinhf", "coshf": "Coshf",
	"tanhf": "Tanhf", "asinhf": "Asinhf", "acoshf": "Acoshf", "atanhf": "Atanhf",
	"expf": "Expf", "exp2f": "Exp2f", "expm1f": "Expm1f", "logf": "Logf", "log2f": "Log2f",
	"log10f": "Log10f", "log1pf": "Log1pf", "logbf": "Logbf", "sqrtf": "Sqrtf",
	"cbrtf": "Cbrtf", "fabsf": "Fabsf", "floorf": "Floorf", "ceilf": "Ceilf",
	"roundf": "Roundf", "truncf": "Truncf", "rintf": "Rintf", "nearbyintf": "Rintf",
	"erff": "Erff", "erfcf": "Erfcf", "tgammaf": "Tgammaf", "lgammaf": "Lgammaf",
	"atan2f": "Atan2f", "powf": "Powf", "hypotf": "Hypotf", "fmodf": "Fmodf",
	"remainderf": "Remainderf", "fminf": "Fminf", "fmaxf": "Fmaxf", "fdimf": "Fdimf",
	"copysignf": "Copysignf", "nextafterf": "Nextafterf", "fmaf": "Fmaf", "ilogbf": "Ilogbf",
	"lroundf": "Lroundf", "lrintf": "Lrintf", "llroundf": "Lroundf", "llrintf": "Lrintf",
	"ldexpf": "Ldexpf", "scalbnf": "Scalbnf", "scalblnf": "Scalblnf", "frexpf": "Frexpf",
	"modff": "Modff", "nanf": "Nanf", "sinl": "Sin", "cosl": "Cos", "tanl": "Tan",
	"asinl": "Asin", "acosl": "Acos", "atanl": "Atan", "sinhl": "Sinh", "coshl": "Cosh",
	"tanhl": "Tanh", "asinhl": "Asinh", "acoshl": "Acosh", "atanhl": "Atanh", "expl": "Exp",
	"exp2l": "Exp2", "expm1l": "Expm1", "logl": "Log", "log2l": "Log2", "log10l": "Log10",
	"log1pl": "Log1p", "logbl": "Logb", "sqrtl": "Sqrt", "cbrtl": "Cbrt", "fabsl": "Fabs",
	"floorl": "Floor", "ceill": "Ceil", "roundl": "Round", "truncl": "Trunc", "rintl": "Rint",
	"nearbyintl": "Rint", "erfl": "Erf", "erfcl": "Erfc", "tgammal": "Tgamma",
	"lgammal": "Lgamma", "atan2l": "Atan2", "powl": "Pow", "hypotl": "Hypot", "fmodl": "Fmod",
	"remainderl": "Remainder", "fminl": "Fmin", "fmaxl": "Fmax", "fdiml": "Fdim",
	"copysignl": "Copysign", "nextafterl": "Nextafter", "fmal": "Fma", "ilogbl": "Ilogb",
	"lroundl": "Lround", "lrintl": "Lrint", "llroundl": "Lround", "llrintl": "Lrint",
	"ldexpl": "Ldexp", "scalbnl": "Scalbn", "scalblnl": "Scalbln", "frexpl": "Frexp",
	"modfl": "Modf", "nanl": "Nan",

	// POSIX: stdio.h, stdlib.h, string.h
	"fileno": "Fileno", "fdopen": "Fdopen",
	"aligned_alloc": "AlignedAlloc", "posix_memalign": "PosixMemalign", "setenv": "Setenv",
	"unsetenv": "Unsetenv", "mkstemp": "Mkstemp", "realpath": "Realpath",
	"stpcpy": "Stpcpy", "stpncpy": "Stpncpy", "strlcpy": "Strlcpy", "strlcat": "Strlcat",

	// unistd.h
	"read": "Read", "write": "Write", "close": "Close", "lseek": "Lseek", "unlink": "Unlink",
	"rmdir": "Rmdir", "access": "Access", "getpid": "Getpid", "getppid": "Getppid",
	"getuid": "Getuid", "geteuid": "Geteuid", "getgid": "Getgid", "getegid": "Getegid",
	"isatty": "Isatty", "getcwd": "Getcwd", "chdir": "Chdir", "sleep": "Sleep",
	"usleep": "Usleep", "_exit": "ExitRaw", "dup": "Dup", "dup2": "Dup2", "pipe": "Pipe",
	"sysconf": "Sysconf", "gethostname": "Gethostname", "getopt": "Getopt",
	"__cc_optarg": "OptargLoc", "__cc_optind": "OptindLoc", "__cc_opterr": "OpterrLoc",
	"__cc_optopt": "OptoptLoc",
	"ftruncate":   "Ftruncate", "truncate": "Truncate", "fsync": "Fsync", "fdatasync": "Fdatasync",
	"chown": "Chown", "lchown": "Lchown", "fchown": "Fchown", "readlink": "Readlink",
	"symlink": "Symlink", "link": "Link", "pread": "Pread", "pwrite": "Pwrite",
	"getpagesize": "Getpagesize",

	// stdio.h, time.h, sys/time.h, utime.h
	"popen": "Popen", "pclose": "Pclose", "clock_gettime": "ClockGettime",
	"clock_getres": "ClockGetres", "nanosleep": "Nanosleep", "utimes": "Utimes", "utime": "Utime",

	// sys/ioctl.h, pwd.h, sys/resource.h, sys/mman.h
	"ioctl": "Ioctl", "getpwuid": "Getpwuid", "getpwnam": "Getpwnam", "getrusage": "Getrusage",
	"mmap": "Mmap", "munmap": "Munmap", "msync": "Msync", "mprotect": "Mprotect", "madvise": "Madvise",

	// fcntl.h, sys/stat.h, dirent.h, sys/time.h
	"open": "Open", "creat": "Creat", "fcntl": "Fcntl",
	"stat": "Stat", "lstat": "Lstat", "fstat": "Fstat", "mkdir": "Mkdir", "chmod": "Chmod",
	"fchmod": "Fchmod", "umask": "Umask",
	"opendir": "Opendir", "readdir": "Readdir", "closedir": "Closedir", "rewinddir": "Rewinddir",
	"gettimeofday": "Gettimeofday",

	// strings.h
	"strcasecmp": "Strcasecmp", "strncasecmp": "Strncasecmp", "bzero": "Bzero", "bcopy": "Bcopy",
	"ffs": "Ffs", "ffsl": "Ffsl", "ffsll": "Ffsl", "index": "Strchr", "rindex": "Strrchr",

	// math.h: C23's
	"acospi":            "Acospi",
	"acospif":           "Acospif",
	"acospil":           "Acospi",
	"asinpi":            "Asinpi",
	"asinpif":           "Asinpif",
	"asinpil":           "Asinpi",
	"atanpi":            "Atanpi",
	"atanpif":           "Atanpif",
	"atanpil":           "Atanpi",
	"atan2pi":           "Atan2pi",
	"atan2pif":          "Atan2pif",
	"atan2pil":          "Atan2pi",
	"cospi":             "Cospi",
	"cospif":            "Cospif",
	"cospil":            "Cospi",
	"sinpi":             "Sinpi",
	"sinpif":            "Sinpif",
	"sinpil":            "Sinpi",
	"tanpi":             "Tanpi",
	"tanpif":            "Tanpif",
	"tanpil":            "Tanpi",
	"exp10":             "Exp10",
	"exp10f":            "Exp10f",
	"exp10l":            "Exp10",
	"exp10m1":           "Exp10m1",
	"exp10m1f":          "Exp10m1f",
	"exp10m1l":          "Exp10m1",
	"exp2m1":            "Exp2m1",
	"exp2m1f":           "Exp2m1f",
	"exp2m1l":           "Exp2m1",
	"log10p1":           "Log10p1",
	"log10p1f":          "Log10p1f",
	"log10p1l":          "Log10p1",
	"logp1":             "Logp1",
	"logp1f":            "Logp1f",
	"logp1l":            "Logp1",
	"log2p1":            "Log2p1",
	"log2p1f":           "Log2p1f",
	"log2p1l":           "Log2p1",
	"llogb":             "Llogb",
	"llogbf":            "Llogbf",
	"llogbl":            "Llogb",
	"compoundn":         "Compoundn",
	"compoundnf":        "Compoundnf",
	"compoundnl":        "Compoundn",
	"pown":              "Pown",
	"pownf":             "Pownf",
	"pownl":             "Pown",
	"powr":              "Powr",
	"powrf":             "Powrf",
	"powrl":             "Powr",
	"rootn":             "Rootn",
	"rootnf":            "Rootnf",
	"rootnl":            "Rootn",
	"rsqrt":             "Rsqrt",
	"rsqrtf":            "Rsqrtf",
	"rsqrtl":            "Rsqrt",
	"roundeven":         "Roundeven",
	"roundevenf":        "Roundevenf",
	"roundevenl":        "Roundeven",
	"fromfp":            "Fromfp",
	"fromfpf":           "Fromfpf",
	"fromfpl":           "Fromfp",
	"ufromfp":           "Ufromfp",
	"ufromfpf":          "Ufromfpf",
	"ufromfpl":          "Ufromfp",
	"fromfpx":           "Fromfpx",
	"fromfpxf":          "Fromfpf",
	"fromfpxl":          "Fromfpx",
	"ufromfpx":          "Ufromfpx",
	"ufromfpxf":         "Ufromfpf",
	"ufromfpxl":         "Ufromfpx",
	"remquo":            "Remquo",
	"remquof":           "Remquof",
	"remquol":           "Remquo",
	"nexttoward":        "Nexttoward",
	"nexttowardf":       "Nexttowardf",
	"nexttowardl":       "Nexttoward",
	"nextup":            "Nextup",
	"nextupf":           "Nextupf",
	"nextupl":           "Nextup",
	"nextdown":          "Nextdown",
	"nextdownf":         "Nextdownf",
	"nextdownl":         "Nextdown",
	"canonicalize":      "Canonicalize",
	"canonicalizef":     "Canonicalizef",
	"canonicalizel":     "Canonicalize",
	"fmaximum":          "Fmaximum",
	"fmaximumf":         "Fmaximumf",
	"fmaximuml":         "Fmaximum",
	"fminimum":          "Fminimum",
	"fminimumf":         "Fminimumf",
	"fminimuml":         "Fminimum",
	"fmaximum_mag":      "FmaximumMag",
	"fmaximum_magf":     "FmaximumMagf",
	"fmaximum_magl":     "FmaximumMag",
	"fminimum_mag":      "FminimumMag",
	"fminimum_magf":     "FminimumMagf",
	"fminimum_magl":     "FminimumMag",
	"fmaximum_num":      "FmaximumNum",
	"fmaximum_numf":     "FmaximumNumf",
	"fmaximum_numl":     "FmaximumNum",
	"fminimum_num":      "FminimumNum",
	"fminimum_numf":     "FminimumNumf",
	"fminimum_numl":     "FminimumNum",
	"fmaximum_mag_num":  "FmaximumMagNum",
	"fmaximum_mag_numf": "FmaximumMagNumf",
	"fmaximum_mag_numl": "FmaximumMagNum",
	"fminimum_mag_num":  "FminimumMagNum",
	"fminimum_mag_numf": "FminimumMagNumf",
	"fminimum_mag_numl": "FminimumMagNum",
	"getpayload":        "Getpayload",
	"getpayloadf":       "Getpayloadf",
	"getpayloadl":       "Getpayload",
	"setpayload":        "Setpayload",
	"setpayloadf":       "Setpayloadf",
	"setpayloadl":       "Setpayload",
	"setpayloadsig":     "Setpayloadsig",
	"setpayloadsigf":    "Setpayloadsigf",
	"setpayloadsigl":    "Setpayloadsig",
	"totalorder":        "Totalorder",
	"totalorderf":       "Totalorderf",
	"totalorderl":       "Totalorder",
	"totalordermag":     "Totalordermag",
	"totalordermagf":    "Totalordermagf",
	"totalordermagl":    "Totalordermag",
	"fadd":              "Fadd",
	"faddl":             "Fadd",
	"daddl":             "Daddl",
	"fsub":              "Fsub",
	"fsubl":             "Fsub",
	"dsubl":             "Dsubl",
	"fmul":              "Fmul",
	"fmull":             "Fmul",
	"dmull":             "Dmull",
	"fdiv":              "Fdiv",
	"fdivl":             "Fdiv",
	"ddivl":             "Ddivl",
	"ffma":              "Ffma",
	"ffmal":             "Ffma",
	"dfmal":             "Dfmal",
	"fsqrt":             "Fsqrt",
	"fsqrtl":            "Fsqrt",
	"dsqrtl":            "Dsqrtl",
	"__cc_issignaling":  "Issignaling",
	"__cc_issignalingf": "Issignalingf",
}

// stdbitFuncs are C23's stdbit.h functions, stdc_NAME_uc and so on,
// whose Go is StdcNAMEUc (unsigned long long's StdcNAMEUl, as unsigned
// long's where long has 64 bits).
var stdbitFuncs = []string{"leading_zeros", "leading_ones", "trailing_zeros", "trailing_ones", "first_leading_zero", "first_leading_one", "first_trailing_zero", "first_trailing_one", "count_zeros", "count_ones", "has_single_bit", "bit_width", "bit_floor", "bit_ceil"}

func init() {
	for _, f := range stdbitFuncs {
		g := "Stdc"
		for _, w := range strings.Split(f, "_") {
			g += strings.ToUpper(w[:1]) + w[1:]
		}
		for c, s := range map[string]string{"uc": "Uc", "us": "Us", "ui": "Ui", "ul": "Ul", "ull": "Ul"} {
			libc["stdc_"+f+"_"+c] = g + s
		}
		if target.ilp32() {
			libc["stdc_"+f+"_ul"] = g + "Ul32"
		}
	}
}

var (
	libcTypesOnce sync.Once
	libcTypesMap  map[string]*ctype
)

// libcType is the type of a library function, as its header declares it.
func libcType(name string) *ctype {
	libcTypesOnce.Do(func() {
		libcTypesMap = map[string]*ctype{}
		var src strings.Builder
		for _, h := range Headers() {
			fmt.Fprintf(&src, "#include <%s>\n", h)
		}
		toks, err := lexUnit([]source{{"<headers>", src.String()}})
		if err != nil {
			panic(err)
		}
		u, err := parse("<headers>", toks)
		if err != nil {
			panic(err)
		}
		for _, s := range u.syms {
			if s.kind == symFunc {
				libcTypesMap[s.name] = s.t
			}
		}
	})
	return libcTypesMap[name]
}
