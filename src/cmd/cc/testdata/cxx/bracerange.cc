// Braced lists: a for statement's range, and a member of an aggregate
// whose members have constructors.
#include <stdio.h>
#include <initializer_list>

struct Name {
	const char *s;
	static int live;
	Name(const char *s = "none") : s(s) { live++; }
	Name(const Name &o) : s(o.s) { live++; }
	~Name() { live--; }
};
int Name::live;

struct Pair {
	Name a, b;
	int n = 4;
};

struct Holder {
	Pair p;
	Name names[3];
	Pair q;
	int k;
	Holder(const Name &x) : p{x, "second"}, names{"n0", x}, q{}, k{3} {}
};

static void designated();

int main() {
	Name left("left"), right("right");
	for (const Name &n : {left, right, Name("temp")}) {
		printf("%s %d\n", n.s, Name::live);
	}
	printf("live %d\n", Name::live);
	for (const char *s : {"a", "b", "c"}) {
		printf("%s", s);
	}
	for (int i : {3, 1, 2}) {
		printf(" %d", i);
	}
	printf("\n");
	{
		Holder h(left);
		printf("%s %s %d | %s %s %s | %s %s %d | %d\n", h.p.a.s, h.p.b.s, h.p.n, h.names[0].s, h.names[1].s, h.names[2].s, h.q.a.s, h.q.b.s, h.q.n, h.k);
		printf("live %d\n", Name::live);
	}
	printf("live %d\n", Name::live);
	designated();
}

// Designated initializers, and + on a string.
struct Fns {
	int (*add)(int, int) = nullptr;
	const char *label = "unset";
	Name who;
	int n = 5;
};
static int add(int a, int b) { return a + b; }
static void use(const Fns &f) { printf("%s %s %d %d\n", f.label, f.who.s, f.n, f.add ? f.add(1, 2) : -1); }
static void designated() {
	use({.add = add, .who = "me"});
	use({.label = "L", .n = 1});
	use(Fns{add});
	const char *plus = +"plus";
	printf("%s %d\n", plus, Name::live);
}
