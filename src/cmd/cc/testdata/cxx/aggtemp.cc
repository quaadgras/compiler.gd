// Aggregates with default member initializers, as temporaries.
#include <stdio.h>
struct Name {
	const char *s;
	static int live;
	Name(const char *s = "none") : s(s) { live++; }
	Name(const Name &o) : s(o.s) { live++; }
	~Name() { live--; }
};
int Name::live;
struct Fns {
	const char *label = "unset";
	Name who;
	int n = 5;
};
static void use(const Fns &f) { printf("%s %s %d\n", f.label, f.who.s, f.n); }
static void val(Fns f) { printf("%s %s %d\n", f.label, f.who.s, f.n); }
int main() {
	use({"a", "me"});
	printf("live %d\n", Name::live);
	val({"b", "you", 2});
	printf("live %d\n", Name::live);
	use(Fns{"c"});
	printf("live %d\n", Name::live);
	Fns f = {"d"};
	printf("live %d\n", Name::live);
}
