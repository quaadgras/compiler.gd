// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "sort"

// sysHeaders are the standard headers cmd/cc provides. The functions
// they declare are implemented in Go, in internal/c23.
var sysHeaders = map[string]string{
	"ctype.h": `#ifndef __CC_CTYPE_H
#define __CC_CTYPE_H
int isalnum(int c);
int isalpha(int c);
int isblank(int c);
int iscntrl(int c);
int isdigit(int c);
int isgraph(int c);
int islower(int c);
int isprint(int c);
int ispunct(int c);
int isspace(int c);
int isupper(int c);
int isxdigit(int c);
int tolower(int c);
int toupper(int c);
#endif
`,
	"errno.h": `#ifndef __CC_ERRNO_H
#define __CC_ERRNO_H
int *__cc_errno(void);
#define errno (*__cc_errno())
#define EPERM 1
#define ENOENT 2
#define ESRCH 3
#define EINTR 4
#define EIO 5
#define ENXIO 6
#define E2BIG 7
#define ENOEXEC 8
#define EBADF 9
#define ECHILD 10
#define EAGAIN 11
#define ENOMEM 12
#define EACCES 13
#define EFAULT 14
#define ENOTBLK 15
#define EBUSY 16
#define EEXIST 17
#define EXDEV 18
#define ENODEV 19
#define ENOTDIR 20
#define EISDIR 21
#define EINVAL 22
#define ENFILE 23
#define EMFILE 24
#define ENOTTY 25
#define ETXTBSY 26
#define EFBIG 27
#define ENOSPC 28
#define ESPIPE 29
#define EROFS 30
#define EMLINK 31
#define EPIPE 32
#define EDOM 33
#define ERANGE 34
#define EDEADLK 35
#define ENAMETOOLONG 36
#define ENOLCK 37
#define ENOSYS 38
#define ENOTEMPTY 39
#define ELOOP 40
#define EOVERFLOW 75
#define EILSEQ 84
#define ENOTSOCK 88
#define EOPNOTSUPP 95
#define EADDRINUSE 98
#define ECONNRESET 104
#define ETIMEDOUT 110
#define ECONNREFUSED 111
#define ENOTSUP EOPNOTSUPP
#endif
`,
	"inttypes.h": `#ifndef __CC_INTTYPES_H
#define __CC_INTTYPES_H
#include <stdint.h>
#define PRId8 "d"
#define PRId16 "d"
#define PRId32 "d"
#define PRId64 "ld"
#define PRIdLEAST8 "d"
#define PRIdLEAST16 "d"
#define PRIdLEAST32 "d"
#define PRIdLEAST64 "ld"
#define PRIdFAST8 "d"
#define PRIdFAST16 "ld"
#define PRIdFAST32 "ld"
#define PRIdFAST64 "ld"
#define PRIdMAX "ld"
#define PRIdPTR "ld"
#define PRIi8 "i"
#define PRIi16 "i"
#define PRIi32 "i"
#define PRIi64 "li"
#define PRIiLEAST8 "i"
#define PRIiLEAST16 "i"
#define PRIiLEAST32 "i"
#define PRIiLEAST64 "li"
#define PRIiFAST8 "i"
#define PRIiFAST16 "li"
#define PRIiFAST32 "li"
#define PRIiFAST64 "li"
#define PRIiMAX "li"
#define PRIiPTR "li"
#define PRIo8 "o"
#define PRIo16 "o"
#define PRIo32 "o"
#define PRIo64 "lo"
#define PRIoLEAST8 "o"
#define PRIoLEAST16 "o"
#define PRIoLEAST32 "o"
#define PRIoLEAST64 "lo"
#define PRIoFAST8 "o"
#define PRIoFAST16 "lo"
#define PRIoFAST32 "lo"
#define PRIoFAST64 "lo"
#define PRIoMAX "lo"
#define PRIoPTR "lo"
#define PRIu8 "u"
#define PRIu16 "u"
#define PRIu32 "u"
#define PRIu64 "lu"
#define PRIuLEAST8 "u"
#define PRIuLEAST16 "u"
#define PRIuLEAST32 "u"
#define PRIuLEAST64 "lu"
#define PRIuFAST8 "u"
#define PRIuFAST16 "lu"
#define PRIuFAST32 "lu"
#define PRIuFAST64 "lu"
#define PRIuMAX "lu"
#define PRIuPTR "lu"
#define PRIx8 "x"
#define PRIx16 "x"
#define PRIx32 "x"
#define PRIx64 "lx"
#define PRIxLEAST8 "x"
#define PRIxLEAST16 "x"
#define PRIxLEAST32 "x"
#define PRIxLEAST64 "lx"
#define PRIxFAST8 "x"
#define PRIxFAST16 "lx"
#define PRIxFAST32 "lx"
#define PRIxFAST64 "lx"
#define PRIxMAX "lx"
#define PRIxPTR "lx"
#define PRIX8 "X"
#define PRIX16 "X"
#define PRIX32 "X"
#define PRIX64 "lX"
#define PRIXLEAST8 "X"
#define PRIXLEAST16 "X"
#define PRIXLEAST32 "X"
#define PRIXLEAST64 "lX"
#define PRIXFAST8 "X"
#define PRIXFAST16 "lX"
#define PRIXFAST32 "lX"
#define PRIXFAST64 "lX"
#define PRIXMAX "lX"
#define PRIXPTR "lX"
#define PRIb8 "b"
#define PRIb16 "b"
#define PRIb32 "b"
#define PRIb64 "lb"
#define PRIbMAX "lb"
#define PRIbPTR "lb"
#define PRIB8 "B"
#define PRIB16 "B"
#define PRIB32 "B"
#define PRIB64 "lB"
#define PRIBMAX "lB"
#define PRIBPTR "lB"
#define SCNd8 "hhd"
#define SCNd16 "hd"
#define SCNd32 "d"
#define SCNd64 "ld"
#define SCNdLEAST8 "hhd"
#define SCNdLEAST16 "hd"
#define SCNdLEAST32 "d"
#define SCNdLEAST64 "ld"
#define SCNdFAST8 "hhd"
#define SCNdFAST16 "ld"
#define SCNdFAST32 "ld"
#define SCNdFAST64 "ld"
#define SCNdMAX "ld"
#define SCNdPTR "ld"
#define SCNi8 "hhi"
#define SCNi16 "hi"
#define SCNi32 "i"
#define SCNi64 "li"
#define SCNiMAX "li"
#define SCNiPTR "li"
#define SCNo8 "hho"
#define SCNo16 "ho"
#define SCNo32 "o"
#define SCNo64 "lo"
#define SCNoMAX "lo"
#define SCNoPTR "lo"
#define SCNu8 "hhu"
#define SCNu16 "hu"
#define SCNu32 "u"
#define SCNu64 "lu"
#define SCNuLEAST8 "hhu"
#define SCNuLEAST16 "hu"
#define SCNuLEAST32 "u"
#define SCNuLEAST64 "lu"
#define SCNuFAST8 "hhu"
#define SCNuFAST16 "lu"
#define SCNuFAST32 "lu"
#define SCNuFAST64 "lu"
#define SCNuMAX "lu"
#define SCNuPTR "lu"
#define SCNx8 "hhx"
#define SCNx16 "hx"
#define SCNx32 "x"
#define SCNx64 "lx"
#define SCNxMAX "lx"
#define SCNxPTR "lx"
intmax_t imaxabs(intmax_t j);
intmax_t strtoimax(const char *restrict nptr, char **restrict endptr, int base);
uintmax_t strtoumax(const char *restrict nptr, char **restrict endptr, int base);
typedef struct { intmax_t quot; intmax_t rem; } imaxdiv_t;
imaxdiv_t imaxdiv(intmax_t numer, intmax_t denom);
#endif
`,
	"math.h": `#ifndef __CC_MATH_H
#define __CC_MATH_H
typedef float float_t;
typedef double double_t;
#define HUGE_VAL 1e10000
#define HUGE_VALF 1e10000f
#define HUGE_VALL 1e10000L
#define INFINITY 1e10000f
#define NAN (0.0f/0.0f)
#define FP_NAN 0
#define FP_INFINITE 1
#define FP_ZERO 2
#define FP_SUBNORMAL 3
#define FP_NORMAL 4
#define FP_ILOGB0 (-2147483647-1)
#define FP_ILOGBNAN (-2147483647-1)
#define MATH_ERRNO 1
#define MATH_ERREXCEPT 2
#define math_errhandling MATH_ERRNO
int __cc_fpclassify(double x);
int __cc_fpclassifyf(float x);
int __cc_signbit(double x);
#define fpclassify(x) _Generic((x), float: __cc_fpclassifyf, default: __cc_fpclassify)(x)
#define isnan(x) (fpclassify(x) == FP_NAN)
#define isinf(x) (fpclassify(x) == FP_INFINITE)
#define isfinite(x) (fpclassify(x) >= FP_ZERO)
#define isnormal(x) (fpclassify(x) == FP_NORMAL)
#define signbit(x) __cc_signbit(x)
#define isgreater(x, y) ((x) > (y))
#define isgreaterequal(x, y) ((x) >= (y))
#define isless(x, y) ((x) < (y))
#define islessequal(x, y) ((x) <= (y))
#define islessgreater(x, y) ((x) < (y) || (x) > (y))
#define isunordered(x, y) (isnan(x) || isnan(y))
#define M_E 2.7182818284590452354
#define M_LOG2E 1.4426950408889634074
#define M_LOG10E 0.43429448190325182765
#define M_LN2 0.69314718055994530942
#define M_LN10 2.30258509299404568402
#define M_PI 3.14159265358979323846
#define M_PI_2 1.57079632679489661923
#define M_PI_4 0.78539816339744830962
#define M_1_PI 0.31830988618379067154
#define M_2_PI 0.63661977236758134308
#define M_2_SQRTPI 1.12837916709551257390
#define M_SQRT2 1.41421356237309504880
#define M_SQRT1_2 0.70710678118654752440
double sin(double x);
double cos(double x);
double tan(double x);
double asin(double x);
double acos(double x);
double atan(double x);
double sinh(double x);
double cosh(double x);
double tanh(double x);
double asinh(double x);
double acosh(double x);
double atanh(double x);
double exp(double x);
double exp2(double x);
double expm1(double x);
double log(double x);
double log2(double x);
double log10(double x);
double log1p(double x);
double logb(double x);
double sqrt(double x);
double cbrt(double x);
double fabs(double x);
double floor(double x);
double ceil(double x);
double round(double x);
double trunc(double x);
double rint(double x);
double nearbyint(double x);
double erf(double x);
double erfc(double x);
double tgamma(double x);
double lgamma(double x);
double atan2(double x, double y);
double pow(double x, double y);
double hypot(double x, double y);
double fmod(double x, double y);
double remainder(double x, double y);
double fmin(double x, double y);
double fmax(double x, double y);
double fdim(double x, double y);
double copysign(double x, double y);
double nextafter(double x, double y);
double fma(double x, double y, double z);
int ilogb(double x);
long lround(double x);
long lrint(double x);
long long llround(double x);
long long llrint(double x);
double ldexp(double x, int exp);
double scalbn(double x, int n);
double scalbln(double x, long n);
double frexp(double value, int *exp);
double modf(double value, double *iptr);
double nan(const char *tagp);
float sinf(float x);
float cosf(float x);
float tanf(float x);
float asinf(float x);
float acosf(float x);
float atanf(float x);
float sinhf(float x);
float coshf(float x);
float tanhf(float x);
float asinhf(float x);
float acoshf(float x);
float atanhf(float x);
float expf(float x);
float exp2f(float x);
float expm1f(float x);
float logf(float x);
float log2f(float x);
float log10f(float x);
float log1pf(float x);
float logbf(float x);
float sqrtf(float x);
float cbrtf(float x);
float fabsf(float x);
float floorf(float x);
float ceilf(float x);
float roundf(float x);
float truncf(float x);
float rintf(float x);
float nearbyintf(float x);
float erff(float x);
float erfcf(float x);
float tgammaf(float x);
float lgammaf(float x);
float atan2f(float x, float y);
float powf(float x, float y);
float hypotf(float x, float y);
float fmodf(float x, float y);
float remainderf(float x, float y);
float fminf(float x, float y);
float fmaxf(float x, float y);
float fdimf(float x, float y);
float copysignf(float x, float y);
float nextafterf(float x, float y);
float fmaf(float x, float y, float z);
int ilogbf(float x);
long lroundf(float x);
long lrintf(float x);
long long llroundf(float x);
long long llrintf(float x);
float ldexpf(float x, int exp);
float scalbnf(float x, int n);
float scalblnf(float x, long n);
float frexpf(float value, int *exp);
float modff(float value, float *iptr);
float nanf(const char *tagp);
long double sinl(long double x);
long double cosl(long double x);
long double tanl(long double x);
long double asinl(long double x);
long double acosl(long double x);
long double atanl(long double x);
long double sinhl(long double x);
long double coshl(long double x);
long double tanhl(long double x);
long double asinhl(long double x);
long double acoshl(long double x);
long double atanhl(long double x);
long double expl(long double x);
long double exp2l(long double x);
long double expm1l(long double x);
long double logl(long double x);
long double log2l(long double x);
long double log10l(long double x);
long double log1pl(long double x);
long double logbl(long double x);
long double sqrtl(long double x);
long double cbrtl(long double x);
long double fabsl(long double x);
long double floorl(long double x);
long double ceill(long double x);
long double roundl(long double x);
long double truncl(long double x);
long double rintl(long double x);
long double nearbyintl(long double x);
long double erfl(long double x);
long double erfcl(long double x);
long double tgammal(long double x);
long double lgammal(long double x);
long double atan2l(long double x, long double y);
long double powl(long double x, long double y);
long double hypotl(long double x, long double y);
long double fmodl(long double x, long double y);
long double remainderl(long double x, long double y);
long double fminl(long double x, long double y);
long double fmaxl(long double x, long double y);
long double fdiml(long double x, long double y);
long double copysignl(long double x, long double y);
long double nextafterl(long double x, long double y);
long double fmal(long double x, long double y, long double z);
int ilogbl(long double x);
long lroundl(long double x);
long lrintl(long double x);
long long llroundl(long double x);
long long llrintl(long double x);
long double ldexpl(long double x, int exp);
long double scalbnl(long double x, int n);
long double scalblnl(long double x, long n);
long double frexpl(long double value, int *exp);
long double modfl(long double value, long double *iptr);
long double nanl(const char *tagp);
#define __STDC_VERSION_MATH_H__ 202311L
#endif
`,
	"time.h": `#ifndef __CC_TIME_H
#define __CC_TIME_H
#include <stddef.h>
typedef long time_t;
typedef long clock_t;
#define CLOCKS_PER_SEC ((clock_t)1000000)
time_t time(time_t *timer);
clock_t clock(void);
double difftime(time_t time1, time_t time0);
struct tm {
	int tm_sec;
	int tm_min;
	int tm_hour;
	int tm_mday;
	int tm_mon;
	int tm_year;
	int tm_wday;
	int tm_yday;
	int tm_isdst;
};
struct timespec {
	time_t tv_sec;
	long tv_nsec;
};
#define TIME_UTC 1
time_t mktime(struct tm *timeptr);
time_t timegm(struct tm *timeptr);
struct tm *gmtime(const time_t *timer);
struct tm *gmtime_r(const time_t *timer, struct tm *buf);
struct tm *localtime(const time_t *timer);
struct tm *localtime_r(const time_t *timer, struct tm *buf);
[[deprecated]] char *asctime(const struct tm *timeptr);
[[deprecated]] char *ctime(const time_t *timer);
size_t strftime(char *restrict s, size_t maxsize, const char *restrict format, const struct tm *restrict timeptr);
int timespec_get(struct timespec *ts, int base);
int timespec_getres(struct timespec *ts, int base);
#define __STDC_VERSION_TIME_H__ 202311L
#endif
`,
	"uchar.h": `#ifndef __CC_UCHAR_H
#define __CC_UCHAR_H
#include <stddef.h>
#ifndef __CC_MBSTATE_T
#define __CC_MBSTATE_T
typedef struct { int __count; unsigned __value; } mbstate_t;
#endif
typedef unsigned char char8_t;
typedef unsigned short char16_t;
typedef unsigned int char32_t;
size_t mbrtoc8(char8_t *restrict pc8, const char *restrict s, size_t n, mbstate_t *restrict ps);
size_t c8rtomb(char *restrict s, char8_t c8, mbstate_t *restrict ps);
size_t mbrtoc16(char16_t *restrict pc16, const char *restrict s, size_t n, mbstate_t *restrict ps);
size_t c16rtomb(char *restrict s, char16_t c16, mbstate_t *restrict ps);
size_t mbrtoc32(char32_t *restrict pc32, const char *restrict s, size_t n, mbstate_t *restrict ps);
size_t c32rtomb(char *restrict s, char32_t c32, mbstate_t *restrict ps);
#define __STDC_VERSION_UCHAR_H__ 202311L
#endif
`,
	"wchar.h": `#ifndef __CC_WCHAR_H
#define __CC_WCHAR_H
#include <stddef.h>
#ifndef __CC_MBSTATE_T
#define __CC_MBSTATE_T
typedef struct { int __count; unsigned __value; } mbstate_t;
#endif
typedef unsigned int wint_t;
#define WEOF (0xffffffffu)
#define WCHAR_MIN (-2147483647-1)
#define WCHAR_MAX 2147483647
size_t wcslen(const wchar_t *s);
int wcscmp(const wchar_t *s1, const wchar_t *s2);
int wcsncmp(const wchar_t *s1, const wchar_t *s2, size_t n);
wchar_t *wcscpy(wchar_t *restrict s1, const wchar_t *restrict s2);
wchar_t *wcscat(wchar_t *restrict s1, const wchar_t *restrict s2);
wchar_t *wcschr(const wchar_t *s, wchar_t c);
wint_t btowc(int c);
int wctob(wint_t c);
int mbsinit(const mbstate_t *ps);
size_t mbrlen(const char *restrict s, size_t n, mbstate_t *restrict ps);
size_t mbrtowc(wchar_t *restrict pwc, const char *restrict s, size_t n, mbstate_t *restrict ps);
size_t wcrtomb(char *restrict s, wchar_t wc, mbstate_t *restrict ps);
#define __STDC_VERSION_WCHAR_H__ 202311L
#endif
`,
	"alloca.h": `#ifndef __CC_ALLOCA_H
#define __CC_ALLOCA_H
#include <stddef.h>
void *alloca(size_t size);
#define alloca(size) __builtin_alloca(size)
#endif
`,
	"assert.h": `#undef assert
#ifdef NDEBUG
#define assert(...) ((void)0)
#else
#define assert(...) __builtin_assert((__VA_ARGS__), #__VA_ARGS__, __FILE__, __LINE__)
#endif
#ifndef __cplusplus
#define static_assert _Static_assert
#endif
`,
	"float.h": `#ifndef __CC_FLOAT_H
#define __CC_FLOAT_H
#define FLT_RADIX 2
#define FLT_MANT_DIG 24
#define DBL_MANT_DIG 53
#define LDBL_MANT_DIG 53
#define FLT_DIG 6
#define DBL_DIG 15
#define LDBL_DIG 15
#define FLT_DECIMAL_DIG 9
#define DBL_DECIMAL_DIG 17
#define LDBL_DECIMAL_DIG 17
#define DECIMAL_DIG 17
#define FLT_EVAL_METHOD 0
#define FLT_HAS_SUBNORM 1
#define DBL_HAS_SUBNORM 1
#define LDBL_HAS_SUBNORM 1
#define FLT_MIN_EXP (-125)
#define DBL_MIN_EXP (-1021)
#define LDBL_MIN_EXP (-1021)
#define FLT_MIN_10_EXP (-37)
#define DBL_MIN_10_EXP (-307)
#define LDBL_MIN_10_EXP (-307)
#define FLT_MAX_EXP 128
#define DBL_MAX_EXP 1024
#define LDBL_MAX_EXP 1024
#define FLT_MAX_10_EXP 38
#define DBL_MAX_10_EXP 308
#define LDBL_MAX_10_EXP 308
#define FLT_MAX 0x1.fffffep+127f
#define DBL_MAX 0x1.fffffffffffffp+1023
#define LDBL_MAX 0x1.fffffffffffffp+1023L
#define FLT_EPSILON 0x1p-23f
#define DBL_EPSILON 0x1p-52
#define LDBL_EPSILON 0x1p-52L
#define FLT_MIN 0x1p-126f
#define DBL_MIN 0x1p-1022
#define LDBL_MIN 0x1p-1022L
#define FLT_TRUE_MIN 0x1p-149f
#define DBL_TRUE_MIN 0x1p-1074
#define LDBL_TRUE_MIN 0x1p-1074L
#define FLT_ROUNDS 1
#endif
`,
	"iso646.h": `#ifndef __CC_ISO646_H
#define __CC_ISO646_H
#define and &&
#define and_eq &=
#define bitand &
#define bitor |
#define compl ~
#define not !
#define not_eq !=
#define or ||
#define or_eq |=
#define xor ^
#define xor_eq ^=
#endif
`,
	"limits.h": `#ifndef __CC_LIMITS_H
#define __CC_LIMITS_H
#define CHAR_BIT 8
#define BOOL_WIDTH 1
#define CHAR_WIDTH 8
#define SCHAR_WIDTH 8
#define UCHAR_WIDTH 8
#define SHRT_WIDTH 16
#define USHRT_WIDTH 16
#define INT_WIDTH 32
#define UINT_WIDTH 32
#define LONG_WIDTH 64
#define ULONG_WIDTH 64
#define LLONG_WIDTH 64
#define ULLONG_WIDTH 64
#define MB_LEN_MAX 4
#define SCHAR_MIN (-128)
#define SCHAR_MAX 127
#define UCHAR_MAX 255
#define CHAR_MIN SCHAR_MIN
#define CHAR_MAX SCHAR_MAX
#define SHRT_MIN (-32768)
#define SHRT_MAX 32767
#define USHRT_MAX 65535
#define INT_MIN (-2147483647-1)
#define INT_MAX 2147483647
#define UINT_MAX 4294967295U
#define LONG_MIN (-9223372036854775807L-1)
#define LONG_MAX 9223372036854775807L
#define ULONG_MAX 18446744073709551615UL
#define LLONG_MIN (-9223372036854775807LL-1)
#define LLONG_MAX 9223372036854775807LL
#define ULLONG_MAX 18446744073709551615ULL
#define BITINT_MAXWIDTH 64
#endif
`,
	"setjmp.h": `#ifndef __CC_SETJMP_H
#define __CC_SETJMP_H
typedef struct { void *__p; } jmp_buf[1];
typedef jmp_buf sigjmp_buf;
#define setjmp(env) __builtin_setjmp(env)
#define _setjmp(env) __builtin_setjmp(env)
#define sigsetjmp(env, save) __builtin_setjmp(env)
[[noreturn]] void longjmp(jmp_buf env, int val);
[[noreturn]] void _longjmp(jmp_buf env, int val);
[[noreturn]] void siglongjmp(sigjmp_buf env, int val);
#define __STDC_VERSION_SETJMP_H__ 202311L
#endif
`,
	"stddefer.h": `#ifndef __CC_STDDEFER_H
#define __CC_STDDEFER_H
#define defer _Defer
#define __STDC_VERSION_STDDEFER_H__ 202602L
#endif
`,
	"pthread.h": `#ifndef __CC_PTHREAD_H
#define __CC_PTHREAD_H
#include <stddef.h>
typedef unsigned long pthread_t;
typedef struct { int __a; } pthread_attr_t;
typedef struct { long __m[5]; } pthread_mutex_t;
typedef struct { int __a; } pthread_mutexattr_t;
typedef struct { long __c[6]; } pthread_cond_t;
typedef struct { int __a; } pthread_condattr_t;
#define PTHREAD_MUTEX_INITIALIZER {{0}}
#define PTHREAD_COND_INITIALIZER {{0}}
int pthread_create(pthread_t *restrict thread, const pthread_attr_t *restrict attr, void *(*start_routine)(void *), void *restrict arg);
int pthread_join(pthread_t thread, void **retval);
int pthread_detach(pthread_t thread);
[[noreturn]] void pthread_exit(void *retval);
pthread_t pthread_self(void);
int pthread_equal(pthread_t t1, pthread_t t2);
int pthread_mutex_init(pthread_mutex_t *restrict mutex, const pthread_mutexattr_t *restrict attr);
int pthread_mutex_destroy(pthread_mutex_t *mutex);
int pthread_mutex_lock(pthread_mutex_t *mutex);
int pthread_mutex_trylock(pthread_mutex_t *mutex);
int pthread_mutex_unlock(pthread_mutex_t *mutex);
int pthread_cond_init(pthread_cond_t *restrict cond, const pthread_condattr_t *restrict attr);
int pthread_cond_destroy(pthread_cond_t *cond);
int pthread_cond_wait(pthread_cond_t *restrict cond, pthread_mutex_t *restrict mutex);
int pthread_cond_signal(pthread_cond_t *cond);
int pthread_cond_broadcast(pthread_cond_t *cond);
int sched_yield(void);
#endif
`,
	"stdatomic.h": `#ifndef __CC_STDATOMIC_H
#define __CC_STDATOMIC_H
#include <stddef.h>
#include <stdint.h>
typedef enum memory_order {
	memory_order_relaxed, memory_order_consume, memory_order_acquire,
	memory_order_release, memory_order_acq_rel, memory_order_seq_cst
} memory_order;
#define ATOMIC_VAR_INIT(value) (value)
#define kill_dependency(y) (y)
#define ATOMIC_BOOL_LOCK_FREE 2
#define ATOMIC_CHAR_LOCK_FREE 2
#define ATOMIC_CHAR16_T_LOCK_FREE 2
#define ATOMIC_CHAR32_T_LOCK_FREE 2
#define ATOMIC_WCHAR_T_LOCK_FREE 2
#define ATOMIC_SHORT_LOCK_FREE 2
#define ATOMIC_INT_LOCK_FREE 2
#define ATOMIC_LONG_LOCK_FREE 2
#define ATOMIC_LLONG_LOCK_FREE 2
#define ATOMIC_POINTER_LOCK_FREE 2
#define atomic_init(obj, value) __cc_atomic_store(obj, value)
#define atomic_is_lock_free(obj) 1
#define atomic_thread_fence(order) __cc_atomic_fence()
#define atomic_signal_fence(order) __cc_atomic_fence()
#define atomic_store(obj, desired) __cc_atomic_store(obj, desired)
#define atomic_store_explicit(obj, desired, order) __cc_atomic_store(obj, desired)
#define atomic_load(obj) __cc_atomic_load(obj)
#define atomic_load_explicit(obj, order) __cc_atomic_load(obj)
#define atomic_exchange(obj, desired) __cc_atomic_fetch(obj, '=', desired)
#define atomic_exchange_explicit(obj, desired, order) __cc_atomic_fetch(obj, '=', desired)
#define atomic_compare_exchange_strong(obj, expected, desired) __cc_atomic_cmpxchg(obj, expected, desired)
#define atomic_compare_exchange_strong_explicit(obj, expected, desired, s, f) __cc_atomic_cmpxchg(obj, expected, desired)
#define atomic_compare_exchange_weak(obj, expected, desired) __cc_atomic_cmpxchg(obj, expected, desired)
#define atomic_compare_exchange_weak_explicit(obj, expected, desired, s, f) __cc_atomic_cmpxchg(obj, expected, desired)
#define atomic_fetch_add(obj, arg) __cc_atomic_fetch(obj, '+', arg)
#define atomic_fetch_add_explicit(obj, arg, order) __cc_atomic_fetch(obj, '+', arg)
#define atomic_fetch_sub(obj, arg) __cc_atomic_fetch(obj, '-', arg)
#define atomic_fetch_sub_explicit(obj, arg, order) __cc_atomic_fetch(obj, '-', arg)
#define atomic_fetch_or(obj, arg) __cc_atomic_fetch(obj, '|', arg)
#define atomic_fetch_or_explicit(obj, arg, order) __cc_atomic_fetch(obj, '|', arg)
#define atomic_fetch_xor(obj, arg) __cc_atomic_fetch(obj, '^', arg)
#define atomic_fetch_xor_explicit(obj, arg, order) __cc_atomic_fetch(obj, '^', arg)
#define atomic_fetch_and(obj, arg) __cc_atomic_fetch(obj, '&', arg)
#define atomic_fetch_and_explicit(obj, arg, order) __cc_atomic_fetch(obj, '&', arg)
typedef struct atomic_flag { _Atomic _Bool __v; } atomic_flag;
#define ATOMIC_FLAG_INIT {0}
#define atomic_flag_test_and_set(obj) __cc_atomic_fetch(&(obj)->__v, '=', 1)
#define atomic_flag_test_and_set_explicit(obj, order) __cc_atomic_fetch(&(obj)->__v, '=', 1)
#define atomic_flag_clear(obj) __cc_atomic_store(&(obj)->__v, 0)
#define atomic_flag_clear_explicit(obj, order) __cc_atomic_store(&(obj)->__v, 0)
typedef _Atomic _Bool atomic_bool;
typedef _Atomic char atomic_char;
typedef _Atomic signed char atomic_schar;
typedef _Atomic unsigned char atomic_uchar;
typedef _Atomic short atomic_short;
typedef _Atomic unsigned short atomic_ushort;
typedef _Atomic int atomic_int;
typedef _Atomic unsigned int atomic_uint;
typedef _Atomic long atomic_long;
typedef _Atomic unsigned long atomic_ulong;
typedef _Atomic long long atomic_llong;
typedef _Atomic unsigned long long atomic_ullong;
typedef _Atomic unsigned short atomic_char16_t;
typedef _Atomic unsigned int atomic_char32_t;
typedef _Atomic int atomic_wchar_t;
typedef _Atomic intptr_t atomic_intptr_t;
typedef _Atomic uintptr_t atomic_uintptr_t;
typedef _Atomic size_t atomic_size_t;
typedef _Atomic ptrdiff_t atomic_ptrdiff_t;
typedef _Atomic intmax_t atomic_intmax_t;
typedef _Atomic uintmax_t atomic_uintmax_t;
#define __STDC_VERSION_STDATOMIC_H__ 202311L
#endif
`,
	"stdckdint.h": `#ifndef __CC_STDCKDINT_H
#define __CC_STDCKDINT_H
#define ckd_add(result, a, b) __builtin_add_overflow(a, b, result)
#define ckd_sub(result, a, b) __builtin_sub_overflow(a, b, result)
#define ckd_mul(result, a, b) __builtin_mul_overflow(a, b, result)
#define __STDC_VERSION_STDCKDINT_H__ 202311L
#endif
`,
	"stdalign.h": ``,
	"stdarg.h": `#ifndef __CC_STDARG_H
#define __CC_STDARG_H
typedef __builtin_va_list va_list;
#define va_start(ap, ...) __builtin_va_start(ap)
#define va_arg(ap, type) __builtin_va_arg(ap, type)
#define va_copy(dest, src) __builtin_va_copy(dest, src)
#define va_end(ap) __builtin_va_end(ap)
#define __STDC_VERSION_STDARG_H__ 202311L
#endif
`,
	"stdbool.h": `#ifndef __CC_STDBOOL_H
#define __CC_STDBOOL_H
#define __bool_true_false_are_defined 1
#endif
`,
	"stddef.h": `#ifndef __CC_STDDEF_H
#define __CC_STDDEF_H
#define NULL ((void*)0)
typedef unsigned long size_t;
typedef long ptrdiff_t;
typedef int wchar_t;
typedef long max_align_t;
typedef typeof(nullptr) nullptr_t;
#define offsetof(type, member) __builtin_offsetof(type, member)
#define unreachable() __builtin_unreachable()
#define __STDC_VERSION_STDDEF_H__ 202311L
#endif
`,
	"stdint.h": `#ifndef __CC_STDINT_H
#define __CC_STDINT_H
typedef signed char int8_t;
typedef short int16_t;
typedef int int32_t;
typedef long int64_t;
typedef unsigned char uint8_t;
typedef unsigned short uint16_t;
typedef unsigned int uint32_t;
typedef unsigned long uint64_t;
typedef signed char int_least8_t;
typedef short int_least16_t;
typedef int int_least32_t;
typedef long int_least64_t;
typedef unsigned char uint_least8_t;
typedef unsigned short uint_least16_t;
typedef unsigned int uint_least32_t;
typedef unsigned long uint_least64_t;
typedef signed char int_fast8_t;
typedef long int_fast16_t;
typedef long int_fast32_t;
typedef long int_fast64_t;
typedef unsigned char uint_fast8_t;
typedef unsigned long uint_fast16_t;
typedef unsigned long uint_fast32_t;
typedef unsigned long uint_fast64_t;
typedef long intptr_t;
typedef unsigned long uintptr_t;
typedef long intmax_t;
typedef unsigned long uintmax_t;
#define INT8_WIDTH 8
#define INT16_WIDTH 16
#define INT32_WIDTH 32
#define INT64_WIDTH 64
#define INT8_MIN (-128)
#define INT16_MIN (-32768)
#define INT32_MIN (-2147483647-1)
#define INT64_MIN (-9223372036854775807L-1)
#define INT8_MAX 127
#define INT16_MAX 32767
#define INT32_MAX 2147483647
#define INT64_MAX 9223372036854775807L
#define UINT8_MAX 255
#define UINT16_MAX 65535
#define UINT32_MAX 4294967295U
#define UINT64_MAX 18446744073709551615UL
#define INT_LEAST8_MIN INT8_MIN
#define INT_LEAST16_MIN INT16_MIN
#define INT_LEAST32_MIN INT32_MIN
#define INT_LEAST64_MIN INT64_MIN
#define INT_LEAST8_MAX INT8_MAX
#define INT_LEAST16_MAX INT16_MAX
#define INT_LEAST32_MAX INT32_MAX
#define INT_LEAST64_MAX INT64_MAX
#define UINT_LEAST8_MAX UINT8_MAX
#define UINT_LEAST16_MAX UINT16_MAX
#define UINT_LEAST32_MAX UINT32_MAX
#define UINT_LEAST64_MAX UINT64_MAX
#define INT_FAST8_MIN INT8_MIN
#define INT_FAST16_MIN INT64_MIN
#define INT_FAST32_MIN INT64_MIN
#define INT_FAST64_MIN INT64_MIN
#define INT_FAST8_MAX INT8_MAX
#define INT_FAST16_MAX INT64_MAX
#define INT_FAST32_MAX INT64_MAX
#define INT_FAST64_MAX INT64_MAX
#define UINT_FAST8_MAX UINT8_MAX
#define UINT_FAST16_MAX UINT64_MAX
#define UINT_FAST32_MAX UINT64_MAX
#define UINT_FAST64_MAX UINT64_MAX
#define INTPTR_MIN INT64_MIN
#define INTPTR_MAX INT64_MAX
#define UINTPTR_MAX UINT64_MAX
#define INTMAX_MIN INT64_MIN
#define INTMAX_MAX INT64_MAX
#define UINTMAX_MAX UINT64_MAX
#define PTRDIFF_MIN INT64_MIN
#define PTRDIFF_MAX INT64_MAX
#define SIZE_MAX UINT64_MAX
#define WCHAR_MIN INT32_MIN
#define WCHAR_MAX INT32_MAX
#define INT8_C(c) c
#define INT16_C(c) c
#define INT32_C(c) c
#define INT64_C(c) c ## L
#define UINT8_C(c) c
#define UINT16_C(c) c
#define UINT32_C(c) c ## U
#define UINT64_C(c) c ## UL
#define INTMAX_C(c) c ## L
#define UINTMAX_C(c) c ## UL
#define __STDC_VERSION_STDINT_H__ 202311L
#endif
`,
	"stdio.h": `#ifndef __CC_STDIO_H
#define __CC_STDIO_H
#include <stddef.h>
#include <stdarg.h>
#define EOF (-1)
#define BUFSIZ 8192
typedef struct __cc_FILE FILE;
FILE *__cc_stdio(int);
#define stdin (__cc_stdio(0))
#define stdout (__cc_stdio(1))
#define stderr (__cc_stdio(2))
int printf(const char *restrict format, ...);
int fprintf(FILE *restrict stream, const char *restrict format, ...);
int sprintf(char *restrict s, const char *restrict format, ...);
int snprintf(char *restrict s, size_t n, const char *restrict format, ...);
int vprintf(const char *restrict format, va_list arg);
int vfprintf(FILE *restrict stream, const char *restrict format, va_list arg);
int vsprintf(char *restrict s, const char *restrict format, va_list arg);
int vsnprintf(char *restrict s, size_t n, const char *restrict format, va_list arg);
int puts(const char *s);
int fputs(const char *restrict s, FILE *restrict stream);
int putchar(int c);
int fputc(int c, FILE *stream);
int putc(int c, FILE *stream);
int getchar(void);
int fgetc(FILE *stream);
int getc(FILE *stream);
char *fgets(char *restrict s, int n, FILE *restrict stream);
size_t fwrite(const void *restrict ptr, size_t size, size_t nmemb, FILE *restrict stream);
size_t fread(void *restrict ptr, size_t size, size_t nmemb, FILE *restrict stream);
int fflush(FILE *stream);
#define SEEK_SET 0
#define SEEK_CUR 1
#define SEEK_END 2
#define _IOFBF 0
#define _IOLBF 1
#define _IONBF 2
#define FILENAME_MAX 4096
#define FOPEN_MAX 16
FILE *fopen(const char *restrict filename, const char *restrict mode);
int fclose(FILE *stream);
FILE *tmpfile(void);
int fseek(FILE *stream, long offset, int whence);
long ftell(FILE *stream);
void rewind(FILE *stream);
int feof(FILE *stream);
int ferror(FILE *stream);
void clearerr(FILE *stream);
int ungetc(int c, FILE *stream);
int setvbuf(FILE *restrict stream, char *restrict buf, int mode, size_t size);
void setbuf(FILE *restrict stream, char *restrict buf);
int remove(const char *filename);
int rename(const char *old, const char *new);
void perror(const char *s);
FILE *freopen(const char *restrict filename, const char *restrict mode, FILE *restrict stream);
#define L_tmpnam 20
#define TMP_MAX 238328
char *tmpnam(char *s);
int scanf(const char *restrict format, ...);
int fscanf(FILE *restrict stream, const char *restrict format, ...);
int sscanf(const char *restrict s, const char *restrict format, ...);
int vscanf(const char *restrict format, va_list arg);
int vfscanf(FILE *restrict stream, const char *restrict format, va_list arg);
int vsscanf(const char *restrict s, const char *restrict format, va_list arg);
#define __STDC_VERSION_STDIO_H__ 202311L
#endif
`,
	"stdlib.h": `#ifndef __CC_STDLIB_H
#define __CC_STDLIB_H
#include <stddef.h>
#define EXIT_SUCCESS 0
#define EXIT_FAILURE 1
#define RAND_MAX 2147483647
void *malloc(size_t size);
void *calloc(size_t nmemb, size_t size);
void *realloc(void *ptr, size_t size);
void free(void *ptr);
[[noreturn]] void abort(void);
[[noreturn]] void exit(int status);
[[noreturn]] void _Exit(int status);
int abs(int j);
long labs(long j);
long long llabs(long long j);
int atoi(const char *nptr);
long atol(const char *nptr);
long long atoll(const char *nptr);
long strtol(const char *restrict nptr, char **restrict endptr, int base);
unsigned long strtoul(const char *restrict nptr, char **restrict endptr, int base);
long long strtoll(const char *restrict nptr, char **restrict endptr, int base);
unsigned long long strtoull(const char *restrict nptr, char **restrict endptr, int base);
double strtod(const char *restrict nptr, char **restrict endptr);
float strtof(const char *restrict nptr, char **restrict endptr);
long double strtold(const char *restrict nptr, char **restrict endptr);
double atof(const char *nptr);
int rand(void);
void srand(unsigned int seed);
char *getenv(const char *name);
int atexit(void (*func)(void));
void qsort(void *base, size_t nmemb, size_t size, int (*compar)(const void *, const void *));
void *bsearch(const void *key, const void *base, size_t nmemb, size_t size, int (*compar)(const void *, const void *));
int system(const char *string);
typedef struct { int quot; int rem; } div_t;
typedef struct { long quot; long rem; } ldiv_t;
typedef struct { long long quot; long long rem; } lldiv_t;
div_t div(int numer, int denom);
ldiv_t ldiv(long numer, long denom);
lldiv_t lldiv(long long numer, long long denom);
#define MB_CUR_MAX ((size_t)4)
#define __STDC_VERSION_STDLIB_H__ 202311L
#endif
`,
	"locale.h": `#ifndef __CC_LOCALE_H
#define __CC_LOCALE_H
#include <stddef.h>
struct lconv {
	char *decimal_point;
	char *thousands_sep;
	char *grouping;
	char *int_curr_symbol;
	char *currency_symbol;
	char *mon_decimal_point;
	char *mon_thousands_sep;
	char *mon_grouping;
	char *positive_sign;
	char *negative_sign;
	char int_frac_digits;
	char frac_digits;
	char p_cs_precedes;
	char p_sep_by_space;
	char n_cs_precedes;
	char n_sep_by_space;
	char p_sign_posn;
	char n_sign_posn;
	char int_p_cs_precedes;
	char int_p_sep_by_space;
	char int_n_cs_precedes;
	char int_n_sep_by_space;
	char int_p_sign_posn;
	char int_n_sign_posn;
};
#define LC_CTYPE 0
#define LC_NUMERIC 1
#define LC_TIME 2
#define LC_COLLATE 3
#define LC_MONETARY 4
#define LC_ALL 6
char *setlocale(int category, const char *locale);
struct lconv *localeconv(void);
#endif
`,
	"signal.h": `#ifndef __CC_SIGNAL_H
#define __CC_SIGNAL_H
typedef int sig_atomic_t;
#define SIG_DFL ((void (*)(int))0)
#define SIG_IGN ((void (*)(int))1)
#define SIG_ERR ((void (*)(int))-1)
#define SIGINT 2
#define SIGILL 4
#define SIGABRT 6
#define SIGFPE 8
#define SIGSEGV 11
#define SIGTERM 15
void (*signal(int sig, void (*func)(int)))(int);
int raise(int sig);
#endif
`,
	"stdnoreturn.h": `#ifndef __CC_STDNORETURN_H
#define __CC_STDNORETURN_H
#define noreturn _Noreturn
#endif
`,
	"string.h": `#ifndef __CC_STRING_H
#define __CC_STRING_H
#include <stddef.h>
void *memcpy(void *restrict s1, const void *restrict s2, size_t n);
void *memccpy(void *restrict s1, const void *restrict s2, int c, size_t n);
void *memmove(void *s1, const void *s2, size_t n);
void *memset(void *s, int c, size_t n);
int memcmp(const void *s1, const void *s2, size_t n);
void *memchr(const void *s, int c, size_t n);
char *strcpy(char *restrict s1, const char *restrict s2);
char *strncpy(char *restrict s1, const char *restrict s2, size_t n);
char *strcat(char *restrict s1, const char *restrict s2);
char *strncat(char *restrict s1, const char *restrict s2, size_t n);
int strcmp(const char *s1, const char *s2);
int strncmp(const char *s1, const char *s2, size_t n);
size_t strlen(const char *s);
char *strchr(const char *s, int c);
char *strrchr(const char *s, int c);
char *strstr(const char *s1, const char *s2);
char *strdup(const char *s);
char *strndup(const char *s, size_t n);
size_t strspn(const char *s1, const char *s2);
size_t strcspn(const char *s1, const char *s2);
size_t strnlen(const char *s, size_t n);
char *strpbrk(const char *s1, const char *s2);
int strcoll(const char *s1, const char *s2);
char *strtok(char *restrict s1, const char *restrict s2);
char *strtok_r(char *restrict s, const char *restrict sep, char **restrict lasts);
char *strerror(int errnum);
void *memset_explicit(void *s, int c, size_t n);
#define __STDC_VERSION_STRING_H__ 202311L
#endif
`,
}

// Headers returns the names of the standard headers cmd/cc provides.
func Headers() []string {
	var names []string
	for name := range sysHeaders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
