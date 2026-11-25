#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include <new>

struct Str {
	char *p;
	Str() : p(nullptr) {}
	Str(const char *s) { p = new char[strlen(s) + 1]; strcpy(p, s); }
	Str(const Str &o) : p(nullptr) { if (o.p) { p = new char[strlen(o.p) + 1]; strcpy(p, o.p); } }
	~Str() { delete[] p; }
};

struct Obj {
	uint64_t id;
	Str *owner;
};

struct Call {
	Str name;
	union {
		uint64_t object = 0;
		Str *custom;
	};
	bool is_custom() const { return name.p == nullptr && custom != nullptr; }
};

class Var {
	int type = 0;
	union {
		bool b;
		int64_t i;
		double f;
		void *ptr;
		uint8_t mem[16]{ 0 };
	} data;

public:
	Var() {}
	Var(int64_t v) : type(1) { data.i = v; }
	Var(double v) : type(2) { data.f = v; }
	Var(const Str &s) : type(3) { new (data.mem) Str(s); }
	Var(const Obj &o) : type(4) { new (data.mem) Obj(o); }
	Var(const Call &c) : type(5) { new (data.mem) Call(c); }
	Var(const Var &o) : type(o.type) {
		switch (type) {
		case 3: new (data.mem) Str(*reinterpret_cast<const Str *>(o.data.mem)); break;
		case 4: new (data.mem) Obj(*reinterpret_cast<const Obj *>(o.data.mem)); break;
		case 5: new (data.mem) Call(*reinterpret_cast<const Call *>(o.data.mem)); break;
		default: data = o.data;
		}
	}
	~Var() { clear(); }
	void clear() {
		if (type == 3) reinterpret_cast<Str *>(data.mem)->~Str();
		if (type == 5) reinterpret_cast<Call *>(data.mem)->~Call();
		type = 0;
	}
	void show() const {
		switch (type) {
		case 0: printf("nil\n"); break;
		case 1: printf("int %lld\n", (long long)data.i); break;
		case 2: printf("float %g\n", data.f); break;
		case 3: printf("str %s\n", reinterpret_cast<const Str *>(data.mem)->p); break;
		case 4: printf("obj %llu %s\n", (unsigned long long)reinterpret_cast<const Obj *>(data.mem)->id, reinterpret_cast<const Obj *>(data.mem)->owner->p); break;
		case 5: {
			const Call *c = reinterpret_cast<const Call *>(data.mem);
			if (c->is_custom()) printf("call custom %s\n", c->custom->p);
			else printf("call %s %llu\n", c->name.p, (unsigned long long)c->object);
		} break;
		}
	}
};

int main() {
	Str owner("owner");
	Var vars[6] = { Var(), Var((int64_t)42), Var(1.5), Var(Str("hello")), Var(Obj{ 7, &owner }), Var() };
	Call std_call;
	std_call.name.~Str();
	new (&std_call.name) Str("method");
	std_call.object = 99;
	Call custom_call;
	custom_call.custom = &owner;
	for (int round = 0; round < 3; round++) {
		for (int i = 0; i < 6; i++) vars[i].show();
		// Every value becomes one of another kind.
		Var first(vars[0]);
		for (int i = 0; i < 5; i++) {
			vars[i].~Var();
			new (&vars[i]) Var(vars[i + 1]);
		}
		vars[5].~Var();
		new (&vars[5]) Var(round == 0 ? Var(std_call) : round == 1 ? Var(custom_call) : first);
	}
	for (int i = 0; i < 6; i++) vars[i].show();
	return 0;
}
