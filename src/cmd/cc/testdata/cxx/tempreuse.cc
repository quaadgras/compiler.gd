// Temporaries of a class that full expressions destroy are one variable
// of the frame (materializePR): each is constructed again, and
// one whose lifetime a reference extends is not shared.
#include <stdio.h>
#include <string.h>

struct Str {
	char *p;
	int n;
	int pad[3]; // not set by the constructor
	Str(const char *s) : n((int)strlen(s)) {
		p = new char[n + 1];
		memcpy(p, s, n + 1);
	}
	Str(const Str &o) : n(o.n) {
		p = new char[n + 1];
		memcpy(p, o.p, n + 1);
	}
	~Str() {
		p[0] = '#';
		delete[] p;
	}
	Str operator+(const Str &o) const {
		char buf[256];
		snprintf(buf, sizeof buf, "%s%s", p, o.p);
		return Str(buf);
	}
};

static int total;
static int len(const Str &s) { return s.n; }
static const char *show(const Str &s) { return s.p; }

struct Two {
	Str a, b;
	Two(const Str &x, const Str &y) : a(x), b(y) {}
};

static Str pick(bool c) { return c ? Str("yes") : Str("no"); }

int main() {
	printf("%d\n", len(Str("abc")));
	printf("%d\n", len(Str("hello") + Str(" world")));
	const Str &kept = Str("kept");
	printf("%d %s\n", len(Str("x") + Str("yz")), show(kept));
	for (int i = 0; i < 3; i++) {
		total += len(Str(i ? "loop" : "first") + Str("!"));
		if (len(Str("cond")) > i)
			total += len(i & 1 ? Str("odd") : Str("even!"));
	}
	printf("%d %s\n", total, show(kept));
	Two t(Str("one"), Str("two"));
	printf("%s %s %s\n", show(t.a), show(t.b), show(pick(true) + pick(false)));
	int k = 0;
	while (len(Str("abcd") + Str("e")) > k)
		k++;
	printf("%d %s\n", k, show(kept));
	return 0;
}
