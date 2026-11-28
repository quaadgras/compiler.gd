// The address of a member function template's instance.
#include <stdio.h>

struct Info {
	int n;
};
struct Parser {
	int base = 10;
	template <int Hint, int Type>
	bool export_as(Info *i, int extra) {
		i->n = base + Hint * 100 + Type + extra;
		return Hint > 0;
	}
	bool plain(Info *i, int extra) {
		i->n = extra;
		return false;
	}
	typedef bool (Parser::*Handler)(Info *, int);
	Handler handlers[4];
	int count = 0;
	void reg(const char *name, Handler h) {
		handlers[count++] = h;
		printf("registered %s\n", name);
	}
	Parser() {
		reg("a", &Parser::export_as<0, 1>);
		reg("b", &Parser::export_as<3, 2>);
		reg("c", &Parser::plain);
	}
};

int main() {
	Parser p;
	Info i;
	for (int k = 0; k < p.count; k++) {
		bool r = (p.*p.handlers[k])(&i, 5);
		printf("%d %d\n", r, i.n);
	}
}
