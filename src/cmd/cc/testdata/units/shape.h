#pragma once
#include <stdio.h>

struct Hidden; // defined in b.cc only
struct Never;  // defined nowhere

template <class T>
struct Box {
	T items[4];
	int n = 0;
	void push(const T &v) { items[n++] = v; }
	T sum() const {
		T s = T();
		for (int i = 0; i < n; i++) {
			s = s + items[i];
		}
		return s;
	}
};

inline int counter() {
	static int calls = 100;
	return ++calls;
}

struct Registry {
	int made = 0;
	Registry() { printf("Registry made\n"); }
	static Registry &get() {
		static Registry r;
		return r;
	}
};

inline int shared_value = 7;

class Shape {
public:
	virtual ~Shape() {}
	virtual double area() const = 0;
	virtual const char *name() const;
	int describe() const {
		printf("%s %g\n", name(), area());
		return ++Registry::get().made;
	}
};

class Square : public Shape {
	double s;

public:
	Square(double s) : s(s) {}
	double area() const override { return s * s; }
	const char *name() const override { return "square"; }
};

struct Holder {
	Hidden *h = nullptr;
	Never *n = nullptr;
	Shape *s = nullptr;
};

Shape *make_circle(double r);
Hidden *make_hidden(int v);
int hidden_value(const Hidden *h);
int use_holder(Holder &h);
typedef int (*binop)(int, int);
binop get_op(int which);
inline int add_op(int a, int b) { return a + b; }
extern int global_count;
extern const char *global_name;
