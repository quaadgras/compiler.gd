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
	"alloca": "Malloc",

	// pthread.h
	"pthread_create": "PthreadCreate", "pthread_join": "PthreadJoin", "pthread_detach": "PthreadDetach",
	"pthread_exit": "PthreadExit", "pthread_self": "PthreadSelf", "pthread_equal": "PthreadEqual",
	"pthread_mutex_init": "PthreadMutexInit", "pthread_mutex_destroy": "PthreadMutexDestroy",
	"pthread_mutex_lock": "PthreadMutexLock", "pthread_mutex_trylock": "PthreadMutexTrylock",
	"pthread_mutex_unlock": "PthreadMutexUnlock", "pthread_cond_init": "PthreadCondInit",
	"pthread_cond_destroy": "PthreadCondDestroy", "pthread_cond_wait": "PthreadCondWait",
	"pthread_cond_signal": "PthreadCondSignal", "pthread_cond_broadcast": "PthreadCondBroadcast",
	"sched_yield": "SchedYield",
	"longjmp":     "Longjmp", "_longjmp": "Longjmp", "siglongjmp": "Longjmp",
	"strnlen": "Strnlen", "strpbrk": "Strpbrk", "strcoll": "Strcoll", "strtok": "Strtok",
	"strtok_r": "StrtokR", "strerror": "Strerror", "memset_explicit": "MemsetExplicit",

	// stdio.h
	"fopen": "Fopen", "fclose": "Fclose", "tmpfile": "Tmpfile", "fseek": "Fseek", "ftell": "Ftell",
	"rewind": "Rewind", "feof": "Feof", "ferror": "Ferror", "clearerr": "Clearerr",
	"ungetc": "Ungetc", "setvbuf": "Setvbuf", "setbuf": "Setbuf", "remove": "Remove",
	"rename": "Rename", "perror": "Perror",

	// stdlib.h
	"strtof": "Strtof", "strtold": "Strtold", "atexit": "Atexit", "qsort": "Qsort",
	"bsearch": "Bsearch",

	// errno.h
	"__cc_errno": "ErrnoLoc",

	// ctype.h
	"isalnum": "Isalnum", "isalpha": "Isalpha", "isblank": "Isblank", "iscntrl": "Iscntrl",
	"isdigit": "Isdigit", "isgraph": "Isgraph", "islower": "Islower", "isprint": "Isprint",
	"ispunct": "Ispunct", "isspace": "Isspace", "isupper": "Isupper", "isxdigit": "Isxdigit",
	"tolower": "Tolower", "toupper": "Toupper",

	// inttypes.h, time.h
	"imaxabs": "Labs", "strtoimax": "Strtol", "strtoumax": "Strtoul",
	"time": "Time", "clock": "Clock", "difftime": "Difftime",

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
