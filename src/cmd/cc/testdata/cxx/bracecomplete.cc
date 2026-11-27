// A template's instance is complete before braces initialize it.
#include <stdio.h>
template <class T>
struct Prop {
	T v;
	operator T() const { return v; }
};
struct PD {
	double v;
	operator double() const { return v; }
};
static double g1 = PD{2.5};
static double g2 = Prop<double>{3.5};
static int g3 = printf("%g %g\n", (double)PD{4.5}, (double)Prop<double>{5.5});
int main() {
	printf("%g %g\n", g1, g2);
}
