// More than one base class with virtual functions.
#include <stdio.h>

struct Named {
	const char *n;
	Named(const char *n) : n(n) {}
	virtual ~Named() { printf("~Named %s\n", n); }
	virtual const char *name() const { return n; }
	virtual int rank() const { return 1; }
};

struct Counter {
	int count = 0;
	virtual void add(int k) { count += k; }
	virtual int total() const { return count; }
	virtual ~Counter() { printf("~Counter %d\n", count); }
};

struct Printer {
	virtual void print() const = 0;
	virtual long big(long a, double b) const { return a + (long)b; }
};

struct Value {
	int v;
	Value(int v) : v(v) { printf("Value(%d)\n", v); }
	Value(const Value &o) : v(o.v + 100) { printf("Value copy\n"); }
	~Value() { printf("~Value %d\n", v); }
};

struct Maker {
	virtual Value make(Value in) const { return Value(in.v + 1); }
	virtual Maker &self() { return *this; }
};

struct Both : Named, Counter, Printer {
	int extra = 7;
	Both() : Named("both") {}
	~Both() override { printf("~Both %d\n", extra); }
	int rank() const override { return 2; }
	void add(int k) override {
		count += 2 * k;
		extra++;
	}
	void print() const override { printf("Both %s %d %d\n", n, count, extra); }
};

// A base of a base: its tables come with it, and are replaced.
struct More : Both, Maker {
	int more = 40;
	~More() { printf("~More %d\n", more); }
	int total() const override { return count + more; }
	void print() const override { printf("More %s %d %d %d\n", n, count, extra, more); }
	long big(long a, double b) const override { return a * (long)b + more; }
	Value make(Value in) const override { return Value(in.v + more); }
	More &self() override { return *this; }
};

// Only the second base has virtual functions.
struct Plain {
	int p = 3;
};
struct Late : Plain, Counter {
	void add(int k) override { count += k * p; }
};

static void use(Named *n, Counter *c, Printer *p) {
	c->add(5);
	printf("%s rank %d total %d big %ld\n", n->name(), n->rank(), c->total(), p->big(6, 7.5));
	p->print();
}

int main() {
	{
		Both b;
		use(&b, &b, &b);
		Counter &c = b;
		c.add(1);
		b.print();
		printf("%d %d\n", b.total(), (Counter *)&b == &c);
	}
	More *m = new More;
	use(m, m, m);
	Maker *mk = m;
	{
		Value v = mk->make(Value(1));
		printf("made %d\n", v.v);
	}
	printf("self %d\n", &mk->self() == mk);
	Both *b = m;
	Printer *p = b;
	p->print();
	Counter *c = m;
	delete c;
	Counter *l = new Late;
	l->add(4);
	printf("late %d\n", l->total());
	delete l;
	Named *n = new Both;
	delete n;
	Printer *pp = new More;
	pp->print();
	delete static_cast<More *>(pp);
}
