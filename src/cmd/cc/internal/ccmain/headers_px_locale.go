// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// POSIX's locale objects (newlocale, uselocale) and the functions of a
// locale (the _l functions), and the rest of POSIX's ctype.h, wchar.h,
// string.h, stdlib.h, time.h, math.h and arpa/inet.h: wcwidth, memmem,
// strerror_r, the drand48 and random families, crypt, strptime, tzset,
// the Bessel functions and others.

// localeT declares locale_t in each header that has it.
const localeT = `#ifndef __CC_LOCALE_T
#define __CC_LOCALE_T
typedef struct __cc_locale *locale_t;
#endif
`

func init() {
	extendHeader("locale.h", localeT+`#define LC_CTYPE_MASK (1 << LC_CTYPE)
#define LC_NUMERIC_MASK (1 << LC_NUMERIC)
#define LC_TIME_MASK (1 << LC_TIME)
#define LC_COLLATE_MASK (1 << LC_COLLATE)
#define LC_MONETARY_MASK (1 << LC_MONETARY)
#define LC_MESSAGES_MASK (1 << LC_MESSAGES)
#define LC_ALL_MASK (LC_CTYPE_MASK | LC_NUMERIC_MASK | LC_TIME_MASK | LC_COLLATE_MASK | LC_MONETARY_MASK | LC_MESSAGES_MASK)
#define LC_GLOBAL_LOCALE ((locale_t)-1)
locale_t newlocale(int category_mask, const char *locale, locale_t base);
locale_t duplocale(locale_t locobj);
void freelocale(locale_t locobj);
locale_t uselocale(locale_t newloc);
`)
	extendHeader("ctype.h", localeT+`int isalnum_l(int c, locale_t locale);
int isalpha_l(int c, locale_t locale);
int isblank_l(int c, locale_t locale);
int iscntrl_l(int c, locale_t locale);
int isdigit_l(int c, locale_t locale);
int isgraph_l(int c, locale_t locale);
int islower_l(int c, locale_t locale);
int isprint_l(int c, locale_t locale);
int ispunct_l(int c, locale_t locale);
int isspace_l(int c, locale_t locale);
int isupper_l(int c, locale_t locale);
int isxdigit_l(int c, locale_t locale);
int tolower_l(int c, locale_t locale);
int toupper_l(int c, locale_t locale);
int isascii(int c);
int toascii(int c);
#define _tolower(c) tolower(c)
#define _toupper(c) toupper(c)
`)
	extendHeader("wctype.h", localeT+`int iswalnum_l(wint_t wc, locale_t locale);
int iswalpha_l(wint_t wc, locale_t locale);
int iswblank_l(wint_t wc, locale_t locale);
int iswcntrl_l(wint_t wc, locale_t locale);
int iswdigit_l(wint_t wc, locale_t locale);
int iswgraph_l(wint_t wc, locale_t locale);
int iswlower_l(wint_t wc, locale_t locale);
int iswprint_l(wint_t wc, locale_t locale);
int iswpunct_l(wint_t wc, locale_t locale);
int iswspace_l(wint_t wc, locale_t locale);
int iswupper_l(wint_t wc, locale_t locale);
int iswxdigit_l(wint_t wc, locale_t locale);
int iswctype_l(wint_t wc, wctype_t desc, locale_t locale);
wctype_t wctype_l(const char *property, locale_t locale);
wint_t towlower_l(wint_t wc, locale_t locale);
wint_t towupper_l(wint_t wc, locale_t locale);
wint_t towctrans_l(wint_t wc, wctrans_t desc, locale_t locale);
wctrans_t wctrans_l(const char *property, locale_t locale);
`)
	// strerror_r is POSIX's (XSI's), returning an error number, unless
	// _GNU_SOURCE asks for glibc's, returning the message.
	extendHeader("string.h", localeT+`int strcoll_l(const char *s1, const char *s2, locale_t locale);
size_t strxfrm_l(char *restrict s1, const char *restrict s2, size_t n, locale_t locale);
char *strerror_l(int errnum, locale_t locale);
void *memmem(const void *haystack, size_t haystacklen, const void *needle, size_t needlelen);
#ifdef _GNU_SOURCE
char *__cc_gnu_strerror_r(int errnum, char *buf, size_t buflen);
#define strerror_r __cc_gnu_strerror_r
#else
int strerror_r(int errnum, char *buf, size_t buflen);
#endif
`)
	extendHeader("strings.h", localeT+`int strcasecmp_l(const char *s1, const char *s2, locale_t locale);
int strncasecmp_l(const char *s1, const char *s2, size_t n, locale_t locale);
`)
	extendHeader("wchar.h", localeT+`int wcwidth(wchar_t c);
int wcswidth(const wchar_t *s, size_t n);
wchar_t *wcsdup(const wchar_t *s);
wchar_t *wcpcpy(wchar_t *restrict s1, const wchar_t *restrict s2);
wchar_t *wcpncpy(wchar_t *restrict s1, const wchar_t *restrict s2, size_t n);
size_t wcsnlen(const wchar_t *s, size_t maxlen);
int wcscasecmp(const wchar_t *s1, const wchar_t *s2);
int wcsncasecmp(const wchar_t *s1, const wchar_t *s2, size_t n);
int wcscasecmp_l(const wchar_t *s1, const wchar_t *s2, locale_t locale);
int wcsncasecmp_l(const wchar_t *s1, const wchar_t *s2, size_t n, locale_t locale);
int wcscoll_l(const wchar_t *s1, const wchar_t *s2, locale_t locale);
size_t wcsxfrm_l(wchar_t *restrict s1, const wchar_t *restrict s2, size_t n, locale_t locale);
size_t wcsftime_l(wchar_t *restrict s, size_t maxsize, const wchar_t *restrict format, const struct tm *restrict timeptr, locale_t locale);
size_t mbsnrtowcs(wchar_t *restrict dst, const char **restrict src, size_t nmc, size_t len, mbstate_t *restrict ps);
size_t wcsnrtombs(char *restrict dst, const wchar_t **restrict src, size_t nwc, size_t len, mbstate_t *restrict ps);
wchar_t *wcswcs(const wchar_t *s1, const wchar_t *s2);
struct __cc_FILE *open_wmemstream(wchar_t **bufp, size_t *sizep);
`)
	extendHeader("stdlib.h", localeT+`double drand48(void);
double erand48(unsigned short xsubi[3]);
long lrand48(void);
long nrand48(unsigned short xsubi[3]);
long mrand48(void);
long jrand48(unsigned short xsubi[3]);
void srand48(long seedval);
unsigned short *seed48(unsigned short seed16v[3]);
void lcong48(unsigned short param[7]);
long random(void);
void srandom(unsigned seed);
char *initstate(unsigned seed, char *state, size_t size);
char *setstate(char *state);
int rand_r(unsigned *seed);
long a64l(const char *s);
char *l64a(long value);
int getsubopt(char **optionp, char *const *keylistp, char **valuep);
int mkostemp(char *template, int flags);
int mkstemps(char *template, int suffixlen);
int mkostemps(char *template, int suffixlen, int flags);
void setkey(const char *key);
double strtod_l(const char *restrict nptr, char **restrict endptr, locale_t locale);
float strtof_l(const char *restrict nptr, char **restrict endptr, locale_t locale);
long double strtold_l(const char *restrict nptr, char **restrict endptr, locale_t locale);
long strtol_l(const char *restrict nptr, char **restrict endptr, int base, locale_t locale);
unsigned long strtoul_l(const char *restrict nptr, char **restrict endptr, int base, locale_t locale);
long long strtoll_l(const char *restrict nptr, char **restrict endptr, int base, locale_t locale);
unsigned long long strtoull_l(const char *restrict nptr, char **restrict endptr, int base, locale_t locale);
`)
	extendHeader("unistd.h", `char *crypt(const char *key, const char *salt);
void encrypt(char block[64], int edflag);
`)
	sysHeaders["crypt.h"] = `#ifndef __CC_CRYPT_H
#define __CC_CRYPT_H
struct crypt_data {
	int initialized;
	char __buf[256];
};
char *crypt(const char *key, const char *salt);
char *crypt_r(const char *key, const char *salt, struct crypt_data *data);
#endif
`
	extendHeader("time.h", localeT+`size_t strftime_l(char *restrict s, size_t maxsize, const char *restrict format, const struct tm *restrict timeptr, locale_t locale);
char *strptime(const char *restrict buf, const char *restrict format, struct tm *restrict tm);
void tzset(void);
extern char *tzname[2];
extern long timezone;
extern int daylight;
char *asctime_r(const struct tm *restrict tm, char *restrict buf);
char *ctime_r(const time_t *timer, char *buf);
`)
	extendHeader("math.h", `double j0(double x);
double j1(double x);
double jn(int n, double x);
double y0(double x);
double y1(double x);
double yn(int n, double x);
float j0f(float x);
float j1f(float x);
float jnf(int n, float x);
float y0f(float x);
float y1f(float x);
float ynf(int n, float x);
`)
	// inet_lnaof takes a struct in_addr, whose s_addr the macro passes.
	// headers_sock.go's init, which runs after this one, makes sockHeaders
	// the system's.
	h := sockHeaders["arpa/inet.h"]
	i := strings.LastIndex(h, "#endif")
	sockHeaders["arpa/inet.h"] = h[:i] + `in_addr_t __cc_inet_lnaof(in_addr_t in);
#define inet_lnaof(in) __cc_inet_lnaof((in).s_addr)
in_addr_t __cc_inet_netof(in_addr_t in);
#define inet_netof(in) __cc_inet_netof((in).s_addr)
struct in_addr inet_makeaddr(in_addr_t net, in_addr_t lna);
in_addr_t inet_network(const char *cp);
` + h[i:]
	extendHeader("monetary.h", localeT+`ssize_t strfmon_l(char *restrict s, size_t maxsize, locale_t locale, const char *restrict format, ...);
`)

	for c, goName := range map[string]string{
		"newlocale": "locale.Newlocale", "duplocale": "locale.Duplocale", "freelocale": "locale.Freelocale",
		"uselocale": "locale.Uselocale",

		"isalnum_l": "ctype.IsalnumL", "isalpha_l": "ctype.IsalphaL", "isblank_l": "ctype.IsblankL",
		"iscntrl_l": "ctype.IscntrlL", "isdigit_l": "ctype.IsdigitL", "isgraph_l": "ctype.IsgraphL",
		"islower_l": "ctype.IslowerL", "isprint_l": "ctype.IsprintL", "ispunct_l": "ctype.IspunctL",
		"isspace_l": "ctype.IsspaceL", "isupper_l": "ctype.IsupperL", "isxdigit_l": "ctype.IsxdigitL",
		"tolower_l": "ctype.TolowerL", "toupper_l": "ctype.ToupperL", "isascii": "ctype.Isascii",
		"toascii": "ctype.Toascii",

		"iswalnum_l": "wctype.IswalnumL", "iswalpha_l": "wctype.IswalphaL", "iswblank_l": "wctype.IswblankL",
		"iswcntrl_l": "wctype.IswcntrlL", "iswdigit_l": "wctype.IswdigitL", "iswgraph_l": "wctype.IswgraphL",
		"iswlower_l": "wctype.IswlowerL", "iswprint_l": "wctype.IswprintL", "iswpunct_l": "wctype.IswpunctL",
		"iswspace_l": "wctype.IswspaceL", "iswupper_l": "wctype.IswupperL", "iswxdigit_l": "wctype.IswxdigitL",
		"iswctype_l": "wctype.IswctypeL", "wctype_l": "wctype.WctypeL", "towlower_l": "wctype.TowlowerL",
		"towupper_l": "wctype.TowupperL", "towctrans_l": "wctype.TowctransL", "wctrans_l": "wctype.WctransL",

		"strcoll_l": "string.StrcollL", "strxfrm_l": "string.StrxfrmL", "strerror_l": "string.StrerrorL",
		"memmem": "string.Memmem", "strerror_r": "string.StrerrorR", "__cc_gnu_strerror_r": "string.GnuStrerrorR",
		"strcasecmp_l": "strings.StrcasecmpL", "strncasecmp_l": "strings.StrncasecmpL",

		"wcwidth": "wchar.Wcwidth", "wcswidth": "wchar.Wcswidth", "wcsdup": "wchar.Wcsdup",
		"wcpcpy": "wchar.Wcpcpy", "wcpncpy": "wchar.Wcpncpy", "wcsnlen": "wchar.Wcsnlen",
		"wcscasecmp": "wchar.Wcscasecmp", "wcsncasecmp": "wchar.Wcsncasecmp", "wcscasecmp_l": "wchar.WcscasecmpL",
		"wcsncasecmp_l": "wchar.WcsncasecmpL", "wcscoll_l": "wchar.WcscollL", "wcsxfrm_l": "wchar.WcsxfrmL",
		"wcsftime_l": "wchar.WcsftimeL", "mbsnrtowcs": "wchar.Mbsnrtowcs", "wcsnrtombs": "wchar.Wcsnrtombs",
		"wcswcs": "wchar.Wcsstr", "open_wmemstream": "wchar.OpenWmemstream",

		"drand48": "stdlib.Drand48", "erand48": "stdlib.Erand48", "lrand48": "stdlib.Lrand48",
		"nrand48": "stdlib.Nrand48", "mrand48": "stdlib.Mrand48", "jrand48": "stdlib.Jrand48",
		"srand48": "stdlib.Srand48", "seed48": "stdlib.Seed48", "lcong48": "stdlib.Lcong48",
		"random": "stdlib.Random", "srandom": "stdlib.Srandom", "initstate": "stdlib.Initstate",
		"setstate": "stdlib.Setstate", "rand_r": "stdlib.RandR", "a64l": "stdlib.A64l", "l64a": "stdlib.L64a",
		"getsubopt": "stdlib.Getsubopt", "mkostemp": "stdlib.Mkostemp", "mkstemps": "stdlib.Mkstemps",
		"mkostemps": "stdlib.Mkostemps", "strtod_l": "stdlib.StrtodL", "strtof_l": "stdlib.StrtofL",
		"strtold_l": "stdlib.StrtodL", "strtol_l": "stdlib.StrtolL", "strtoul_l": "stdlib.StrtoulL",
		"strtoll_l": "stdlib.StrtolL", "strtoull_l": "stdlib.StrtoulL",

		"crypt": "crypt.Crypt", "crypt_r": "crypt.CryptR", "encrypt": "crypt.Encrypt", "setkey": "crypt.Setkey",

		"strftime_l": "time.StrftimeL", "strptime": "time.Strptime", "tzset": "time.Tzset",
		"asctime_r": "time.AsctimeR", "ctime_r": "time.CtimeR",

		"j0": "math.J0", "j1": "math.J1", "jn": "math.Jn", "y0": "math.Y0", "y1": "math.Y1", "yn": "math.Yn",
		"j0f": "math.J0f", "j1f": "math.J1f", "jnf": "math.Jnf", "y0f": "math.Y0f", "y1f": "math.Y1f",
		"ynf": "math.Ynf",

		"__cc_inet_lnaof": "arpa/inet.InetLnaof", "__cc_inet_netof": "arpa/inet.InetNetof",
		"inet_makeaddr": "arpa/inet.InetMakeaddr", "inet_network": "arpa/inet.InetNetwork",

		"nl_langinfo_l": "langinfo.NlLanginfoL", "strfmon_l": "monetary.StrfmonL",
	} {
		libc[c] = goName
	}
	// strtol_l and strtoul_l have long's range.
	libcLong32["strtol_l"] = "stdlib.StrtolL32"
	libcLong32["strtoul_l"] = "stdlib.StrtoulL32"

	libcVars["tzname"] = "time.Tzname"
	libcVars["timezone"] = "time.Timezone"
	libcVars["daylight"] = "time.Daylight"
}
