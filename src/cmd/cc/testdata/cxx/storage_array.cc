// An array of bytes that is declared with an alignment is memory that
// objects are kept in: of a class with pointers too, made there with
// the placement new, as Godot keeps the Callables of a signal.
#include <cstdio>
#include <cstdlib>
#include <new>

struct Slot {
	const char *name;
	Slot *next;
	int n;
	Slot(const char *p_name, int p_n) : name(p_name), next(nullptr), n(p_n) {}
	~Slot() { printf("~%s\n", name); }
};

static const char *names[] = { "a", "b", "c", "d", "e", "f", "g" };

static int run(int count) {
	const int MAX_ON_STACK = 4;
	alignas(Slot) unsigned char stack[sizeof(Slot) * MAX_ON_STACK];
	Slot *slots = (Slot *)stack;
	if (count > MAX_ON_STACK) {
		slots = (Slot *)malloc(sizeof(Slot) * count);
	}
	for (int i = 0; i < count; i++) {
		new (&slots[i]) Slot(names[i], i * 10);
		if (i > 0) {
			slots[i - 1].next = &slots[i];
		}
	}
	int sum = 0;
	for (Slot *s = &slots[0]; s; s = s->next) {
		sum += s->n;
		printf("%s ", s->name);
	}
	printf("| %d %d\n", sum, slots == (Slot *)stack);
	for (int i = 0; i < count; i++) {
		slots[i].~Slot();
	}
	if (slots != (Slot *)stack) {
		free(slots);
	}
	return sum;
}

alignas(8) static unsigned char pool[64];

int main() {
	int a = run(3);
	int b = run(6);
	Slot *g = new (pool) Slot("global", 5);
	Slot *h = new (pool + sizeof(Slot)) Slot("second", 6);
	g->next = h;
	printf("%d %d %s %s %d\n", a, b, g->name, g->next->name, (int)sizeof(pool));
	return 0;
}
