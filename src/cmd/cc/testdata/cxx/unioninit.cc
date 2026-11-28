// A union's default member initializer is applied by its constructor,
// also in memory that held something else.
#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include <new>

struct Custom {
	int v;
};
class Handle {
	const char *name = nullptr;
	union {
		uint64_t object = 0;
		Custom *custom;
	};

public:
	Handle() {}
	explicit Handle(Custom *c) { custom = c; }
	bool is_null() const { return name == nullptr && object == 0; }
	bool is_custom() const { return name == nullptr && custom != nullptr; }
	int value() const { return is_custom() ? custom->v : -1; }
};
struct Message {
	Handle h;
	int type;
};
union Num {
	int i = 42;
	float f;
};
int main() {
	alignas(8) static unsigned char page[256];
	memset(page, 0xAB, sizeof page);
	Message *m = new (page) Message;
	Custom c{7};
	Message *n = new (page + 64) Message;
	n->h = Handle(&c);
	Num num;
	printf("%d %d %d %d %d %d\n", m->h.is_null(), m->h.is_custom(), m->h.value(), n->h.is_custom(), n->h.value(), num.i);
	return 0;
}
