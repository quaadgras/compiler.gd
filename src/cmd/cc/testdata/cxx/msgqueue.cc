#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <new>

struct Custom { int v; int refs; };
class Handle {
	const char *name = nullptr;
	union {
		uint64_t object = 0;
		Custom *custom;
	};
public:
	Handle() {}
	explicit Handle(Custom *c) { custom = c; c->refs++; }
	explicit Handle(uint64_t id) { object = id; name = "m"; }
	Handle(const Handle &o) {
		if (o.is_custom()) { object = 0; custom = o.custom; custom->refs++; }
		else { name = o.name; object = o.object; }
	}
	void operator=(const Handle &o) {
		if (is_custom()) { custom->refs--; custom = nullptr; }
		if (o.is_custom()) { name = nullptr; object = 0; custom = o.custom; custom->refs++; }
		else { name = o.name; object = o.object; }
	}
	~Handle() { if (is_custom()) custom->refs--; }
	bool is_custom() const { return name == nullptr && custom != nullptr; }
	long value() const { return is_custom() ? custom->v : (long)object; }
};
struct Var {
	int type = 0;
	union {
		int64_t i;
		void *p;
		uint8_t mem[16];
	} data;
	Var() { memset(&data, 0, sizeof data); }
	Var(const Var &o) : type(o.type) { if (type == 2) new (data.mem) Handle(*reinterpret_cast<const Handle *>(o.data.mem)); else data = o.data; }
	explicit Var(const Handle &h) : type(2) { new (data.mem) Handle(h); }
	explicit Var(int64_t v) : type(1) { memset(&data, 0, sizeof data); data.i = v; }
	~Var() { if (type == 2) reinterpret_cast<Handle *>(data.mem)->~Handle(); }
};
struct Message {
	Handle h;
	int16_t type;
	int16_t args;
};
struct Page { uint8_t data[4096]; };

int main() {
	Page *page = (Page *)malloc(sizeof(Page));
	Custom c{7, 0};
	long sum = 0;
	for (int round = 0; round < 5; round++) {
		uint32_t used = 0;
		for (int k = 0; k < 20; k++) {
			int nargs = (k + round) % 3;
			Message *m = new (&page->data[used]) Message;
			m->args = nargs;
			m->h = (k % 2) ? Handle(&c) : Handle((uint64_t)(100 + k));
			used += sizeof(Message);
			for (int a = 0; a < nargs; a++) {
				Var *v = new (&page->data[used]) Var;
				used += sizeof(Var);
				Var src = (a % 2) ? Var(Handle(&c)) : Var((int64_t)a);
				new (v) Var(src);
			}
		}
		uint32_t off = 0;
		while (off < used) {
			Message *m = (Message *)&page->data[off];
			sum += m->h.value();
			off += sizeof(Message);
			Var *args = (Var *)(m + 1);
			for (int a = 0; a < m->args; a++) {
				sum += args[a].type;
				args[a].~Var();
			}
			off += sizeof(Var) * m->args;
			m->~Message();
		}
	}
	printf("%ld %d\n", sum, c.refs);
	free(page);
	return 0;
}
