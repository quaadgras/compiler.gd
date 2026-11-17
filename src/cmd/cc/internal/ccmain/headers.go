// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "sort"

// sysHeaders are the standard headers cmd/cc provides. The functions
// they declare are implemented in Go, in internal/c23.
var sysHeaders = map[string]string{
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
double atof(const char *nptr);
int rand(void);
void srand(unsigned int seed);
char *getenv(const char *name);
#define __STDC_VERSION_STDLIB_H__ 202311L
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
