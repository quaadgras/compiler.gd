// Braced arguments and explicit template arguments choose among overloads.
#include <stdio.h>
#include <utility>

struct Str {
	const char *s;
	Str(const char *s = "") : s(s) {}
	Str(const Str &o) : s(o.s) {}
};

struct Opt {
	Str text;
	int kind = 0, loc = 0;
	Opt() {}
	Opt(const Str &t, int k, int l = 7, const Str &theme = "") : text(t), kind(k), loc(l) {}
};

struct Mode {
	Str name;
	int n = 0;
	Mode() {}
	Mode(const Str &a) : name(a), n(1) {}
	Mode(const Str &a, const Str &b, const Str &c) : name(a), n(3) {}
	Mode(const Str &a, const Str &b, const Str &c, const Str &d) : name(a), n(4) {}
};

template <class T>
struct Vec {
	T items[8];
	int n = 0;
	void push_back(T e) { items[n++] = std::move(e); }
};

template <class T>
struct List {
	T items[8];
	int n = 0;
	T *push_back(const T &e) {
		items[n] = e;
		return &items[n++];
	}
};

struct Base {
	int id;
	virtual ~Base() {}
};
struct Node : Base {
	int extra = 9;
};
struct DB {
	static Base *slot;
	static Base *get(int id) { return slot; }
	template <class T>
	static T *get(int id) { return static_cast<T *>(get(id)); }
};
Base *DB::slot;

int main() {
	List<Opt> l;
	l.push_back({"plain", 3});
	List<Opt> *lp = &l;
	lp->push_back({"more", 4, 5});
	Vec<Mode> v;
	v.push_back({"blend", "mix", "add"});
	v.push_back({"one"});
	v.push_back({"a", "b", "c", "d"});
	for (int i = 0; i < l.n; i++) {
		printf("%s %d %d\n", l.items[i].text.s, l.items[i].kind, l.items[i].loc);
	}
	for (int i = 0; i < v.n; i++) {
		printf("%s %d\n", v.items[i].name.s, v.items[i].n);
	}
	Node n;
	DB::slot = &n;
	Vec<Node *> nodes;
	nodes.push_back(DB::get<Node>(1));
	printf("%d\n", nodes.items[0]->extra);
}
