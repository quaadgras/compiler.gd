// A lambda's copy of a class with an explicit copy constructor, and a
// table of pointers to member functions with null entries.
#include <stdio.h>

struct Set {
	int n;
	Set(int n) : n(n) {}
	explicit Set(const Set &o) : n(o.n + 100) {}
	bool has(int k) const { return k < n; }
};

template <class F>
static int call(F f, int k) { return f(k); }

struct Parser {
	int v = 3;
	int plus(int k) { return v + k; }
	int times(int k) { return v * k; }
	typedef int (Parser::*Fn)(int);
	struct Rule {
		Fn prefix;
		Fn infix;
		int prec;
	};
	static Rule *rules() {
		static Rule table[] = {
			{nullptr, nullptr, 0},
			{&Parser::plus, nullptr, 1},
			{nullptr, &Parser::times, 2},
		};
		return table;
	}
};

int main() {
	Set s(5);
	int tag = 2;
	printf("%d %d\n", call([s, tag](int k) { return s.has(k) + s.n + tag; }, 3), s.n);
	Parser p;
	Parser::Rule *r = Parser::rules();
	for (int i = 0; i < 3; i++) {
		int a = r[i].prefix ? (p.*r[i].prefix)(10) : -1;
		int b = r[i].infix ? (p.*r[i].infix)(10) : -1;
		printf("%d %d %d\n", a, b, r[i].prec);
	}
}

// A variable named _.
static void both(int k, int &a, int &b) { a = k; b = k * 2; }
static int underscore() {
	int _;
	int keep;
	both(4, _, keep);
	return printf("%d %d\n", _, keep);
}
static int underscoreDone = underscore();
