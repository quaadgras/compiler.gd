// A class's tables are made where its key function is defined.
#include <stdio.h>

struct Hidden; // never defined here

template <class T>
struct Owner {
	T *p = nullptr;
	~Owner() { delete p; } // cannot be made for an incomplete T
};

// Its key function, run, is not defined in this unit: neither are its
// tables, nor its implicit destructor, which could not be.
struct Elsewhere {
	Owner<Hidden> h;
	virtual void run();
	virtual int id() { return 1; }
};

struct Base {
	virtual ~Base() { printf("~Base\n"); }
	virtual void run();
	virtual int id() const { return 1; }
};

struct Derived : Base {
	Owner<int> o;
	void run() override;
	int id() const override { return 2; }
};

static void use(Elsewhere *e) {
	if (e) {
		e->run();
	}
}

void Base::run() { printf("Base::run %d\n", id()); }
void Derived::run() { printf("Derived::run %d\n", id()); }

int main() {
	use(nullptr);
	Base *b = new Derived;
	b->run();
	delete b;
	Base plain;
	plain.run();
}
