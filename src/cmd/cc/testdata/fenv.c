// The floating-point environment (Annex F): the output must match gcc's
// (with -frounding-math, and a C library that honors the directions).
#include <fenv.h>
#include <float.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>

#pragma STDC FENV_ACCESS ON

static const int modes[] = {FE_TONEAREST, FE_DOWNWARD, FE_UPWARD, FE_TOWARDZERO};
static const char *names[] = {"nearest", "down", "up", "zero"};

static void flags(const char *what) {
	int f = fetestexcept(FE_ALL_EXCEPT);
	printf("  %-10s%s%s%s%s%s\n", what, f & FE_INVALID ? " invalid" : "", f & FE_DIVBYZERO ? " divbyzero" : "",
		f & FE_OVERFLOW ? " overflow" : "", f & FE_UNDERFLOW ? " underflow" : "", f & FE_INEXACT ? " inexact" : "");
	feclearexcept(FE_ALL_EXCEPT);
}

int main(void) {
	volatile double one = 1, three = 3, big = 1e308, tiny = 0x1p-1074, zero = 0, inf = INFINITY, nan = NAN;
	volatile float onef = 1, threef = 3;
	volatile long long l = (1LL << 53) + 1;
	volatile unsigned long long ul = ~0ULL;
	volatile int i24 = (1 << 24) + 1;
	for (int i = 0; i < 4; i++) {
		fesetround(modes[i]);
		feclearexcept(FE_ALL_EXCEPT);
		printf("%s: FLT_ROUNDS %d, fegetround %s\n", names[i], FLT_ROUNDS, fegetround() == modes[i] ? "ok" : "wrong");
		printf("  1/3 %a, -1/3 %a\n", one / three, -one / three);
		flags("div");
		printf("  1f/3f %a\n", (double)(onef / threef));
		flags("divf");
		double d = one;
		d /= three;
		d++;
		printf("  d/=3, d++ %a\n", d);
		flags("assign");
		printf("  const %a\n", 1.0 / 3.0 + 0.0);
		flags("const");
		printf("  big*10 %a\n", big * 10);
		flags("overflow");
		printf("  tiny/2 %a\n", tiny / 2);
		flags("underflow");
		printf("  1/0 %a\n", one / zero);
		flags("divbyzero");
		printf("  isnan(inf-inf) %d\n", isnan(inf - inf));
		flags("inf-inf");
		printf("  1-1 %a\n", one - one);
		flags("1-1");
		printf("  ll %a, ull %a, ull->f %a, int->f %a\n", (double)l, (double)ul, (double)(float)ul, (double)(float)i24);
		flags("int conv");
		printf("  d->f %a\n", (double)(float)(one / three));
		flags("narrow");
		printf("  (int)2.5 %d, (int)-0.5 %d\n", (int)(2.5 * one), (int)(-0.5 * one));
		flags("to int");
		printf("  (int)3e9 %d\n", (int)(3e9 * one));
		flags("to int big");
		printf("  nan<1 %d\n", nan < one);
		flags("nan<1");
		printf("  nan==1 %d\n", nan == one);
		flags("nan==1");
		printf("  sqrt(2) %a\n", sqrt(2 * one));
		flags("sqrt");
		printf("  fma %a\n", fma(one / three, three, -one));
		flags("fma");
		printf("  rint %.1f %.1f, nearbyint %.1f, lrint %ld %ld\n", rint(2.5 * one), rint(-2.5 * one),
			nearbyint(1.5 * one), lrint(-1.5 * one), lrint(2.5 * one));
		flags("rint");
		printf("  %.3f %.3f %.2e %g %.3a %.0f %.1f\n", 2 / three, -2 / three, 2 / three, 2 / three, 2 / three,
			0.5 * one, 0.25 * one);
		feclearexcept(FE_ALL_EXCEPT);
		printf("  strtod %a %a %a, strtof %a\n", strtod("0.1", NULL), strtod("-0.1", NULL), strtod("1e400", NULL),
			(double)strtof("0.1", NULL));
		feclearexcept(FE_ALL_EXCEPT);
	}
	fesetround(FE_TONEAREST);
	feclearexcept(FE_ALL_EXCEPT);
	printf("library:\n  log(0) %f\n", log(zero));
	flags("log(0)");
	printf("  isnan(sqrt(-1)) %d\n", isnan(sqrt(-one)));
	flags("sqrt(-1)");
	printf("  exp(-1000) %a\n", exp(-1000 * one));
	flags("exp");
	printf("  pow(10, 400) %f\n", pow(10 * one, 400));
	flags("pow");
	printf("  nextafter %a\n", nextafter(DBL_MAX * one, INFINITY));
	flags("nextafter");
	printf("  nextafter %a\n", nextafter(0x1p-1022 * one, 0));
	flags("nextafter");
	fenv_t env;
	feholdexcept(&env);
	volatile double x = one / three;
	printf("  held %d\n", fetestexcept(FE_INEXACT) != 0);
	feupdateenv(&env);
	printf("  updated %d %a\n", fetestexcept(FE_INEXACT) != 0, x);
	return 0;
}
