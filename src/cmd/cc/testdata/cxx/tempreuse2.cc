// A temporary that the parser materializes for a conversion it tries
// and drops is still destroyed: one that another full expression's
// temporary was (materializePR's reuse) is made zero first, so that it
// destroys to nothing, as a new variable does.
#include <stdio.h>

static int live, twice;

struct Box {
	int *p;
	Box() : p(nullptr) {}
	~Box() {
		if (p) {
			if (*p != 1)
				twice++;
			*p = 2;
			delete p;
			p = nullptr;
			live--;
		}
	}
};

struct V {
	int type;
	Box box;
	V() : type(0) {}
	V(const char *) : type(1) { box.p = new int(1); live++; }
	V(int) : type(2) { box.p = new int(1); live++; }
	V(double) : type(3) { box.p = new int(1); live++; }
	V(const V &o) : type(o.type) {
		if (o.box.p) {
			box.p = new int(1);
			live++;
		}
	}
	~V() {}
};

struct D {
	int n = 0;
	bool has(const V &v) const { return v.type == 1; }
	V get(const V &k) const { return k; }
};

static V setting(const char *s) { return V(s); }

int main() {
	D d;
	for (int i = 0; i < 4; i++) {
		D a = d;
		V v = setting("x");
		if (!a.has("events"))
			continue;
		int dead = a.has("deadzone") ? a.get("deadzone").type : 7;
		V e = a.get("events");
		printf("%d %d %d\n", v.type, dead, e.type);
	}
	printf("live %d twice %d\n", live, twice);
	return 0;
}
