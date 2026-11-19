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
	"printf": "stdio.Printf", "fprintf": "stdio.Fprintf", "sprintf": "stdio.Sprintf", "snprintf": "stdio.Snprintf",
	"vprintf": "stdio.Vprintf", "vfprintf": "stdio.Vfprintf", "vsprintf": "stdio.Vsprintf", "vsnprintf": "stdio.Vsnprintf",
	"puts": "stdio.Puts", "fputs": "stdio.Fputs", "putchar": "stdio.Putchar", "fputc": "stdio.Fputc", "putc": "stdio.Fputc",
	"getchar": "stdio.Getchar", "fgetc": "stdio.Fgetc", "getc": "stdio.Fgetc", "fgets": "stdio.Fgets", "fwrite": "stdio.Fwrite",
	"fread": "stdio.Fread", "fflush": "Fflush",
	"malloc": "Malloc", "calloc": "Calloc", "realloc": "Realloc", "free": "Free",
	"abort": "Abort", "exit": "Exit", "_Exit": "stdlib.ExitNow", "abs": "stdlib.Abs", "labs": "stdlib.Labs", "llabs": "stdlib.Labs",
	"atoi": "stdlib.Atoi", "atol": "stdlib.Atol", "atoll": "stdlib.Atol", "strtol": "stdlib.Strtol", "strtoul": "stdlib.Strtoul",
	"strtoll": "stdlib.Strtol", "strtoull": "stdlib.Strtoul", "strtod": "stdlib.Strtod", "atof": "stdlib.Atof",
	"rand": "stdlib.Rand", "srand": "stdlib.Srand", "getenv": "stdlib.Getenv",
	"memcpy": "string.Memcpy", "memmove": "string.Memcpy", "memset": "Memset", "memcmp": "Memcmp", "memchr": "string.Memchr",
	"memccpy": "string.Memccpy",
	"strcpy":  "string.Strcpy", "strncpy": "string.Strncpy", "strcat": "string.Strcat", "strncat": "string.Strncat",
	"strcmp": "string.Strcmp", "strncmp": "string.Strncmp", "strlen": "string.Strlen", "strchr": "string.Strchr",
	"strrchr": "string.Strrchr", "strstr": "string.Strstr", "strdup": "string.Strdup", "strndup": "string.Strndup",
	"strspn": "string.Strspn", "strcspn": "string.Strcspn", "__cc_stdio": "Stdio",
	// complex.h
	"creal":   "complex.Creal",
	"cimag":   "complex.Cimag",
	"conj":    "complex.Conj",
	"cabs":    "complex.Cabs",
	"carg":    "complex.Carg",
	"cproj":   "complex.Cproj",
	"cexp":    "complex.Cexp",
	"clog":    "complex.Clog",
	"csqrt":   "complex.Csqrt",
	"cpow":    "complex.Cpow",
	"csin":    "complex.Csin",
	"ccos":    "complex.Ccos",
	"ctan":    "complex.Ctan",
	"casin":   "complex.Casin",
	"cacos":   "complex.Cacos",
	"catan":   "complex.Catan",
	"csinh":   "complex.Csinh",
	"ccosh":   "complex.Ccosh",
	"ctanh":   "complex.Ctanh",
	"casinh":  "complex.Casinh",
	"cacosh":  "complex.Cacosh",
	"catanh":  "complex.Catanh",
	"crealf":  "complex.Crealf",
	"cimagf":  "complex.Cimagf",
	"conjf":   "complex.Conjf",
	"cabsf":   "complex.Cabsf",
	"cargf":   "complex.Cargf",
	"cexpf":   "complex.Cexpf",
	"clogf":   "complex.Clogf",
	"csqrtf":  "complex.Csqrtf",
	"cpowf":   "complex.Cpowf",
	"csinf":   "complex.Csinf",
	"ccosf":   "complex.Ccosf",
	"ctanf":   "complex.Ctanf",
	"casinf":  "complex.Casinf",
	"cacosf":  "complex.Cacosf",
	"catanf":  "complex.Catanf",
	"csinhf":  "complex.Csinhf",
	"ccoshf":  "complex.Ccoshf",
	"ctanhf":  "complex.Ctanhf",
	"casinhf": "complex.Casinhf",
	"cacoshf": "complex.Cacoshf",
	"catanhf": "complex.Catanhf",
	"cprojf":  "complex.Cprojf",
	"dlopen":  "Dlopen", "dlsym": "Dlsym", "dlclose": "Dlclose", "dlerror": "Dlerror",
	"alloca": "Malloc",

	// pthread.h
	"pthread_create": "pthread.PthreadCreate", "pthread_join": "pthread.PthreadJoin", "pthread_detach": "pthread.PthreadDetach",
	"pthread_exit": "pthread.PthreadExit", "pthread_self": "threads.PthreadSelf", "pthread_equal": "threads.PthreadEqual",
	"pthread_mutex_init": "pthread.PthreadMutexInit", "pthread_mutex_destroy": "pthread.PthreadMutexDestroy",
	"pthread_mutex_lock": "pthread.PthreadMutexLock", "pthread_mutex_trylock": "pthread.PthreadMutexTrylock",
	"pthread_mutex_unlock": "pthread.PthreadMutexUnlock", "pthread_cond_init": "pthread.PthreadCondInit",
	"pthread_cond_destroy": "pthread.PthreadCondDestroy", "pthread_cond_wait": "pthread.PthreadCondWait",
	"pthread_cond_signal": "pthread.PthreadCondSignal", "pthread_cond_broadcast": "pthread.PthreadCondBroadcast",
	"pthread_mutexattr_init": "pthread.PthreadMutexattrInit", "pthread_mutexattr_destroy": "pthread.PthreadMutexattrDestroy",
	"pthread_mutexattr_settype": "pthread.PthreadMutexattrSettype", "pthread_mutexattr_gettype": "pthread.PthreadMutexattrGettype",
	// stdatomic.h's functions, for their addresses: calls are macros.
	"atomic_thread_fence": "stdatomic.AtomicThreadFence", "atomic_signal_fence": "stdatomic.AtomicThreadFence",
	"atomic_flag_test_and_set": "stdatomic.AtomicFlagTestAndSet", "atomic_flag_test_and_set_explicit": "stdatomic.AtomicFlagTestAndSetExplicit",
	"atomic_flag_clear": "stdatomic.AtomicFlagClear", "atomic_flag_clear_explicit": "stdatomic.AtomicFlagClearExplicit",
	"pthread_cond_timedwait": "pthread.PthreadCondTimedwait", "pthread_mutex_timedlock": "pthread.PthreadMutexTimedlock",
	// threads.h
	"thrd_create": "threads.ThrdCreate", "thrd_current": "threads.PthreadSelf", "thrd_detach": "threads.ThrdDetach",
	"thrd_equal": "threads.PthreadEqual", "thrd_exit": "threads.ThrdExit", "thrd_join": "threads.ThrdJoin",
	"thrd_sleep": "threads.ThrdSleep", "thrd_yield": "threads.ThrdYield",
	"mtx_init": "threads.MtxInit", "mtx_destroy": "threads.MtxDestroy", "mtx_lock": "threads.MtxLock",
	"mtx_timedlock": "threads.MtxTimedlock", "mtx_trylock": "threads.MtxTrylock", "mtx_unlock": "threads.MtxUnlock",
	"cnd_init": "threads.CndInit", "cnd_destroy": "threads.CndDestroy", "cnd_signal": "threads.CndSignal",
	"cnd_broadcast": "threads.CndBroadcast", "cnd_wait": "threads.CndWait", "cnd_timedwait": "threads.CndTimedwait",
	"tss_create": "threads.TssCreate", "tss_delete": "threads.TssDelete", "tss_get": "threads.TssGet", "tss_set": "threads.TssSet",
	"pthread_once": "pthread.PthreadOnce", "pthread_key_create": "pthread.PthreadKeyCreate", "pthread_key_delete": "pthread.PthreadKeyDelete",
	"pthread_getspecific": "pthread.PthreadGetspecific", "pthread_setspecific": "pthread.PthreadSetspecific",
	"sched_yield": "pthread.SchedYield",
	"longjmp":     "Longjmp", "_longjmp": "Longjmp", "siglongjmp": "Longjmp",
	"strnlen": "string.Strnlen", "strpbrk": "string.Strpbrk", "strcoll": "string.Strcoll", "strtok": "string.Strtok",
	"strtok_r": "string.StrtokR", "strerror": "Strerror", "memset_explicit": "string.MemsetExplicit",

	// stdio.h
	"fopen": "stdio.Fopen", "fclose": "stdio.Fclose", "tmpfile": "stdio.Tmpfile", "fseek": "stdio.Fseek", "ftell": "stdio.Ftell",
	"rewind": "stdio.Rewind", "feof": "stdio.Feof", "ferror": "stdio.Ferror", "clearerr": "stdio.Clearerr",
	"ungetc": "stdio.Ungetc", "setvbuf": "stdio.Setvbuf", "setbuf": "stdio.Setbuf", "remove": "stdio.Remove",
	"rename": "stdio.Rename", "perror": "stdio.Perror", "freopen": "stdio.Freopen", "tmpnam": "stdio.Tmpnam",
	"scanf": "stdio.Scanf", "fscanf": "stdio.Fscanf", "sscanf": "stdio.Sscanf", "vscanf": "stdio.Vscanf",
	"vfscanf": "stdio.Vfscanf", "vsscanf": "stdio.Vsscanf",

	// stdlib.h
	"strtof": "stdlib.Strtof", "strtold": "stdlib.Strtold", "atexit": "stdlib.Atexit", "qsort": "stdlib.Qsort",
	"bsearch": "stdlib.Bsearch", "system": "stdlib.System", "div": "stdlib.Div", "ldiv": "stdlib.Ldiv", "lldiv": "stdlib.Ldiv",

	// locale.h, signal.h
	"setlocale": "locale.Setlocale", "localeconv": "locale.Localeconv", "signal": "signal.Signal", "raise": "signal.Raise",

	// errno.h
	"__cc_errno": "errno.ErrnoLoc",

	// ctype.h
	"isalnum": "ctype.Isalnum", "isalpha": "ctype.Isalpha", "isblank": "ctype.Isblank", "iscntrl": "ctype.Iscntrl",
	"isdigit": "ctype.Isdigit", "isgraph": "ctype.Isgraph", "islower": "ctype.Islower", "isprint": "ctype.Isprint",
	"ispunct": "ctype.Ispunct", "isspace": "ctype.Isspace", "isupper": "ctype.Isupper", "isxdigit": "ctype.Isxdigit",
	"tolower": "ctype.Tolower", "toupper": "ctype.Toupper",

	// inttypes.h, time.h
	"imaxabs": "stdlib.Labs", "strtoimax": "stdlib.Strtol", "strtoumax": "stdlib.Strtoul",
	"imaxdiv": "stdlib.Ldiv",
	"time":    "time.Time", "clock": "time.Clock", "difftime": "time.Difftime", "mktime": "time.Mktime",
	"timegm": "time.Timegm", "gmtime": "time.Gmtime", "gmtime_r": "time.GmtimeR", "localtime": "time.Localtime",
	"localtime_r": "time.LocaltimeR", "asctime": "time.Asctime", "ctime": "time.Ctime", "strftime": "time.Strftime",
	"timespec_get": "time.TimespecGet", "timespec_getres": "time.TimespecGetres",

	// uchar.h, wchar.h
	"mbrtoc8": "uchar.Mbrtoc8", "c8rtomb": "uchar.C8rtomb", "mbrtoc16": "uchar.Mbrtoc16", "c16rtomb": "uchar.C16rtomb",
	"mbrtoc32": "uchar.Mbrtoc32", "c32rtomb": "uchar.C32rtomb", "mbrtowc": "wchar.Mbrtowc", "wcrtomb": "wchar.Wcrtomb",
	"mbrlen": "wchar.Mbrlen", "mbsinit": "wchar.Mbsinit", "btowc": "wchar.Btowc", "wctob": "wchar.Wctob",
	"wcslen": "wchar.Wcslen", "wcscmp": "wchar.Wcscmp", "wcsncmp": "wchar.Wcsncmp", "wcscpy": "wchar.Wcscpy",
	"wcscat": "wchar.Wcscat", "wcschr": "wchar.Wcschr",

	// math.h
	"__cc_fpclassify": "math.Fpclassify", "__cc_fpclassifyf": "math.Fpclassifyf", "__cc_signbit": "math.Signbit",
	"sin": "math.Sin", "cos": "math.Cos", "tan": "math.Tan", "asin": "math.Asin", "acos": "math.Acos", "atan": "math.Atan",
	"sinh": "math.Sinh", "cosh": "math.Cosh", "tanh": "math.Tanh", "asinh": "math.Asinh", "acosh": "math.Acosh",
	"atanh": "math.Atanh", "exp": "math.Exp", "exp2": "math.Exp2", "expm1": "math.Expm1", "log": "math.Log",
	"log2": "math.Log2", "log10": "math.Log10", "log1p": "math.Log1p", "logb": "math.Logb", "sqrt": "math.Sqrt",
	"cbrt": "math.Cbrt", "fabs": "math.Fabs", "floor": "math.Floor", "ceil": "math.Ceil", "round": "math.Round",
	"trunc": "math.Trunc", "rint": "Rint", "nearbyint": "math.Nearbyint", "erf": "math.Erf", "erfc": "math.Erfc",
	"tgamma": "math.Tgamma", "lgamma": "math.Lgamma", "atan2": "math.Atan2", "pow": "math.Pow", "hypot": "math.Hypot",
	"fmod": "math.Fmod", "remainder": "math.Remainder", "fmin": "math.Fmin", "fmax": "math.Fmax", "fdim": "math.Fdim",
	"copysign": "math.Copysign", "nextafter": "math.Nextafter", "fma": "math.Fma", "ilogb": "math.Ilogb",
	"lround": "math.Lround", "lrint": "math.Lrint", "llround": "math.Lround", "llrint": "math.Lrint",
	"ldexp": "math.Ldexp", "scalbn": "math.Scalbn", "scalbln": "math.Scalbln", "frexp": "math.Frexp",
	"modf": "math.Modf", "nan": "math.Nan", "sinf": "math.Sinf", "cosf": "math.Cosf", "tanf": "math.Tanf",
	"asinf": "math.Asinf", "acosf": "math.Acosf", "atanf": "math.Atanf", "sinhf": "math.Sinhf", "coshf": "math.Coshf",
	"tanhf": "math.Tanhf", "asinhf": "math.Asinhf", "acoshf": "math.Acoshf", "atanhf": "math.Atanhf",
	"expf": "math.Expf", "exp2f": "math.Exp2f", "expm1f": "math.Expm1f", "logf": "math.Logf", "log2f": "math.Log2f",
	"log10f": "math.Log10f", "log1pf": "math.Log1pf", "logbf": "math.Logbf", "sqrtf": "math.Sqrtf",
	"cbrtf": "math.Cbrtf", "fabsf": "math.Fabsf", "floorf": "math.Floorf", "ceilf": "math.Ceilf",
	"roundf": "math.Roundf", "truncf": "math.Truncf", "rintf": "math.Rintf", "nearbyintf": "math.Nearbyintf",
	"erff": "math.Erff", "erfcf": "math.Erfcf", "tgammaf": "math.Tgammaf", "lgammaf": "math.Lgammaf",
	"atan2f": "math.Atan2f", "powf": "math.Powf", "hypotf": "math.Hypotf", "fmodf": "math.Fmodf",
	"remainderf": "math.Remainderf", "fminf": "math.Fminf", "fmaxf": "math.Fmaxf", "fdimf": "math.Fdimf",
	"copysignf": "math.Copysignf", "nextafterf": "math.Nextafterf", "fmaf": "math.Fmaf", "ilogbf": "math.Ilogbf",
	"lroundf": "math.Lroundf", "lrintf": "math.Lrintf", "llroundf": "math.Lroundf", "llrintf": "math.Lrintf",
	"ldexpf": "math.Ldexpf", "scalbnf": "math.Scalbnf", "scalblnf": "math.Scalblnf", "frexpf": "math.Frexpf",
	"modff": "math.Modff", "nanf": "math.Nanf", "sinl": "math.Sin", "cosl": "math.Cos", "tanl": "math.Tan",
	"asinl": "math.Asin", "acosl": "math.Acos", "atanl": "math.Atan", "sinhl": "math.Sinh", "coshl": "math.Cosh",
	"tanhl": "math.Tanh", "asinhl": "math.Asinh", "acoshl": "math.Acosh", "atanhl": "math.Atanh", "expl": "math.Exp",
	"exp2l": "math.Exp2", "expm1l": "math.Expm1", "logl": "math.Log", "log2l": "math.Log2", "log10l": "math.Log10",
	"log1pl": "math.Log1p", "logbl": "math.Logb", "sqrtl": "math.Sqrt", "cbrtl": "math.Cbrt", "fabsl": "math.Fabs",
	"floorl": "math.Floor", "ceill": "math.Ceil", "roundl": "math.Round", "truncl": "math.Trunc", "rintl": "Rint",
	"nearbyintl": "math.Nearbyint", "erfl": "math.Erf", "erfcl": "math.Erfc", "tgammal": "math.Tgamma",
	"lgammal": "math.Lgamma", "atan2l": "math.Atan2", "powl": "math.Pow", "hypotl": "math.Hypot", "fmodl": "math.Fmod",
	"remainderl": "math.Remainder", "fminl": "math.Fmin", "fmaxl": "math.Fmax", "fdiml": "math.Fdim",
	"copysignl": "math.Copysign", "nextafterl": "math.Nextafter", "fmal": "math.Fma", "ilogbl": "math.Ilogb",
	"lroundl": "math.Lround", "lrintl": "math.Lrint", "llroundl": "math.Lround", "llrintl": "math.Lrint",
	"ldexpl": "math.Ldexp", "scalbnl": "math.Scalbn", "scalblnl": "math.Scalbln", "frexpl": "math.Frexp",
	"modfl": "math.Modf", "nanl": "math.Nan",

	// POSIX: stdio.h, stdlib.h, string.h
	"fileno": "stdio.Fileno", "fdopen": "stdio.Fdopen",
	"aligned_alloc": "AlignedAlloc", "posix_memalign": "stdlib.PosixMemalign", "setenv": "stdlib.Setenv",
	"unsetenv": "stdlib.Unsetenv", "mkstemp": "stdlib.Mkstemp", "realpath": "stdlib.Realpath",
	"stpcpy": "string.Stpcpy", "stpncpy": "string.Stpncpy", "strlcpy": "string.Strlcpy", "strlcat": "string.Strlcat",

	// unistd.h
	"read": "unistd.Read", "write": "unistd.Write", "close": "unistd.Close", "lseek": "unistd.Lseek", "unlink": "unistd.Unlink",
	"rmdir": "unistd.Rmdir", "access": "unistd.Access", "getpid": "unistd.Getpid", "getppid": "unistd.Getppid",
	"getuid": "unistd.Getuid", "geteuid": "unistd.Geteuid", "getgid": "unistd.Getgid", "getegid": "unistd.Getegid",
	"isatty": "unistd.Isatty", "getcwd": "unistd.Getcwd", "chdir": "unistd.Chdir", "sleep": "unistd.Sleep",
	"usleep": "unistd.Usleep", "_exit": "unistd.ExitRaw", "dup": "unistd.Dup", "dup2": "unistd.Dup2", "pipe": "unistd.Pipe",
	"sysconf": "unistd.Sysconf", "gethostname": "unistd.Gethostname", "getopt": "unistd.Getopt",
	"__cc_optarg": "unistd.OptargLoc", "__cc_optind": "unistd.OptindLoc", "__cc_opterr": "unistd.OpterrLoc",
	"__cc_optopt": "unistd.OptoptLoc",
	"ftruncate":   "unistd.Ftruncate", "truncate": "unistd.Truncate", "fsync": "unistd.Fsync", "fdatasync": "unistd.Fdatasync",
	"chown": "unistd.Chown", "lchown": "unistd.Lchown", "fchown": "unistd.Fchown", "readlink": "unistd.Readlink",
	"symlink": "unistd.Symlink", "link": "unistd.Link", "pread": "unistd.Pread", "pwrite": "unistd.Pwrite",
	"getpagesize": "unistd.Getpagesize",

	// stdio.h, time.h, sys/time.h, utime.h
	"popen": "stdio.Popen", "pclose": "stdio.Pclose", "clock_gettime": "time.ClockGettime",
	"clock_getres": "time.ClockGetres", "nanosleep": "time.Nanosleep", "utimes": "sys/time.Utimes", "utime": "utime.Utime",

	// sys/ioctl.h, pwd.h, sys/resource.h, sys/mman.h
	"ioctl": "sys/ioctl.Ioctl", "getpwuid": "pwd.Getpwuid", "getpwnam": "pwd.Getpwnam", "getrusage": "sys/resource.Getrusage",
	"mmap": "sys/mman.Mmap", "munmap": "sys/mman.Munmap", "msync": "sys/mman.Msync", "mprotect": "sys/mman.Mprotect", "madvise": "sys/mman.Madvise",

	// fcntl.h, sys/stat.h, dirent.h, sys/time.h
	"open": "fcntl.Open", "creat": "fcntl.Creat", "fcntl": "fcntl.Fcntl",
	"stat": "sys/stat.Stat", "lstat": "sys/stat.Lstat", "fstat": "sys/stat.Fstat", "mkdir": "sys/stat.Mkdir", "chmod": "sys/stat.Chmod",
	"fchmod": "sys/stat.Fchmod", "umask": "sys/stat.Umask",
	"opendir": "dirent.Opendir", "readdir": "dirent.Readdir", "closedir": "dirent.Closedir", "rewinddir": "dirent.Rewinddir",
	"gettimeofday": "sys/time.Gettimeofday",

	// strings.h
	"strcasecmp": "strings.Strcasecmp", "strncasecmp": "strings.Strncasecmp", "bzero": "strings.Bzero", "bcopy": "strings.Bcopy",
	"ffs": "strings.Ffs", "ffsl": "strings.Ffsl", "ffsll": "strings.Ffsl", "index": "string.Strchr", "rindex": "string.Strrchr",

	// wchar.h, stdlib.h's multibyte functions and C23's, stdio.h's fpos_t
	"fgetwc": "wchar.Fgetwc", "getwc": "wchar.Fgetwc", "getwchar": "wchar.Getwchar", "fgetws": "wchar.Fgetws",
	"fputwc": "wchar.Fputwc", "putwc": "wchar.Fputwc", "putwchar": "wchar.Putwchar", "fputws": "wchar.Fputws",
	"fwide": "wchar.Fwide", "ungetwc": "wchar.Ungetwc",
	"fwprintf": "wchar.Fwprintf", "wprintf": "wchar.Wprintf", "swprintf": "wchar.Swprintf", "vfwprintf": "wchar.Vfwprintf",
	"vwprintf": "wchar.Vwprintf", "vswprintf": "wchar.Vswprintf", "fwscanf": "wchar.Fwscanf", "wscanf": "wchar.Wscanf",
	"swscanf": "wchar.Swscanf", "vfwscanf": "wchar.Vfwscanf", "vwscanf": "wchar.Vwscanf", "vswscanf": "wchar.Vswscanf",
	"wcstod": "wchar.Wcstod", "wcstof": "wchar.Wcstof", "wcstold": "wchar.Wcstod", "wcstol": "wchar.Wcstol", "wcstoll": "wchar.Wcstol",
	"wcstoul": "wchar.Wcstoul", "wcstoull": "wchar.Wcstoul", "wcstoimax": "wchar.Wcstol", "wcstoumax": "wchar.Wcstoul",
	"wcsncpy": "wchar.Wcsncpy", "wcsncat": "wchar.Wcsncat", "wcscoll": "wchar.Wcscoll", "wcsxfrm": "wchar.Wcsxfrm",
	"wcscspn": "wchar.Wcscspn", "wcsspn": "wchar.Wcsspn", "wcspbrk": "wchar.Wcspbrk", "wcsrchr": "wchar.Wcsrchr",
	"wcsstr": "wchar.Wcsstr", "wcstok": "wchar.Wcstok", "wmemchr": "wchar.Wmemchr", "wmemcmp": "wchar.Wmemcmp",
	"wmemcpy": "wchar.Wmemcpy", "wmemmove": "wchar.Wmemmove", "wmemset": "wchar.Wmemset", "wcsftime": "wchar.Wcsftime",
	"mbsrtowcs": "wchar.Mbsrtowcs", "wcsrtombs": "wchar.Wcsrtombs", "mblen": "stdlib.Mblen", "mbtowc": "stdlib.Mbtowc",
	"wctomb": "stdlib.Wctomb", "mbstowcs": "stdlib.Mbstowcs", "wcstombs": "stdlib.Wcstombs",
	"strfromd": "stdlib.Strfromd", "strfromf": "stdlib.Strfromf", "strfroml": "stdlib.Strfromd",
	"free_sized": "stdlib.FreeSized", "free_aligned_sized": "stdlib.FreeAlignedSized",
	"memalignment": "stdlib.Memalignment", "call_once": "stdlib.CallOnce", "quick_exit": "stdlib.QuickExit",
	"at_quick_exit": "stdlib.AtQuickExit", "strxfrm": "string.Strxfrm", "fgetpos": "stdio.Fgetpos", "fsetpos": "stdio.Fsetpos",

	// complex.h's long double functions: double's
	"creall": "complex.Creal", "cimagl": "complex.Cimag", "conjl": "complex.Conj", "cabsl": "complex.Cabs", "cargl": "complex.Carg", "cprojl": "complex.Cproj", "cexpl": "complex.Cexp", "clogl": "complex.Clog", "csqrtl": "complex.Csqrt", "cpowl": "complex.Cpow", "csinl": "complex.Csin", "ccosl": "complex.Ccos", "ctanl": "complex.Ctan", "casinl": "complex.Casin", "cacosl": "complex.Cacos", "catanl": "complex.Catan", "csinhl": "complex.Csinh", "ccoshl": "complex.Ccosh", "ctanhl": "complex.Ctanh", "casinhl": "complex.Casinh", "cacoshl": "complex.Cacosh", "catanhl": "complex.Catanh",

	// wctype.h, fenv.h
	"iswalnum": "wctype.Iswalnum", "iswalpha": "wctype.Iswalpha", "iswblank": "wctype.Iswblank", "iswcntrl": "wctype.Iswcntrl", "iswdigit": "wctype.Iswdigit", "iswgraph": "wctype.Iswgraph", "iswlower": "wctype.Iswlower", "iswprint": "wctype.Iswprint", "iswpunct": "wctype.Iswpunct", "iswspace": "wctype.Iswspace", "iswupper": "wctype.Iswupper", "iswxdigit": "wctype.Iswxdigit",
	"iswctype": "wctype.Iswctype", "wctype": "wctype.Wctype", "towlower": "wctype.Towlower", "towupper": "wctype.Towupper",
	"towctrans": "wctype.Towctrans", "wctrans": "wctype.Wctrans",
	"feclearexcept": "Feclearexcept", "fegetexceptflag": "Fegetexceptflag", "feraiseexcept": "Feraiseexcept",
	"fesetexcept": "Fesetexcept", "fesetexceptflag": "Fesetexceptflag", "fetestexceptflag": "Fetestexceptflag",
	"fetestexcept": "Fetestexcept", "fegetmode": "Fegetmode", "fegetround": "Fegetround",
	"fesetmode": "Fesetmode", "fesetround": "Fesetround", "fegetenv": "Fegetenv",
	"feholdexcept": "Feholdexcept", "fesetenv": "Fesetenv", "feupdateenv": "Feupdateenv",
	"__cc_fe_dfl": "FeDfl", "__cc_flt_rounds": "FltRounds",

	// math.h: C23's
	"acospi":            "math.Acospi",
	"acospif":           "math.Acospif",
	"acospil":           "math.Acospi",
	"asinpi":            "math.Asinpi",
	"asinpif":           "math.Asinpif",
	"asinpil":           "math.Asinpi",
	"atanpi":            "math.Atanpi",
	"atanpif":           "math.Atanpif",
	"atanpil":           "math.Atanpi",
	"atan2pi":           "math.Atan2pi",
	"atan2pif":          "math.Atan2pif",
	"atan2pil":          "math.Atan2pi",
	"cospi":             "math.Cospi",
	"cospif":            "math.Cospif",
	"cospil":            "math.Cospi",
	"sinpi":             "math.Sinpi",
	"sinpif":            "math.Sinpif",
	"sinpil":            "math.Sinpi",
	"tanpi":             "math.Tanpi",
	"tanpif":            "math.Tanpif",
	"tanpil":            "math.Tanpi",
	"exp10":             "math.Exp10",
	"exp10f":            "math.Exp10f",
	"exp10l":            "math.Exp10",
	"exp10m1":           "math.Exp10m1",
	"exp10m1f":          "math.Exp10m1f",
	"exp10m1l":          "math.Exp10m1",
	"exp2m1":            "math.Exp2m1",
	"exp2m1f":           "math.Exp2m1f",
	"exp2m1l":           "math.Exp2m1",
	"log10p1":           "math.Log10p1",
	"log10p1f":          "math.Log10p1f",
	"log10p1l":          "math.Log10p1",
	"logp1":             "math.Logp1",
	"logp1f":            "math.Logp1f",
	"logp1l":            "math.Logp1",
	"log2p1":            "math.Log2p1",
	"log2p1f":           "math.Log2p1f",
	"log2p1l":           "math.Log2p1",
	"llogb":             "Llogb",
	"llogbf":            "math.Llogbf",
	"llogbl":            "Llogb",
	"compoundn":         "math.Compoundn",
	"compoundnf":        "math.Compoundnf",
	"compoundnl":        "math.Compoundn",
	"pown":              "math.Pown",
	"pownf":             "math.Pownf",
	"pownl":             "math.Pown",
	"powr":              "math.Powr",
	"powrf":             "math.Powrf",
	"powrl":             "math.Powr",
	"rootn":             "math.Rootn",
	"rootnf":            "math.Rootnf",
	"rootnl":            "math.Rootn",
	"rsqrt":             "math.Rsqrt",
	"rsqrtf":            "math.Rsqrtf",
	"rsqrtl":            "math.Rsqrt",
	"roundeven":         "math.Roundeven",
	"roundevenf":        "math.Roundevenf",
	"roundevenl":        "math.Roundeven",
	"fromfp":            "math.Fromfp",
	"fromfpf":           "math.Fromfpf",
	"fromfpl":           "math.Fromfp",
	"ufromfp":           "math.Ufromfp",
	"ufromfpf":          "math.Ufromfpf",
	"ufromfpl":          "math.Ufromfp",
	"fromfpx":           "math.Fromfpx",
	"fromfpxf":          "math.Fromfpf",
	"fromfpxl":          "math.Fromfpx",
	"ufromfpx":          "math.Ufromfpx",
	"ufromfpxf":         "math.Ufromfpf",
	"ufromfpxl":         "math.Ufromfpx",
	"remquo":            "math.Remquo",
	"remquof":           "math.Remquof",
	"remquol":           "math.Remquo",
	"nexttoward":        "math.Nexttoward",
	"nexttowardf":       "math.Nexttowardf",
	"nexttowardl":       "math.Nexttoward",
	"nextup":            "math.Nextup",
	"nextupf":           "math.Nextupf",
	"nextupl":           "math.Nextup",
	"nextdown":          "math.Nextdown",
	"nextdownf":         "math.Nextdownf",
	"nextdownl":         "math.Nextdown",
	"canonicalize":      "math.Canonicalize",
	"canonicalizef":     "math.Canonicalizef",
	"canonicalizel":     "math.Canonicalize",
	"fmaximum":          "math.Fmaximum",
	"fmaximumf":         "math.Fmaximumf",
	"fmaximuml":         "math.Fmaximum",
	"fminimum":          "math.Fminimum",
	"fminimumf":         "math.Fminimumf",
	"fminimuml":         "math.Fminimum",
	"fmaximum_mag":      "math.FmaximumMag",
	"fmaximum_magf":     "math.FmaximumMagf",
	"fmaximum_magl":     "math.FmaximumMag",
	"fminimum_mag":      "math.FminimumMag",
	"fminimum_magf":     "math.FminimumMagf",
	"fminimum_magl":     "math.FminimumMag",
	"fmaximum_num":      "math.FmaximumNum",
	"fmaximum_numf":     "math.FmaximumNumf",
	"fmaximum_numl":     "math.FmaximumNum",
	"fminimum_num":      "math.FminimumNum",
	"fminimum_numf":     "math.FminimumNumf",
	"fminimum_numl":     "math.FminimumNum",
	"fmaximum_mag_num":  "math.FmaximumMagNum",
	"fmaximum_mag_numf": "math.FmaximumMagNumf",
	"fmaximum_mag_numl": "math.FmaximumMagNum",
	"fminimum_mag_num":  "math.FminimumMagNum",
	"fminimum_mag_numf": "math.FminimumMagNumf",
	"fminimum_mag_numl": "math.FminimumMagNum",
	"getpayload":        "math.Getpayload",
	"getpayloadf":       "math.Getpayloadf",
	"getpayloadl":       "math.Getpayload",
	"setpayload":        "math.Setpayload",
	"setpayloadf":       "math.Setpayloadf",
	"setpayloadl":       "math.Setpayload",
	"setpayloadsig":     "math.Setpayloadsig",
	"setpayloadsigf":    "math.Setpayloadsigf",
	"setpayloadsigl":    "math.Setpayloadsig",
	"totalorder":        "math.Totalorder",
	"totalorderf":       "math.Totalorderf",
	"totalorderl":       "math.Totalorder",
	"totalordermag":     "math.Totalordermag",
	"totalordermagf":    "math.Totalordermagf",
	"totalordermagl":    "math.Totalordermag",
	"fadd":              "math.Fadd",
	"faddl":             "math.Fadd",
	"daddl":             "math.Daddl",
	"fsub":              "math.Fsub",
	"fsubl":             "math.Fsub",
	"dsubl":             "math.Dsubl",
	"fmul":              "math.Fmul",
	"fmull":             "math.Fmul",
	"dmull":             "math.Dmull",
	"fdiv":              "math.Fdiv",
	"fdivl":             "math.Fdiv",
	"ddivl":             "math.Ddivl",
	"ffma":              "math.Ffma",
	"ffmal":             "math.Ffma",
	"dfmal":             "math.Dfmal",
	"fsqrt":             "math.Fsqrt",
	"fsqrtl":            "math.Fsqrt",
	"dsqrtl":            "math.Dsqrtl",
	"__cc_issignaling":  "math.Issignaling",
	"__cc_issignalingf": "math.Issignalingf",
	"__cc_iseqsig":      "math.Iseqsig",
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
			libc["stdc_"+f+"_"+c] = "stdbit." + g + s
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
