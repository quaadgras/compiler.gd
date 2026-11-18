// C23's FENV_ROUND: a constant rounding direction for a compound
// statement's operations, conversions and some library functions (sqrt,
// printf); FP_CONTRACT OFF rounds products alone.
#include <math.h>
#include <stdio.h>

int main(void) {
	volatile double one = 1, three = 3, x = 0.1, y = 10;
	{
#pragma STDC FENV_ROUND FE_UPWARD
		printf("%a %a %.1f %a\n", one / three, sqrt(3 * one), 0.25 * one, (double)(float)(one / three));
	}
	{
#pragma STDC FENV_ROUND FE_TOWARDZERO
#pragma STDC FP_CONTRACT OFF
		printf("%a %a\n", -one / three, x * y - one);
	}
	printf("%a %.1f\n", one / three, 0.25 * one);
	return 0;
}
