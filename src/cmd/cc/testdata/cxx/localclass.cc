// Classes of the same name in different functions are different types.
#include <stdio.h>

template <class T>
struct Box {
	T items[4];
	int n = 0;
	void push_back(T e) { items[n++] = e; }
	int size() const { return sizeof(T); }
};

static int first() {
	struct Data {
		int a = 1;
		int get() const { return a; }
	};
	Box<Data> b;
	Data d;
	b.push_back(d);
	return b.size() * 100 + b.items[0].get();
}

static int second() {
	struct Data {
		double x = 2.5, y = 4;
		int get() const { return (int)(x + y); }
	};
	Box<Data> b;
	Data d;
	b.push_back(d);
	return b.size() * 100 + b.items[0].get();
}

int main() { printf("%d %d\n", first(), second()); }

// A built-in unary operator on a class that converts to a number.
template <class T>
struct Prop {
	T v;
	operator T() const { return v; }
};
struct Flags {
	unsigned f;
	operator unsigned() const { return f; }
};
static int unary = printf("%g %d %u\n", -Prop<double>{2.5}, +Prop<int>{3}, ~Flags{0xf0} & 0xff);
