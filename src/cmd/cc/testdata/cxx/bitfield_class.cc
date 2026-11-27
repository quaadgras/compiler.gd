// A class with bit-fields among members that are pointers and classes,
// as Godot's Object is: the other members are what they are, in a base
// too, and the bit-fields are in the bytes between them.
#include <cstdio>
#include <cstdlib>
#include <new>

struct Name {
	const char *s;
	Name(const char *p = "none") : s(p) {}
};

class Base {
public:
	void *ext = nullptr;
	Name name;
	unsigned id = 7;
	bool blocked : 1;
	bool emitting : 1;
	unsigned level : 5;
	int delta : 4;
	Base *next = nullptr;
	bool late : 1;
	Base() : blocked(false), emitting(true), level(19), delta(-3), late(true) {}
	virtual ~Base() {}
	virtual int kind() const { return 1; }
};

class Counted : public Base {
public:
	unsigned refs = 1;
	unsigned weak = 2;
	int kind() const override { return 2; }
};

// (No constructor of its own: new Loader() sets it to zero first.)
class Loader : public Counted {
public:
	void *hooks[3];
	int kind() const override { return 3; }
};

struct Flags {
	unsigned a : 3;
	unsigned b : 13;
	char c;
	long long wide : 40;
	const char *text;
};

static void show(const char *what, Base *b) {
	printf("%s: kind %d id %u blocked %d emitting %d level %u delta %d late %d name %s next %d\n", what, b->kind(), b->id,
			b->blocked, b->emitting, b->level, b->delta, b->late, b->name.s, b->next != nullptr);
}

int main() {
	void *mem = malloc(sizeof(Loader) + 16);
	Loader *l = new ((char *)mem + 16) Loader();
	show("loader", l);
	printf("refs %u weak %u hooks %d %d\n", l->refs, l->weak, l->hooks[0] == nullptr, l->hooks[2] == nullptr);
	l->blocked = true;
	l->level = 31;
	l->delta = 7;
	l->late = false;
	l->next = l;
	l->refs += 5;
	l->hooks[1] = l;
	show("changed", l);
	printf("refs %u hooks %d\n", l->refs, l->hooks[1] == (void *)l);
	Counted c;
	c.name = Name("counted");
	c.level = 3;
	show("counted", &c);
	Base copy = c;
	show("copy", &copy);
	Flags f = {5, 4097, 'x', -123456789012LL, "text"};
	f.b += 3;
	f.wide *= 2;
	printf("flags %u %u %c %lld %s\n", f.a, f.b, f.c, f.wide, f.text);
	Flags g = f;
	g.a = 1;
	printf("flags %u %u %u\n", g.a, f.a, (unsigned)(sizeof(Flags) + sizeof(Base) + sizeof(Counted) + sizeof(Loader)));
	l->~Loader();
	free(mem);
	return 0;
}
