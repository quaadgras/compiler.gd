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
#define ENOMSG 42
#define EIDRM 43
#define ENOSTR 60
#define ENODATA 61
#define ETIME 62
#define ENOSR 63
#define ENOLINK 67
#define EPROTO 71
#define EMULTIHOP 72
#define EBADMSG 74
#define EUSERS 87
#define EDESTADDRREQ 89
#define EMSGSIZE 90
#define EPROTOTYPE 91
#define ENOPROTOOPT 92
#define EPROTONOSUPPORT 93
#define ESOCKTNOSUPPORT 94
#define EPFNOSUPPORT 96
#define EAFNOSUPPORT 97
#define EADDRNOTAVAIL 99
#define ENETDOWN 100
#define ENETUNREACH 101
#define ENETRESET 102
#define ECONNABORTED 103
#define ENOBUFS 105
#define EISCONN 106
#define ENOTCONN 107
#define ESHUTDOWN 108
#define ETOOMANYREFS 109
#define EHOSTDOWN 112
#define EHOSTUNREACH 113
#define EALREADY 114
#define EINPROGRESS 115
#define ESTALE 116
#define EDQUOT 122
#define ECANCELED 125
#define EOWNERDEAD 130
#define ENOTRECOVERABLE 131
#define ENOTSUP EOPNOTSUPP
#define EWOULDBLOCK EAGAIN
#define EDEADLOCK EDEADLK
#endif
`,
	"inttypes.h": `#ifndef __CC_INTTYPES_H
#define __CC_INTTYPES_H
#include <stddef.h>
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
#define SCNb8 "hhb"
#define SCNb16 "hb"
#define SCNb32 "b"
#define SCNb64 "lb"
#define SCNbLEAST8 "hhb"
#define SCNbLEAST16 "hb"
#define SCNbLEAST32 "b"
#define SCNbLEAST64 "lb"
#define SCNbFAST8 "hhb"
#define SCNbFAST16 "lb"
#define SCNbFAST32 "lb"
#define SCNbFAST64 "lb"
#define SCNbMAX "lb"
#define SCNbPTR "lb"
#ifndef __STDC_VERSION_INTTYPES_H__
#define __STDC_VERSION_INTTYPES_H__ 202311L
#endif
intmax_t wcstoimax(const wchar_t *restrict nptr, wchar_t **restrict endptr, int base);
uintmax_t wcstoumax(const wchar_t *restrict nptr, wchar_t **restrict endptr, int base);
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
#ifndef INFINITY
#define INFINITY 1e10000f
#endif
#ifndef NAN
#define NAN (0.0f/0.0f)
#endif
#define FP_NAN 0
#define FP_INFINITE 1
#define FP_ZERO 2
#define FP_SUBNORMAL 3
#define FP_NORMAL 4
#define FP_ILOGB0 (-2147483647-1)
#define FP_ILOGBNAN (-2147483647-1)
#define MATH_ERRNO 1
#define MATH_ERREXCEPT 2
#ifdef __GD_FENV__
#define math_errhandling (MATH_ERRNO | MATH_ERREXCEPT)
#else
#define math_errhandling MATH_ERRNO
#endif
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
#define iseqsig(x, y) __cc_iseqsig((x), (y))
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
#define FP_INT_UPWARD 0
#define FP_INT_DOWNWARD 1
#define FP_INT_TOWARDZERO 2
#define FP_INT_TONEARESTFROMZERO 3
#define FP_INT_TONEAREST 4
#define FP_LLOGB0 (-__LONG_MAX__-1)
#define FP_LLOGBNAN (-__LONG_MAX__-1)
#define iszero(x) (fpclassify(x) == FP_ZERO)
#define issubnormal(x) (fpclassify(x) == FP_SUBNORMAL)
#define iscanonical(x) ((void)(x), 1)
#define issignaling(x) _Generic((x), float: __cc_issignalingf, default: __cc_issignaling)(x)
#define __STDC_VERSION_MATH_H__ 202311L
double acospi(double);
float acospif(float);
long double acospil(long double);
double asinpi(double);
float asinpif(float);
long double asinpil(long double);
double atanpi(double);
float atanpif(float);
long double atanpil(long double);
double atan2pi(double, double);
float atan2pif(float, float);
long double atan2pil(long double, long double);
double cospi(double);
float cospif(float);
long double cospil(long double);
double sinpi(double);
float sinpif(float);
long double sinpil(long double);
double tanpi(double);
float tanpif(float);
long double tanpil(long double);
double exp10(double);
float exp10f(float);
long double exp10l(long double);
double exp10m1(double);
float exp10m1f(float);
long double exp10m1l(long double);
double exp2m1(double);
float exp2m1f(float);
long double exp2m1l(long double);
double log10p1(double);
float log10p1f(float);
long double log10p1l(long double);
double logp1(double);
float logp1f(float);
long double logp1l(long double);
double log2p1(double);
float log2p1f(float);
long double log2p1l(long double);
long llogb(double);
long llogbf(float);
long llogbl(long double);
double compoundn(double, long long);
float compoundnf(float, long long);
long double compoundnl(long double, long long);
double pown(double, long long);
float pownf(float, long long);
long double pownl(long double, long long);
double powr(double, double);
float powrf(float, float);
long double powrl(long double, long double);
double rootn(double, long long);
float rootnf(float, long long);
long double rootnl(long double, long long);
double rsqrt(double);
float rsqrtf(float);
long double rsqrtl(long double);
double roundeven(double);
float roundevenf(float);
long double roundevenl(long double);
double fromfp(double, int, unsigned int);
float fromfpf(float, int, unsigned int);
long double fromfpl(long double, int, unsigned int);
double ufromfp(double, int, unsigned int);
float ufromfpf(float, int, unsigned int);
long double ufromfpl(long double, int, unsigned int);
double fromfpx(double, int, unsigned int);
float fromfpxf(float, int, unsigned int);
long double fromfpxl(long double, int, unsigned int);
double ufromfpx(double, int, unsigned int);
float ufromfpxf(float, int, unsigned int);
long double ufromfpxl(long double, int, unsigned int);
double remquo(double, double, int *);
float remquof(float, float, int *);
long double remquol(long double, long double, int *);
double nexttoward(double, long double);
float nexttowardf(float, long double);
long double nexttowardl(long double, long double);
double nextup(double);
float nextupf(float);
long double nextupl(long double);
double nextdown(double);
float nextdownf(float);
long double nextdownl(long double);
int canonicalize(double *, const double *);
int canonicalizef(float *, const float *);
int canonicalizel(long double *, const long double *);
double fmaximum(double, double);
float fmaximumf(float, float);
long double fmaximuml(long double, long double);
double fminimum(double, double);
float fminimumf(float, float);
long double fminimuml(long double, long double);
double fmaximum_mag(double, double);
float fmaximum_magf(float, float);
long double fmaximum_magl(long double, long double);
double fminimum_mag(double, double);
float fminimum_magf(float, float);
long double fminimum_magl(long double, long double);
double fmaximum_num(double, double);
float fmaximum_numf(float, float);
long double fmaximum_numl(long double, long double);
double fminimum_num(double, double);
float fminimum_numf(float, float);
long double fminimum_numl(long double, long double);
double fmaximum_mag_num(double, double);
float fmaximum_mag_numf(float, float);
long double fmaximum_mag_numl(long double, long double);
double fminimum_mag_num(double, double);
float fminimum_mag_numf(float, float);
long double fminimum_mag_numl(long double, long double);
double getpayload(const double *);
float getpayloadf(const float *);
long double getpayloadl(const long double *);
int setpayload(double *, double);
int setpayloadf(float *, float);
int setpayloadl(long double *, long double);
int setpayloadsig(double *, double);
int setpayloadsigf(float *, float);
int setpayloadsigl(long double *, long double);
int totalorder(const double *, const double *);
int totalorderf(const float *, const float *);
int totalorderl(const long double *, const long double *);
int totalordermag(const double *, const double *);
int totalordermagf(const float *, const float *);
int totalordermagl(const long double *, const long double *);
float fadd(double x, double y);
float faddl(long double x, long double y);
double daddl(long double x, long double y);
float fsub(double x, double y);
float fsubl(long double x, long double y);
double dsubl(long double x, long double y);
float fmul(double x, double y);
float fmull(long double x, long double y);
double dmull(long double x, long double y);
float fdiv(double x, double y);
float fdivl(long double x, long double y);
double ddivl(long double x, long double y);
float ffma(double x, double y, double z);
float ffmal(long double x, long double y, long double z);
double dfmal(long double x, long double y, long double z);
float fsqrt(double x);
float fsqrtl(long double x);
double dsqrtl(long double x);
int __cc_issignaling(double x);
int __cc_iseqsig(double x, double y);
int __cc_issignalingf(float x);
#endif
`,
	"time.h": `#ifndef __CC_TIME_H
#define __CC_TIME_H
#include <stddef.h>
#ifndef __CC_TIME_T
#define __CC_TIME_T
typedef long time_t;
typedef long clock_t;
#endif
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
#ifndef __CC_TIMESPEC
#define __CC_TIMESPEC
struct timespec {
	time_t tv_sec;
	long tv_nsec;
};
#endif
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
#ifndef __CC_CLOCKID_T
#define __CC_CLOCKID_T
typedef int clockid_t;
#endif
#define CLOCK_REALTIME 0
#define CLOCK_MONOTONIC 1
#define CLOCK_PROCESS_CPUTIME_ID 2
#define CLOCK_THREAD_CPUTIME_ID 3
int clock_gettime(clockid_t clock_id, struct timespec *tp);
int clock_getres(clockid_t clock_id, struct timespec *res);
int nanosleep(const struct timespec *rqtp, struct timespec *rmtp);
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
struct tm;
struct __cc_FILE;
wint_t fgetwc(struct __cc_FILE *stream);
wchar_t *fgetws(wchar_t *restrict s, int n, struct __cc_FILE *restrict stream);
wint_t fputwc(wchar_t c, struct __cc_FILE *stream);
int fputws(const wchar_t *restrict s, struct __cc_FILE *restrict stream);
int fwide(struct __cc_FILE *stream, int mode);
int fwprintf(struct __cc_FILE *restrict stream, const wchar_t *restrict format, ...);
int fwscanf(struct __cc_FILE *restrict stream, const wchar_t *restrict format, ...);
wint_t getwc(struct __cc_FILE *stream);
wint_t getwchar(void);
wint_t putwc(wchar_t c, struct __cc_FILE *stream);
wint_t putwchar(wchar_t c);
int swprintf(wchar_t *restrict s, size_t n, const wchar_t *restrict format, ...);
int swscanf(const wchar_t *restrict s, const wchar_t *restrict format, ...);
wint_t ungetwc(wint_t c, struct __cc_FILE *stream);
int vfwprintf(struct __cc_FILE *restrict stream, const wchar_t *restrict format, __builtin_va_list arg);
int vfwscanf(struct __cc_FILE *restrict stream, const wchar_t *restrict format, __builtin_va_list arg);
int vswprintf(wchar_t *restrict s, size_t n, const wchar_t *restrict format, __builtin_va_list arg);
int vswscanf(const wchar_t *restrict s, const wchar_t *restrict format, __builtin_va_list arg);
int vwprintf(const wchar_t *restrict format, __builtin_va_list arg);
int vwscanf(const wchar_t *restrict format, __builtin_va_list arg);
int wprintf(const wchar_t *restrict format, ...);
int wscanf(const wchar_t *restrict format, ...);
double wcstod(const wchar_t *restrict nptr, wchar_t **restrict endptr);
float wcstof(const wchar_t *restrict nptr, wchar_t **restrict endptr);
long double wcstold(const wchar_t *restrict nptr, wchar_t **restrict endptr);
long int wcstol(const wchar_t *restrict nptr, wchar_t **restrict endptr, int base);
long long int wcstoll(const wchar_t *restrict nptr, wchar_t **restrict endptr, int base);
unsigned long int wcstoul(const wchar_t *restrict nptr, wchar_t **restrict endptr, int base);
unsigned long long int wcstoull(const wchar_t *restrict nptr, wchar_t **restrict endptr, int base);
wchar_t *wcsncpy(wchar_t *restrict s1, const wchar_t *restrict s2, size_t n);
wchar_t *wmemcpy(wchar_t *restrict s1, const wchar_t *restrict s2, size_t n);
wchar_t *wmemmove(wchar_t *s1, const wchar_t *s2, size_t n);
wchar_t *wcsncat(wchar_t *restrict s1, const wchar_t *restrict s2, size_t n);
int wcscoll(const wchar_t *s1, const wchar_t *s2);
size_t wcsxfrm(wchar_t *restrict s1, const wchar_t *restrict s2, size_t n);
int wmemcmp(const wchar_t *s1, const wchar_t *s2, size_t n);
size_t wcscspn(const wchar_t *s1, const wchar_t *s2);
wchar_t *wcspbrk(const wchar_t *s1, const wchar_t *s2);
wchar_t *wcsrchr(const wchar_t *s, wchar_t c);
size_t wcsspn(const wchar_t *s1, const wchar_t *s2);
wchar_t *wcsstr(const wchar_t *s1, const wchar_t *s2);
wchar_t *wcstok(wchar_t *restrict s1, const wchar_t *restrict s2, wchar_t **restrict ptr);
wchar_t *wmemchr(const wchar_t *s, wchar_t c, size_t n);
wchar_t *wmemset(wchar_t *s, wchar_t c, size_t n);
size_t wcsftime(wchar_t *restrict s, size_t maxsize, const wchar_t *restrict format, const struct tm *restrict timeptr);
size_t mbsrtowcs(wchar_t *restrict dst, const char **restrict src, size_t len, mbstate_t *restrict ps);
size_t wcsrtombs(char *restrict dst, const wchar_t **restrict src, size_t len, mbstate_t *restrict ps);
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
#define __STDC_VERSION_ASSERT_H__ 202311L
`,
	"float.h": `#ifndef __CC_FLOAT_H
#define __CC_FLOAT_H
#define __STDC_VERSION_FLOAT_H__ 202311L
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
int __cc_flt_rounds(void);
#define FLT_ROUNDS (__cc_flt_rounds())
#define FLT_IS_IEC_60559 1
#define DBL_IS_IEC_60559 1
#define LDBL_IS_IEC_60559 1
#define FLT_NORM_MAX FLT_MAX
#define DBL_NORM_MAX DBL_MAX
#define LDBL_NORM_MAX LDBL_MAX
#ifndef INFINITY
#define INFINITY 1e10000f
#endif
#ifndef NAN
#define NAN (0.0f/0.0f)
#endif
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
#define __STDC_VERSION_LIMITS_H__ 202311L
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
#include <time.h>
typedef unsigned long pthread_t;
typedef struct { int __a; } pthread_attr_t;
typedef struct { long __m[5]; } pthread_mutex_t;
typedef struct { int __a; } pthread_mutexattr_t;
typedef struct { long __c[6]; } pthread_cond_t;
typedef struct { int __a; } pthread_condattr_t;
#define PTHREAD_MUTEX_INITIALIZER {{0}}
#define PTHREAD_COND_INITIALIZER {{0}}
typedef int pthread_once_t;
#define PTHREAD_ONCE_INIT 0
typedef unsigned int pthread_key_t;
#define PTHREAD_KEYS_MAX 1024
#define PTHREAD_DESTRUCTOR_ITERATIONS 4
#define PTHREAD_MUTEX_NORMAL 0
#define PTHREAD_MUTEX_RECURSIVE 1
#define PTHREAD_MUTEX_ERRORCHECK 2
#define PTHREAD_MUTEX_DEFAULT 0
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
int pthread_mutexattr_init(pthread_mutexattr_t *attr);
int pthread_mutexattr_destroy(pthread_mutexattr_t *attr);
int pthread_mutexattr_settype(pthread_mutexattr_t *attr, int type);
int pthread_mutexattr_gettype(const pthread_mutexattr_t *restrict attr, int *restrict type);
int pthread_cond_init(pthread_cond_t *restrict cond, const pthread_condattr_t *restrict attr);
int pthread_cond_destroy(pthread_cond_t *cond);
int pthread_cond_wait(pthread_cond_t *restrict cond, pthread_mutex_t *restrict mutex);
int pthread_cond_timedwait(pthread_cond_t *restrict cond, pthread_mutex_t *restrict mutex, const struct timespec *restrict abstime);
int pthread_mutex_timedlock(pthread_mutex_t *restrict mutex, const struct timespec *restrict abstime);
int pthread_cond_signal(pthread_cond_t *cond);
int pthread_cond_broadcast(pthread_cond_t *cond);
int pthread_once(pthread_once_t *once_control, void (*init_routine)(void));
int pthread_key_create(pthread_key_t *key, void (*destructor)(void *));
int pthread_key_delete(pthread_key_t key);
void *pthread_getspecific(pthread_key_t key);
int pthread_setspecific(pthread_key_t key, const void *value);
int sched_yield(void);
#endif
`,
	"threads.h": `#ifndef __CC_THREADS_H
#define __CC_THREADS_H
#include <time.h>
typedef unsigned long thrd_t;
typedef int (*thrd_start_t)(void *);
typedef struct { long __m[5]; } mtx_t;
typedef struct { long __c[6]; } cnd_t;
typedef unsigned int tss_t;
typedef void (*tss_dtor_t)(void *);
typedef int once_flag;
#define ONCE_FLAG_INIT 0
#define TSS_DTOR_ITERATIONS 4
enum { thrd_success = 0, thrd_busy = 1, thrd_error = 2, thrd_nomem = 3, thrd_timedout = 4 };
enum { mtx_plain = 0, mtx_recursive = 1, mtx_timed = 2 };
void call_once(once_flag *flag, void (*func)(void));
int cnd_broadcast(cnd_t *cond);
void cnd_destroy(cnd_t *cond);
int cnd_init(cnd_t *cond);
int cnd_signal(cnd_t *cond);
int cnd_timedwait(cnd_t *restrict cond, mtx_t *restrict mtx, const struct timespec *restrict ts);
int cnd_wait(cnd_t *cond, mtx_t *mtx);
void mtx_destroy(mtx_t *mtx);
int mtx_init(mtx_t *mtx, int type);
int mtx_lock(mtx_t *mtx);
int mtx_timedlock(mtx_t *restrict mtx, const struct timespec *restrict ts);
int mtx_trylock(mtx_t *mtx);
int mtx_unlock(mtx_t *mtx);
int thrd_create(thrd_t *thr, thrd_start_t func, void *arg);
thrd_t thrd_current(void);
int thrd_detach(thrd_t thr);
int thrd_equal(thrd_t thr0, thrd_t thr1);
[[noreturn]] void thrd_exit(int res);
int thrd_join(thrd_t thr, int *res);
int thrd_sleep(const struct timespec *duration, struct timespec *remaining);
void thrd_yield(void);
int tss_create(tss_t *key, tss_dtor_t dtor);
void tss_delete(tss_t key);
void *tss_get(tss_t key);
int tss_set(tss_t key, void *val);
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
#define atomic_is_lock_free(obj) (sizeof(*(obj)) <= 8)
void atomic_thread_fence(memory_order order);
void atomic_signal_fence(memory_order order);
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
bool atomic_flag_test_and_set(volatile atomic_flag *object);
bool atomic_flag_test_and_set_explicit(volatile atomic_flag *object, memory_order order);
void atomic_flag_clear(volatile atomic_flag *object);
void atomic_flag_clear_explicit(volatile atomic_flag *object, memory_order order);
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
typedef _Atomic unsigned char atomic_char8_t;
typedef _Atomic int_least8_t atomic_int_least8_t;
typedef _Atomic uint_least8_t atomic_uint_least8_t;
typedef _Atomic int_least16_t atomic_int_least16_t;
typedef _Atomic uint_least16_t atomic_uint_least16_t;
typedef _Atomic int_least32_t atomic_int_least32_t;
typedef _Atomic uint_least32_t atomic_uint_least32_t;
typedef _Atomic int_least64_t atomic_int_least64_t;
typedef _Atomic uint_least64_t atomic_uint_least64_t;
typedef _Atomic int_fast8_t atomic_int_fast8_t;
typedef _Atomic uint_fast8_t atomic_uint_fast8_t;
typedef _Atomic int_fast16_t atomic_int_fast16_t;
typedef _Atomic uint_fast16_t atomic_uint_fast16_t;
typedef _Atomic int_fast32_t atomic_int_fast32_t;
typedef _Atomic uint_fast32_t atomic_uint_fast32_t;
typedef _Atomic int_fast64_t atomic_int_fast64_t;
typedef _Atomic uint_fast64_t atomic_uint_fast64_t;
#define ATOMIC_CHAR8_T_LOCK_FREE 2
#define __STDC_VERSION_STDATOMIC_H__ 202311L
#endif
`,
	"stdbit.h": `#ifndef __CC_STDBIT_H
#define __CC_STDBIT_H
#include <stddef.h>
#include <stdint.h>
#define __STDC_VERSION_STDBIT_H__ 202311L
#define __STDC_ENDIAN_LITTLE__ 1234
#define __STDC_ENDIAN_BIG__ 4321
#define __STDC_ENDIAN_NATIVE__ __STDC_ENDIAN_LITTLE__
unsigned int stdc_leading_zeros_uc(unsigned char __value);
unsigned int stdc_leading_zeros_us(unsigned short __value);
unsigned int stdc_leading_zeros_ui(unsigned int __value);
unsigned int stdc_leading_zeros_ul(unsigned long __value);
unsigned int stdc_leading_zeros_ull(unsigned long long __value);
#define stdc_leading_zeros(__value) _Generic((__value), unsigned char: stdc_leading_zeros_uc, unsigned short: stdc_leading_zeros_us, unsigned int: stdc_leading_zeros_ui, unsigned long: stdc_leading_zeros_ul, unsigned long long: stdc_leading_zeros_ull)(__value)
unsigned int stdc_leading_ones_uc(unsigned char __value);
unsigned int stdc_leading_ones_us(unsigned short __value);
unsigned int stdc_leading_ones_ui(unsigned int __value);
unsigned int stdc_leading_ones_ul(unsigned long __value);
unsigned int stdc_leading_ones_ull(unsigned long long __value);
#define stdc_leading_ones(__value) _Generic((__value), unsigned char: stdc_leading_ones_uc, unsigned short: stdc_leading_ones_us, unsigned int: stdc_leading_ones_ui, unsigned long: stdc_leading_ones_ul, unsigned long long: stdc_leading_ones_ull)(__value)
unsigned int stdc_trailing_zeros_uc(unsigned char __value);
unsigned int stdc_trailing_zeros_us(unsigned short __value);
unsigned int stdc_trailing_zeros_ui(unsigned int __value);
unsigned int stdc_trailing_zeros_ul(unsigned long __value);
unsigned int stdc_trailing_zeros_ull(unsigned long long __value);
#define stdc_trailing_zeros(__value) _Generic((__value), unsigned char: stdc_trailing_zeros_uc, unsigned short: stdc_trailing_zeros_us, unsigned int: stdc_trailing_zeros_ui, unsigned long: stdc_trailing_zeros_ul, unsigned long long: stdc_trailing_zeros_ull)(__value)
unsigned int stdc_trailing_ones_uc(unsigned char __value);
unsigned int stdc_trailing_ones_us(unsigned short __value);
unsigned int stdc_trailing_ones_ui(unsigned int __value);
unsigned int stdc_trailing_ones_ul(unsigned long __value);
unsigned int stdc_trailing_ones_ull(unsigned long long __value);
#define stdc_trailing_ones(__value) _Generic((__value), unsigned char: stdc_trailing_ones_uc, unsigned short: stdc_trailing_ones_us, unsigned int: stdc_trailing_ones_ui, unsigned long: stdc_trailing_ones_ul, unsigned long long: stdc_trailing_ones_ull)(__value)
unsigned int stdc_first_leading_zero_uc(unsigned char __value);
unsigned int stdc_first_leading_zero_us(unsigned short __value);
unsigned int stdc_first_leading_zero_ui(unsigned int __value);
unsigned int stdc_first_leading_zero_ul(unsigned long __value);
unsigned int stdc_first_leading_zero_ull(unsigned long long __value);
#define stdc_first_leading_zero(__value) _Generic((__value), unsigned char: stdc_first_leading_zero_uc, unsigned short: stdc_first_leading_zero_us, unsigned int: stdc_first_leading_zero_ui, unsigned long: stdc_first_leading_zero_ul, unsigned long long: stdc_first_leading_zero_ull)(__value)
unsigned int stdc_first_leading_one_uc(unsigned char __value);
unsigned int stdc_first_leading_one_us(unsigned short __value);
unsigned int stdc_first_leading_one_ui(unsigned int __value);
unsigned int stdc_first_leading_one_ul(unsigned long __value);
unsigned int stdc_first_leading_one_ull(unsigned long long __value);
#define stdc_first_leading_one(__value) _Generic((__value), unsigned char: stdc_first_leading_one_uc, unsigned short: stdc_first_leading_one_us, unsigned int: stdc_first_leading_one_ui, unsigned long: stdc_first_leading_one_ul, unsigned long long: stdc_first_leading_one_ull)(__value)
unsigned int stdc_first_trailing_zero_uc(unsigned char __value);
unsigned int stdc_first_trailing_zero_us(unsigned short __value);
unsigned int stdc_first_trailing_zero_ui(unsigned int __value);
unsigned int stdc_first_trailing_zero_ul(unsigned long __value);
unsigned int stdc_first_trailing_zero_ull(unsigned long long __value);
#define stdc_first_trailing_zero(__value) _Generic((__value), unsigned char: stdc_first_trailing_zero_uc, unsigned short: stdc_first_trailing_zero_us, unsigned int: stdc_first_trailing_zero_ui, unsigned long: stdc_first_trailing_zero_ul, unsigned long long: stdc_first_trailing_zero_ull)(__value)
unsigned int stdc_first_trailing_one_uc(unsigned char __value);
unsigned int stdc_first_trailing_one_us(unsigned short __value);
unsigned int stdc_first_trailing_one_ui(unsigned int __value);
unsigned int stdc_first_trailing_one_ul(unsigned long __value);
unsigned int stdc_first_trailing_one_ull(unsigned long long __value);
#define stdc_first_trailing_one(__value) _Generic((__value), unsigned char: stdc_first_trailing_one_uc, unsigned short: stdc_first_trailing_one_us, unsigned int: stdc_first_trailing_one_ui, unsigned long: stdc_first_trailing_one_ul, unsigned long long: stdc_first_trailing_one_ull)(__value)
unsigned int stdc_count_zeros_uc(unsigned char __value);
unsigned int stdc_count_zeros_us(unsigned short __value);
unsigned int stdc_count_zeros_ui(unsigned int __value);
unsigned int stdc_count_zeros_ul(unsigned long __value);
unsigned int stdc_count_zeros_ull(unsigned long long __value);
#define stdc_count_zeros(__value) _Generic((__value), unsigned char: stdc_count_zeros_uc, unsigned short: stdc_count_zeros_us, unsigned int: stdc_count_zeros_ui, unsigned long: stdc_count_zeros_ul, unsigned long long: stdc_count_zeros_ull)(__value)
unsigned int stdc_count_ones_uc(unsigned char __value);
unsigned int stdc_count_ones_us(unsigned short __value);
unsigned int stdc_count_ones_ui(unsigned int __value);
unsigned int stdc_count_ones_ul(unsigned long __value);
unsigned int stdc_count_ones_ull(unsigned long long __value);
#define stdc_count_ones(__value) _Generic((__value), unsigned char: stdc_count_ones_uc, unsigned short: stdc_count_ones_us, unsigned int: stdc_count_ones_ui, unsigned long: stdc_count_ones_ul, unsigned long long: stdc_count_ones_ull)(__value)
bool stdc_has_single_bit_uc(unsigned char __value);
bool stdc_has_single_bit_us(unsigned short __value);
bool stdc_has_single_bit_ui(unsigned int __value);
bool stdc_has_single_bit_ul(unsigned long __value);
bool stdc_has_single_bit_ull(unsigned long long __value);
#define stdc_has_single_bit(__value) _Generic((__value), unsigned char: stdc_has_single_bit_uc, unsigned short: stdc_has_single_bit_us, unsigned int: stdc_has_single_bit_ui, unsigned long: stdc_has_single_bit_ul, unsigned long long: stdc_has_single_bit_ull)(__value)
unsigned int stdc_bit_width_uc(unsigned char __value);
unsigned int stdc_bit_width_us(unsigned short __value);
unsigned int stdc_bit_width_ui(unsigned int __value);
unsigned int stdc_bit_width_ul(unsigned long __value);
unsigned int stdc_bit_width_ull(unsigned long long __value);
#define stdc_bit_width(__value) _Generic((__value), unsigned char: stdc_bit_width_uc, unsigned short: stdc_bit_width_us, unsigned int: stdc_bit_width_ui, unsigned long: stdc_bit_width_ul, unsigned long long: stdc_bit_width_ull)(__value)
unsigned char stdc_bit_floor_uc(unsigned char __value);
unsigned short stdc_bit_floor_us(unsigned short __value);
unsigned int stdc_bit_floor_ui(unsigned int __value);
unsigned long stdc_bit_floor_ul(unsigned long __value);
unsigned long long stdc_bit_floor_ull(unsigned long long __value);
#define stdc_bit_floor(__value) _Generic((__value), unsigned char: stdc_bit_floor_uc, unsigned short: stdc_bit_floor_us, unsigned int: stdc_bit_floor_ui, unsigned long: stdc_bit_floor_ul, unsigned long long: stdc_bit_floor_ull)(__value)
unsigned char stdc_bit_ceil_uc(unsigned char __value);
unsigned short stdc_bit_ceil_us(unsigned short __value);
unsigned int stdc_bit_ceil_ui(unsigned int __value);
unsigned long stdc_bit_ceil_ul(unsigned long __value);
unsigned long long stdc_bit_ceil_ull(unsigned long long __value);
#define stdc_bit_ceil(__value) _Generic((__value), unsigned char: stdc_bit_ceil_uc, unsigned short: stdc_bit_ceil_us, unsigned int: stdc_bit_ceil_ui, unsigned long: stdc_bit_ceil_ul, unsigned long long: stdc_bit_ceil_ull)(__value)
#endif
`,
	"wctype.h": `#ifndef __CC_WCTYPE_H
#define __CC_WCTYPE_H
typedef unsigned int wint_t;
typedef unsigned int wctrans_t;
typedef unsigned int wctype_t;
#ifndef WEOF
#define WEOF (0xffffffffu)
#endif
int iswalnum(wint_t wc);
int iswalpha(wint_t wc);
int iswblank(wint_t wc);
int iswcntrl(wint_t wc);
int iswdigit(wint_t wc);
int iswgraph(wint_t wc);
int iswlower(wint_t wc);
int iswprint(wint_t wc);
int iswpunct(wint_t wc);
int iswspace(wint_t wc);
int iswupper(wint_t wc);
int iswxdigit(wint_t wc);
int iswctype(wint_t wc, wctype_t desc);
wctype_t wctype(const char *property);
wint_t towlower(wint_t wc);
wint_t towupper(wint_t wc);
wint_t towctrans(wint_t wc, wctrans_t desc);
wctrans_t wctrans(const char *property);
#endif
`,
	"fenv.h": `#ifndef __CC_FENV_H
#define __CC_FENV_H
typedef struct { int __env; } fenv_t;
typedef int fexcept_t;
typedef struct { int __mode; } femode_t;
#define FE_TONEAREST 0
#ifdef __GD_FENV__
/* The hardware's directions and exceptions, numbered as x86's. */
#define FE_DOWNWARD 0x400
#define FE_UPWARD 0x800
#define FE_TOWARDZERO 0xc00
#define FE_INVALID 0x01
#define FE_DIVBYZERO 0x04
#define FE_OVERFLOW 0x08
#define FE_UNDERFLOW 0x10
#define FE_INEXACT 0x20
#define FE_ALL_EXCEPT 0x3d
#else
/* No other direction, no exceptions: wasm has none. */
#define FE_ALL_EXCEPT 0
#endif
const void *__cc_fe_dfl(void);
#define FE_DFL_ENV ((const fenv_t *)__cc_fe_dfl())
#define FE_DFL_MODE ((const femode_t *)__cc_fe_dfl())
int feclearexcept(int excepts);
int fegetexceptflag(fexcept_t *flagp, int excepts);
int feraiseexcept(int excepts);
int fesetexcept(int excepts);
int fesetexceptflag(const fexcept_t *flagp, int excepts);
int fetestexceptflag(const fexcept_t *flagp, int excepts);
int fetestexcept(int excepts);
int fegetmode(femode_t *modep);
int fegetround(void);
int fesetmode(const femode_t *modep);
int fesetround(int rnd);
int fegetenv(fenv_t *envp);
int feholdexcept(fenv_t *envp);
int fesetenv(const fenv_t *envp);
int feupdateenv(const fenv_t *envp);
#define __STDC_VERSION_FENV_H__ 202311L
#endif
`,
	"tgmath.h": `#ifndef __CC_TGMATH_H
#define __CC_TGMATH_H
#include <math.h>
#include <complex.h>
#define __STDC_VERSION_TGMATH_H__ 202311L
/* The type of the generic arguments: an integer is double. */
#define __TG_T(x) _Generic((x), float: 0.0f, long double: 0.0L, float _Complex: (float _Complex)0, double _Complex: (double _Complex)0, long double _Complex: (long double _Complex)0, default: 0.0)
#define acos(__x0) _Generic(__TG_T(__x0), float: acosf, long double: acosl, float _Complex: cacosf, double _Complex: cacos, long double _Complex: cacosl, default: acos)(__x0)
#define asin(__x0) _Generic(__TG_T(__x0), float: asinf, long double: asinl, float _Complex: casinf, double _Complex: casin, long double _Complex: casinl, default: asin)(__x0)
#define atan(__x0) _Generic(__TG_T(__x0), float: atanf, long double: atanl, float _Complex: catanf, double _Complex: catan, long double _Complex: catanl, default: atan)(__x0)
#define acosh(__x0) _Generic(__TG_T(__x0), float: acoshf, long double: acoshl, float _Complex: cacoshf, double _Complex: cacosh, long double _Complex: cacoshl, default: acosh)(__x0)
#define asinh(__x0) _Generic(__TG_T(__x0), float: asinhf, long double: asinhl, float _Complex: casinhf, double _Complex: casinh, long double _Complex: casinhl, default: asinh)(__x0)
#define atanh(__x0) _Generic(__TG_T(__x0), float: atanhf, long double: atanhl, float _Complex: catanhf, double _Complex: catanh, long double _Complex: catanhl, default: atanh)(__x0)
#define cos(__x0) _Generic(__TG_T(__x0), float: cosf, long double: cosl, float _Complex: ccosf, double _Complex: ccos, long double _Complex: ccosl, default: cos)(__x0)
#define sin(__x0) _Generic(__TG_T(__x0), float: sinf, long double: sinl, float _Complex: csinf, double _Complex: csin, long double _Complex: csinl, default: sin)(__x0)
#define tan(__x0) _Generic(__TG_T(__x0), float: tanf, long double: tanl, float _Complex: ctanf, double _Complex: ctan, long double _Complex: ctanl, default: tan)(__x0)
#define cosh(__x0) _Generic(__TG_T(__x0), float: coshf, long double: coshl, float _Complex: ccoshf, double _Complex: ccosh, long double _Complex: ccoshl, default: cosh)(__x0)
#define sinh(__x0) _Generic(__TG_T(__x0), float: sinhf, long double: sinhl, float _Complex: csinhf, double _Complex: csinh, long double _Complex: csinhl, default: sinh)(__x0)
#define tanh(__x0) _Generic(__TG_T(__x0), float: tanhf, long double: tanhl, float _Complex: ctanhf, double _Complex: ctanh, long double _Complex: ctanhl, default: tanh)(__x0)
#define exp(__x0) _Generic(__TG_T(__x0), float: expf, long double: expl, float _Complex: cexpf, double _Complex: cexp, long double _Complex: cexpl, default: exp)(__x0)
#define log(__x0) _Generic(__TG_T(__x0), float: logf, long double: logl, float _Complex: clogf, double _Complex: clog, long double _Complex: clogl, default: log)(__x0)
#define pow(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: powf, long double: powl, float _Complex: cpowf, double _Complex: cpow, long double _Complex: cpowl, default: pow)(__x0, __x1)
#define sqrt(__x0) _Generic(__TG_T(__x0), float: sqrtf, long double: sqrtl, float _Complex: csqrtf, double _Complex: csqrt, long double _Complex: csqrtl, default: sqrt)(__x0)
#define fabs(__x0) _Generic(__TG_T(__x0), float: fabsf, long double: fabsl, float _Complex: cabsf, double _Complex: cabs, long double _Complex: cabsl, default: fabs)(__x0)
#define atan2(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: atan2f, long double: atan2l, default: atan2)(__x0, __x1)
#define cbrt(__x0) _Generic(__TG_T(__x0), float: cbrtf, long double: cbrtl, default: cbrt)(__x0)
#define ceil(__x0) _Generic(__TG_T(__x0), float: ceilf, long double: ceill, default: ceil)(__x0)
#define copysign(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: copysignf, long double: copysignl, default: copysign)(__x0, __x1)
#define erf(__x0) _Generic(__TG_T(__x0), float: erff, long double: erfl, default: erf)(__x0)
#define erfc(__x0) _Generic(__TG_T(__x0), float: erfcf, long double: erfcl, default: erfc)(__x0)
#define exp2(__x0) _Generic(__TG_T(__x0), float: exp2f, long double: exp2l, default: exp2)(__x0)
#define expm1(__x0) _Generic(__TG_T(__x0), float: expm1f, long double: expm1l, default: expm1)(__x0)
#define fdim(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fdimf, long double: fdiml, default: fdim)(__x0, __x1)
#define floor(__x0) _Generic(__TG_T(__x0), float: floorf, long double: floorl, default: floor)(__x0)
#define fma(__x0, __x1, __x2) _Generic(__TG_T(__x0) + __TG_T(__x1) + __TG_T(__x2), float: fmaf, long double: fmal, default: fma)(__x0, __x1, __x2)
#define fmax(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fmaxf, long double: fmaxl, default: fmax)(__x0, __x1)
#define fmin(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fminf, long double: fminl, default: fmin)(__x0, __x1)
#define fmod(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fmodf, long double: fmodl, default: fmod)(__x0, __x1)
#define frexp(__x0, __x1) _Generic(__TG_T(__x0), float: frexpf, long double: frexpl, default: frexp)(__x0, __x1)
#define hypot(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: hypotf, long double: hypotl, default: hypot)(__x0, __x1)
#define ilogb(__x0) _Generic(__TG_T(__x0), float: ilogbf, long double: ilogbl, default: ilogb)(__x0)
#define ldexp(__x0, __x1) _Generic(__TG_T(__x0), float: ldexpf, long double: ldexpl, default: ldexp)(__x0, __x1)
#define lgamma(__x0) _Generic(__TG_T(__x0), float: lgammaf, long double: lgammal, default: lgamma)(__x0)
#define llrint(__x0) _Generic(__TG_T(__x0), float: llrintf, long double: llrintl, default: llrint)(__x0)
#define llround(__x0) _Generic(__TG_T(__x0), float: llroundf, long double: llroundl, default: llround)(__x0)
#define log10(__x0) _Generic(__TG_T(__x0), float: log10f, long double: log10l, default: log10)(__x0)
#define log1p(__x0) _Generic(__TG_T(__x0), float: log1pf, long double: log1pl, default: log1p)(__x0)
#define log2(__x0) _Generic(__TG_T(__x0), float: log2f, long double: log2l, default: log2)(__x0)
#define logb(__x0) _Generic(__TG_T(__x0), float: logbf, long double: logbl, default: logb)(__x0)
#define lrint(__x0) _Generic(__TG_T(__x0), float: lrintf, long double: lrintl, default: lrint)(__x0)
#define lround(__x0) _Generic(__TG_T(__x0), float: lroundf, long double: lroundl, default: lround)(__x0)
#define nearbyint(__x0) _Generic(__TG_T(__x0), float: nearbyintf, long double: nearbyintl, default: nearbyint)(__x0)
#define nextafter(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: nextafterf, long double: nextafterl, default: nextafter)(__x0, __x1)
#define nexttoward(__x0, __x1) _Generic(__TG_T(__x0), float: nexttowardf, long double: nexttowardl, default: nexttoward)(__x0, __x1)
#define remainder(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: remainderf, long double: remainderl, default: remainder)(__x0, __x1)
#define remquo(__x0, __x1, __x2) _Generic(__TG_T(__x0) + __TG_T(__x1), float: remquof, long double: remquol, default: remquo)(__x0, __x1, __x2)
#define rint(__x0) _Generic(__TG_T(__x0), float: rintf, long double: rintl, default: rint)(__x0)
#define round(__x0) _Generic(__TG_T(__x0), float: roundf, long double: roundl, default: round)(__x0)
#define scalbn(__x0, __x1) _Generic(__TG_T(__x0), float: scalbnf, long double: scalbnl, default: scalbn)(__x0, __x1)
#define scalbln(__x0, __x1) _Generic(__TG_T(__x0), float: scalblnf, long double: scalblnl, default: scalbln)(__x0, __x1)
#define tgamma(__x0) _Generic(__TG_T(__x0), float: tgammaf, long double: tgammal, default: tgamma)(__x0)
#define trunc(__x0) _Generic(__TG_T(__x0), float: truncf, long double: truncl, default: trunc)(__x0)
#define acospi(__x0) _Generic(__TG_T(__x0), float: acospif, long double: acospil, default: acospi)(__x0)
#define asinpi(__x0) _Generic(__TG_T(__x0), float: asinpif, long double: asinpil, default: asinpi)(__x0)
#define atan2pi(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: atan2pif, long double: atan2pil, default: atan2pi)(__x0, __x1)
#define atanpi(__x0) _Generic(__TG_T(__x0), float: atanpif, long double: atanpil, default: atanpi)(__x0)
#define compoundn(__x0, __x1) _Generic(__TG_T(__x0), float: compoundnf, long double: compoundnl, default: compoundn)(__x0, __x1)
#define cospi(__x0) _Generic(__TG_T(__x0), float: cospif, long double: cospil, default: cospi)(__x0)
#define exp10m1(__x0) _Generic(__TG_T(__x0), float: exp10m1f, long double: exp10m1l, default: exp10m1)(__x0)
#define exp10(__x0) _Generic(__TG_T(__x0), float: exp10f, long double: exp10l, default: exp10)(__x0)
#define exp2m1(__x0) _Generic(__TG_T(__x0), float: exp2m1f, long double: exp2m1l, default: exp2m1)(__x0)
#define fmaximum(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fmaximumf, long double: fmaximuml, default: fmaximum)(__x0, __x1)
#define fmaximum_mag(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fmaximum_magf, long double: fmaximum_magl, default: fmaximum_mag)(__x0, __x1)
#define fmaximum_num(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fmaximum_numf, long double: fmaximum_numl, default: fmaximum_num)(__x0, __x1)
#define fmaximum_mag_num(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fmaximum_mag_numf, long double: fmaximum_mag_numl, default: fmaximum_mag_num)(__x0, __x1)
#define fminimum(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fminimumf, long double: fminimuml, default: fminimum)(__x0, __x1)
#define fminimum_mag(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fminimum_magf, long double: fminimum_magl, default: fminimum_mag)(__x0, __x1)
#define fminimum_num(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fminimum_numf, long double: fminimum_numl, default: fminimum_num)(__x0, __x1)
#define fminimum_mag_num(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: fminimum_mag_numf, long double: fminimum_mag_numl, default: fminimum_mag_num)(__x0, __x1)
#define fromfp(__x0, __x1, __x2) _Generic(__TG_T(__x0), float: fromfpf, long double: fromfpl, default: fromfp)(__x0, __x1, __x2)
#define fromfpx(__x0, __x1, __x2) _Generic(__TG_T(__x0), float: fromfpxf, long double: fromfpxl, default: fromfpx)(__x0, __x1, __x2)
#define llogb(__x0) _Generic(__TG_T(__x0), float: llogbf, long double: llogbl, default: llogb)(__x0)
#define log10p1(__x0) _Generic(__TG_T(__x0), float: log10p1f, long double: log10p1l, default: log10p1)(__x0)
#define log2p1(__x0) _Generic(__TG_T(__x0), float: log2p1f, long double: log2p1l, default: log2p1)(__x0)
#define logp1(__x0) _Generic(__TG_T(__x0), float: logp1f, long double: logp1l, default: logp1)(__x0)
#define nextdown(__x0) _Generic(__TG_T(__x0), float: nextdownf, long double: nextdownl, default: nextdown)(__x0)
#define nextup(__x0) _Generic(__TG_T(__x0), float: nextupf, long double: nextupl, default: nextup)(__x0)
#define pown(__x0, __x1) _Generic(__TG_T(__x0), float: pownf, long double: pownl, default: pown)(__x0, __x1)
#define powr(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), float: powrf, long double: powrl, default: powr)(__x0, __x1)
#define rootn(__x0, __x1) _Generic(__TG_T(__x0), float: rootnf, long double: rootnl, default: rootn)(__x0, __x1)
#define roundeven(__x0) _Generic(__TG_T(__x0), float: roundevenf, long double: roundevenl, default: roundeven)(__x0)
#define rsqrt(__x0) _Generic(__TG_T(__x0), float: rsqrtf, long double: rsqrtl, default: rsqrt)(__x0)
#define sinpi(__x0) _Generic(__TG_T(__x0), float: sinpif, long double: sinpil, default: sinpi)(__x0)
#define tanpi(__x0) _Generic(__TG_T(__x0), float: tanpif, long double: tanpil, default: tanpi)(__x0)
#define ufromfp(__x0, __x1, __x2) _Generic(__TG_T(__x0), float: ufromfpf, long double: ufromfpl, default: ufromfp)(__x0, __x1, __x2)
#define ufromfpx(__x0, __x1, __x2) _Generic(__TG_T(__x0), float: ufromfpxf, long double: ufromfpxl, default: ufromfpx)(__x0, __x1, __x2)
#define carg(__x0) _Generic(__TG_T(__x0), float _Complex: cargf, long double _Complex: cargl, float: cargf, long double: cargl, default: carg)(__x0)
#define cimag(__x0) _Generic(__TG_T(__x0), float _Complex: cimagf, long double _Complex: cimagl, float: cimagf, long double: cimagl, default: cimag)(__x0)
#define conj(__x0) _Generic(__TG_T(__x0), float _Complex: conjf, long double _Complex: conjl, float: conjf, long double: conjl, default: conj)(__x0)
#define cproj(__x0) _Generic(__TG_T(__x0), float _Complex: cprojf, long double _Complex: cprojl, float: cprojf, long double: cprojl, default: cproj)(__x0)
#define creal(__x0) _Generic(__TG_T(__x0), float _Complex: crealf, long double _Complex: creall, float: crealf, long double: creall, default: creal)(__x0)
#define fadd(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), long double: faddl, default: fadd)(__x0, __x1)
#define dadd(__x0, __x1) daddl(__x0, __x1)
#define fsub(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), long double: fsubl, default: fsub)(__x0, __x1)
#define dsub(__x0, __x1) dsubl(__x0, __x1)
#define fmul(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), long double: fmull, default: fmul)(__x0, __x1)
#define dmul(__x0, __x1) dmull(__x0, __x1)
#define fdiv(__x0, __x1) _Generic(__TG_T(__x0) + __TG_T(__x1), long double: fdivl, default: fdiv)(__x0, __x1)
#define ddiv(__x0, __x1) ddivl(__x0, __x1)
#define ffma(__x0, __x1, __x2) _Generic(__TG_T(__x0) + __TG_T(__x1) + __TG_T(__x2), long double: ffmal, default: ffma)(__x0, __x1, __x2)
#define dfma(__x0, __x1, __x2) dfmal(__x0, __x1, __x2)
#define fsqrt(__x0) _Generic(__TG_T(__x0), long double: fsqrtl, default: fsqrt)(__x0)
#define dsqrt(__x0) dsqrtl(__x0)
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
	"complex.h": `#ifndef __CC_COMPLEX_H
#define __CC_COMPLEX_H
#define __STDC_VERSION_COMPLEX_H__ 202311L
#define complex _Complex
#define _Complex_I (__extension__ 1.0iF)
#define I _Complex_I
#define CMPLX(x, y) __builtin_complex((double)(x), (double)(y))
#define CMPLXF(x, y) __builtin_complex((float)(x), (float)(y))
#define CMPLXL(x, y) __builtin_complex((long double)(x), (long double)(y))
double creal(double complex z);
double cimag(double complex z);
double complex conj(double complex z);
double cabs(double complex z);
double carg(double complex z);
double complex cproj(double complex z);
double complex cexp(double complex z);
double complex clog(double complex z);
double complex csqrt(double complex z);
double complex cpow(double complex x, double complex y);
double complex csin(double complex z);
double complex ccos(double complex z);
double complex ctan(double complex z);
double complex casin(double complex z);
double complex cacos(double complex z);
double complex catan(double complex z);
double complex csinh(double complex z);
double complex ccosh(double complex z);
double complex ctanh(double complex z);
double complex casinh(double complex z);
double complex cacosh(double complex z);
double complex catanh(double complex z);
float crealf(float complex z);
float cimagf(float complex z);
float complex conjf(float complex z);
float cabsf(float complex z);
float cargf(float complex z);
float complex cexpf(float complex z);
float complex clogf(float complex z);
float complex csqrtf(float complex z);
float complex cpowf(float complex x, float complex y);
float complex csinf(float complex z);
float complex ccosf(float complex z);
float complex ctanf(float complex z);
float complex casinf(float complex z);
float complex cacosf(float complex z);
float complex catanf(float complex z);
float complex csinhf(float complex z);
float complex ccoshf(float complex z);
float complex ctanhf(float complex z);
float complex casinhf(float complex z);
float complex cacoshf(float complex z);
float complex catanhf(float complex z);
float complex cprojf(float complex z);
long double creall(long double complex z);
long double cimagl(long double complex z);
long double complex conjl(long double complex z);
long double cabsl(long double complex z);
long double cargl(long double complex z);
long double complex cprojl(long double complex z);
long double complex cexpl(long double complex z);
long double complex clogl(long double complex z);
long double complex csqrtl(long double complex z);
long double complex cpowl(long double complex x, long double complex y);
long double complex csinl(long double complex z);
long double complex ccosl(long double complex z);
long double complex ctanl(long double complex z);
long double complex casinl(long double complex z);
long double complex cacosl(long double complex z);
long double complex catanl(long double complex z);
long double complex csinhl(long double complex z);
long double complex ccoshl(long double complex z);
long double complex ctanhl(long double complex z);
long double complex casinhl(long double complex z);
long double complex cacoshl(long double complex z);
long double complex catanhl(long double complex z);
#endif
`,
	"dlfcn.h": `#ifndef __CC_DLFCN_H
#define __CC_DLFCN_H
#define RTLD_LAZY 1
#define RTLD_NOW 2
#define RTLD_GLOBAL 0x100
#define RTLD_LOCAL 0
void *dlopen(const char *path, int flags);
void *dlsym(void *restrict handle, const char *restrict name);
int dlclose(void *handle);
char *dlerror(void);
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
/* Integers that carry a pointer's capability (CHERI C's __intcap). */
typedef __intcap intptr_t;
typedef unsigned __intcap uintptr_t;
typedef long intmax_t;
typedef unsigned long uintmax_t;
#define INT8_WIDTH 8
#define INT16_WIDTH 16
#define INT32_WIDTH 32
#define INT64_WIDTH 64
#define UINT8_WIDTH 8
#define UINT16_WIDTH 16
#define UINT32_WIDTH 32
#define UINT64_WIDTH 64
#define INT_LEAST8_WIDTH 8
#define INT_LEAST16_WIDTH 16
#define INT_LEAST32_WIDTH 32
#define INT_LEAST64_WIDTH 64
#define UINT_LEAST8_WIDTH 8
#define UINT_LEAST16_WIDTH 16
#define UINT_LEAST32_WIDTH 32
#define UINT_LEAST64_WIDTH 64
#define INT_FAST8_WIDTH 8
#define INT_FAST16_WIDTH 64
#define INT_FAST32_WIDTH 64
#define INT_FAST64_WIDTH 64
#define UINT_FAST8_WIDTH 8
#define UINT_FAST16_WIDTH 64
#define UINT_FAST32_WIDTH 64
#define UINT_FAST64_WIDTH 64
#define INTPTR_WIDTH 64
#define UINTPTR_WIDTH 64
#define INTMAX_WIDTH 64
#define UINTMAX_WIDTH 64
#define PTRDIFF_WIDTH 64
#define SIZE_WIDTH 64
#define SIG_ATOMIC_WIDTH 32
#define SIG_ATOMIC_MIN INT32_MIN
#define SIG_ATOMIC_MAX INT32_MAX
#define WCHAR_WIDTH 32
#define WINT_WIDTH 32
#define WINT_MIN 0U
#define WINT_MAX UINT32_MAX
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
int fileno(FILE *stream);
FILE *fdopen(int fd, const char *mode);
FILE *popen(const char *command, const char *mode);
int pclose(FILE *stream);
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
#define _PRINTF_NAN_LEN_MAX 4
typedef struct { long long __pos; } fpos_t;
int fgetpos(FILE *restrict stream, fpos_t *restrict pos);
int fsetpos(FILE *stream, const fpos_t *pos);
long getline(char **restrict lineptr, size_t *restrict n, FILE *restrict stream);
long getdelim(char **restrict lineptr, size_t *restrict n, int delim, FILE *restrict stream);
int asprintf(char **restrict strp, const char *restrict fmt, ...);
int vasprintf(char **restrict strp, const char *restrict fmt, va_list ap);
int dprintf(int fd, const char *restrict format, ...);
int vdprintf(int fd, const char *restrict format, va_list ap);
FILE *fmemopen(void *restrict buf, size_t size, const char *restrict mode);
FILE *open_memstream(char **bufp, size_t *sizep);
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
int at_quick_exit(void (*func)(void));
_Noreturn void quick_exit(int status);
typedef int once_flag;
#define ONCE_FLAG_INIT 0
void call_once(once_flag *flag, void (*func)(void));
int strfromd(char *restrict s, size_t n, const char *restrict format, double fp);
int strfromf(char *restrict s, size_t n, const char *restrict format, float fp);
int strfroml(char *restrict s, size_t n, const char *restrict format, long double fp);
void free_sized(void *ptr, size_t size);
void free_aligned_sized(void *ptr, size_t alignment, size_t size);
size_t memalignment(const void *p);
int mblen(const char *s, size_t n);
int mbtowc(wchar_t *restrict pwc, const char *restrict s, size_t n);
int wctomb(char *s, wchar_t wc);
size_t mbstowcs(wchar_t *restrict pwcs, const char *restrict s, size_t n);
size_t wcstombs(char *restrict s, const wchar_t *restrict pwcs, size_t n);
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
void *aligned_alloc(size_t alignment, size_t size);
int posix_memalign(void **memptr, size_t alignment, size_t size);
int setenv(const char *name, const char *value, int overwrite);
int unsetenv(const char *name);
int mkstemp(char *template);
char *realpath(const char *restrict path, char *restrict resolved);
void *reallocarray(void *ptr, size_t nmemb, size_t size);
void qsort_r(void *base, size_t nmemb, size_t size, int (*compar)(const void *, const void *, void *), void *arg);
char *mkdtemp(char *template);
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
char *stpcpy(char *restrict s1, const char *restrict s2);
char *stpncpy(char *restrict s1, const char *restrict s2, size_t n);
size_t strlcpy(char *restrict s1, const char *restrict s2, size_t n);
size_t strlcat(char *restrict s1, const char *restrict s2, size_t n);
#define __STDC_VERSION_STRING_H__ 202311L
size_t strxfrm(char *restrict s1, const char *restrict s2, size_t n);
char *strsep(char **restrict stringp, const char *restrict delim);
char *strsignal(int sig);
#endif
`,
	"sys/types.h": `#ifndef __CC_SYS_TYPES_H
#define __CC_SYS_TYPES_H
#include <stddef.h>
typedef long ssize_t;
typedef long off_t;
typedef int pid_t;
typedef unsigned int uid_t;
typedef unsigned int gid_t;
typedef unsigned int mode_t;
typedef unsigned long dev_t;
typedef unsigned long ino_t;
typedef unsigned long nlink_t;
typedef long blksize_t;
typedef long blkcnt_t;
typedef unsigned int useconds_t;
typedef long suseconds_t;
#ifndef __CC_CLOCKID_T
#define __CC_CLOCKID_T
typedef int clockid_t;
#endif
#ifndef __CC_TIME_T
#define __CC_TIME_T
typedef long time_t;
typedef long clock_t;
#endif
#endif
`,
	"unistd.h": `#ifndef __CC_UNISTD_H
#define __CC_UNISTD_H
#include <sys/types.h>
#define STDIN_FILENO 0
#define STDOUT_FILENO 1
#define STDERR_FILENO 2
#define SEEK_SET 0
#define SEEK_CUR 1
#define SEEK_END 2
#define F_OK 0
#define X_OK 1
#define W_OK 2
#define R_OK 4
#define _SC_CLK_TCK 2
#define _SC_PAGESIZE 30
#define _SC_PAGE_SIZE 30
#define _SC_NPROCESSORS_CONF 83
#define _SC_NPROCESSORS_ONLN 84
#define _POSIX_VERSION 200809L
ssize_t read(int fd, void *buf, size_t nbyte);
ssize_t write(int fd, const void *buf, size_t nbyte);
int close(int fd);
off_t lseek(int fd, off_t offset, int whence);
int unlink(const char *path);
int rmdir(const char *path);
int access(const char *path, int amode);
pid_t getpid(void);
pid_t getppid(void);
uid_t getuid(void);
uid_t geteuid(void);
gid_t getgid(void);
gid_t getegid(void);
int isatty(int fd);
char *getcwd(char *buf, size_t size);
int chdir(const char *path);
unsigned int sleep(unsigned int seconds);
int usleep(useconds_t usec);
[[noreturn]] void _exit(int status);
int dup(int fd);
int dup2(int fd, int fd2);
int pipe(int fds[2]);
long sysconf(int name);
int gethostname(char *name, size_t namelen);
int ftruncate(int fd, off_t length);
int truncate(const char *path, off_t length);
int fsync(int fd);
int fdatasync(int fd);
int chown(const char *path, uid_t owner, gid_t group);
int lchown(const char *path, uid_t owner, gid_t group);
int fchown(int fd, uid_t owner, gid_t group);
ssize_t readlink(const char *restrict path, char *restrict buf, size_t bufsize);
int symlink(const char *path1, const char *path2);
int link(const char *path1, const char *path2);
ssize_t pread(int fd, void *buf, size_t nbyte, off_t offset);
ssize_t pwrite(int fd, const void *buf, size_t nbyte, off_t offset);
int getpagesize(void);
int getopt(int argc, char *const argv[], const char *optstring);
char **__cc_optarg(void);
int *__cc_optind(void);
int *__cc_opterr(void);
int *__cc_optopt(void);
#define optarg (*__cc_optarg())
#define optind (*__cc_optind())
#define opterr (*__cc_opterr())
#define optopt (*__cc_optopt())
#endif
`,
	"fcntl.h": `#ifndef __CC_FCNTL_H
#define __CC_FCNTL_H
#include <sys/types.h>
#include <sys/stat.h>
#define O_RDONLY 0
#define O_WRONLY 1
#define O_RDWR 2
#define O_ACCMODE 3
#define O_CREAT 0100
#define O_EXCL 0200
#define O_NOCTTY 0400
#define O_TRUNC 01000
#define O_APPEND 02000
#define O_NONBLOCK 04000
#define O_NDELAY O_NONBLOCK
#define O_DSYNC 010000
#define O_LARGEFILE 0100000
#define O_DIRECTORY 0200000
#define O_NOFOLLOW 0400000
#define O_CLOEXEC 02000000
#define O_SYNC 04010000
#define F_DUPFD 0
#define F_GETFD 1
#define F_SETFD 2
#define F_GETFL 3
#define F_SETFL 4
#define F_DUPFD_CLOEXEC 1030
#define FD_CLOEXEC 1
#define F_GETLK 5
#define F_SETLK 6
#define F_SETLKW 7
#define F_RDLCK 0
#define F_WRLCK 1
#define F_UNLCK 2
struct flock {
	short l_type;
	short l_whence;
	off_t l_start;
	off_t l_len;
	pid_t l_pid;
};
#define SEEK_SET 0
#define SEEK_CUR 1
#define SEEK_END 2
int open(const char *path, int oflag, ...);
int creat(const char *path, mode_t mode);
int fcntl(int fd, int cmd, ...);
#endif
`,
	"sys/stat.h": `#ifndef __CC_SYS_STAT_H
#define __CC_SYS_STAT_H
#include <sys/types.h>
#ifndef __CC_TIMESPEC
#define __CC_TIMESPEC
struct timespec {
	time_t tv_sec;
	long tv_nsec;
};
#endif
struct stat {
	dev_t st_dev;
	ino_t st_ino;
	nlink_t st_nlink;
	mode_t st_mode;
	uid_t st_uid;
	gid_t st_gid;
	int __pad0;
	dev_t st_rdev;
	off_t st_size;
	blksize_t st_blksize;
	blkcnt_t st_blocks;
	struct timespec st_atim;
	struct timespec st_mtim;
	struct timespec st_ctim;
	long __unused[3];
};
#define st_atime st_atim.tv_sec
#define st_mtime st_mtim.tv_sec
#define st_ctime st_ctim.tv_sec
#define S_IFMT 0170000
#define S_IFSOCK 0140000
#define S_IFLNK 0120000
#define S_IFREG 0100000
#define S_IFBLK 0060000
#define S_IFDIR 0040000
#define S_IFCHR 0020000
#define S_IFIFO 0010000
#define S_ISUID 04000
#define S_ISGID 02000
#define S_ISVTX 01000
#define S_IRWXU 0700
#define S_IRUSR 0400
#define S_IWUSR 0200
#define S_IXUSR 0100
#define S_IRWXG 070
#define S_IRGRP 040
#define S_IWGRP 020
#define S_IXGRP 010
#define S_IRWXO 07
#define S_IROTH 04
#define S_IWOTH 02
#define S_IXOTH 01
#define S_ISDIR(m) (((m) & S_IFMT) == S_IFDIR)
#define S_ISCHR(m) (((m) & S_IFMT) == S_IFCHR)
#define S_ISBLK(m) (((m) & S_IFMT) == S_IFBLK)
#define S_ISREG(m) (((m) & S_IFMT) == S_IFREG)
#define S_ISFIFO(m) (((m) & S_IFMT) == S_IFIFO)
#define S_ISLNK(m) (((m) & S_IFMT) == S_IFLNK)
#define S_ISSOCK(m) (((m) & S_IFMT) == S_IFSOCK)
int stat(const char *restrict path, struct stat *restrict buf);
int lstat(const char *restrict path, struct stat *restrict buf);
int fstat(int fd, struct stat *buf);
int mkdir(const char *path, mode_t mode);
int chmod(const char *path, mode_t mode);
int fchmod(int fd, mode_t mode);
mode_t umask(mode_t cmask);
#endif
`,
	"dirent.h": `#ifndef __CC_DIRENT_H
#define __CC_DIRENT_H
#include <sys/types.h>
typedef struct __cc_DIR DIR;
struct dirent {
	ino_t d_ino;
	off_t d_off;
	unsigned short d_reclen;
	unsigned char d_type;
	char d_name[256];
};
#define DT_UNKNOWN 0
#define DT_FIFO 1
#define DT_CHR 2
#define DT_DIR 4
#define DT_BLK 6
#define DT_REG 8
#define DT_LNK 10
#define DT_SOCK 12
DIR *opendir(const char *dirname);
struct dirent *readdir(DIR *dirp);
int closedir(DIR *dirp);
void rewinddir(DIR *dirp);
#endif
`,
	"strings.h": `#ifndef __CC_STRINGS_H
#define __CC_STRINGS_H
#include <stddef.h>
int strcasecmp(const char *s1, const char *s2);
int strncasecmp(const char *s1, const char *s2, size_t n);
void bzero(void *s, size_t n);
void bcopy(const void *s1, void *s2, size_t n);
int ffs(int i);
int ffsl(long i);
int ffsll(long long i);
char *index(const char *s, int c);
char *rindex(const char *s, int c);
#endif
`,
	"sys/time.h": `#ifndef __CC_SYS_TIME_H
#define __CC_SYS_TIME_H
#include <sys/types.h>
struct timeval {
	time_t tv_sec;
	suseconds_t tv_usec;
};
struct timezone {
	int tz_minuteswest;
	int tz_dsttime;
};
int gettimeofday(struct timeval *restrict tv, void *restrict tz);
int utimes(const char *path, const struct timeval times[2]);
#define timerisset(t) ((t)->tv_sec || (t)->tv_usec)
#define timerclear(t) ((t)->tv_sec = (t)->tv_usec = 0)
#define timercmp(a, b, CMP) ((a)->tv_sec == (b)->tv_sec ? (a)->tv_usec CMP (b)->tv_usec : (a)->tv_sec CMP (b)->tv_sec)
#define timeradd(a, b, r) do { (r)->tv_sec = (a)->tv_sec + (b)->tv_sec; (r)->tv_usec = (a)->tv_usec + (b)->tv_usec; if ((r)->tv_usec >= 1000000) { (r)->tv_sec++; (r)->tv_usec -= 1000000; } } while (0)
#define timersub(a, b, r) do { (r)->tv_sec = (a)->tv_sec - (b)->tv_sec; (r)->tv_usec = (a)->tv_usec - (b)->tv_usec; if ((r)->tv_usec < 0) { (r)->tv_sec--; (r)->tv_usec += 1000000; } } while (0)
#include <sys/select.h>
#endif
`,
	"sys/ioctl.h": `#ifndef __CC_SYS_IOCTL_H
#define __CC_SYS_IOCTL_H
struct winsize {
	unsigned short ws_row;
	unsigned short ws_col;
	unsigned short ws_xpixel;
	unsigned short ws_ypixel;
};
#define TIOCGWINSZ 0x5413
#define FIONREAD 0x541B
#define FIONBIO 0x5421
#define FIONCLEX 0x5450
#define FIOCLEX 0x5451
int ioctl(int fd, unsigned long request, ...);
#endif
`,
	"pwd.h": `#ifndef __CC_PWD_H
#define __CC_PWD_H
#include <sys/types.h>
struct passwd {
	char *pw_name;
	char *pw_passwd;
	uid_t pw_uid;
	gid_t pw_gid;
	char *pw_gecos;
	char *pw_dir;
	char *pw_shell;
};
struct passwd *getpwuid(uid_t uid);
struct passwd *getpwnam(const char *name);
#endif
`,
	"sys/resource.h": `#ifndef __CC_SYS_RESOURCE_H
#define __CC_SYS_RESOURCE_H
#include <sys/time.h>
struct rusage {
	struct timeval ru_utime;
	struct timeval ru_stime;
	long ru_maxrss;
	long ru_ixrss;
	long ru_idrss;
	long ru_isrss;
	long ru_minflt;
	long ru_majflt;
	long ru_nswap;
	long ru_inblock;
	long ru_oublock;
	long ru_msgsnd;
	long ru_msgrcv;
	long ru_nsignals;
	long ru_nvcsw;
	long ru_nivcsw;
};
#define RUSAGE_SELF 0
#define RUSAGE_CHILDREN (-1)
#define RUSAGE_THREAD 1
int getrusage(int who, struct rusage *r_usage);
#endif
`,
	"memory.h": `#include <string.h>
`,
	"utime.h": `#ifndef __CC_UTIME_H
#define __CC_UTIME_H
#include <sys/types.h>
struct utimbuf {
	time_t actime;
	time_t modtime;
};
int utime(const char *path, const struct utimbuf *times);
#endif
`,
	"sys/mman.h": `#ifndef __CC_SYS_MMAN_H
#define __CC_SYS_MMAN_H
#include <sys/types.h>
#define PROT_NONE 0
#define PROT_READ 1
#define PROT_WRITE 2
#define PROT_EXEC 4
#define MAP_SHARED 0x01
#define MAP_PRIVATE 0x02
#define MAP_FIXED 0x10
#define MAP_ANONYMOUS 0x20
#define MAP_ANON MAP_ANONYMOUS
#define MAP_FAILED ((void *) -1)
#define MS_ASYNC 1
#define MS_INVALIDATE 2
#define MS_SYNC 4
#define MADV_NORMAL 0
#define MADV_RANDOM 1
#define MADV_SEQUENTIAL 2
#define MADV_WILLNEED 3
#define MADV_DONTNEED 4
void *mmap(void *addr, size_t len, int prot, int flags, int fd, off_t off);
int munmap(void *addr, size_t len);
int msync(void *addr, size_t len, int flags);
int mprotect(void *addr, size_t len, int prot);
int madvise(void *addr, size_t len, int advice);
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
