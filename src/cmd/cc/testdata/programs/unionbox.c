#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

// A union with a member that has pointers inside it.
typedef struct { int tag; char *s; } Str;
typedef struct { int tag; double d; } Num;
typedef struct { char *name; struct Pair *next; } Pair;
typedef union {
	Str str;
	Num num;
	Pair pair;
	long raw;
	char *names[3];
} Node;

// A union whose bytes hold values that are none of its members.
typedef struct { char *text; } String;
typedef struct { uint64_t id; void *obj; } ObjData;
typedef struct {
	int type;
	union {
		int64_t i;
		double f;
		void *ptr;
		uint8_t mem[16];
	} data;
} Variant;

static String *var_string(Variant *v) { return (String *)v->data.mem; }
static ObjData *var_obj(Variant *v) { return (ObjData *)v->data.mem; }

static Node table[2] = { { .str = { 1, "one" } }, { .num = { 2, 2.5 } } };

int main(void) {
	printf("%d %s %d %g\n", table[0].str.tag, table[0].str.s, table[1].num.tag, table[1].num.d);

	Node n;
	memset(&n, 0, sizeof n);
	n.str.tag = 7;
	n.str.s = "seven";
	Str *sp = &n.str;
	printf("%d %s %s\n", n.str.tag, sp->s, n.str.s);
	Node m = n;            // a copy: its own
	m.str.s = "copied";
	printf("%s %s\n", n.str.s, m.str.s);
	n.names[1] = "middle";
	char **np = n.names;
	printf("%s %s\n", np[1], n.names[2] ? "set" : "null");
	n.num.tag = 3;
	n.num.d = 0.5;
	printf("%d %g\n", n.num.tag, n.num.d);

	Node *list = malloc(100 * sizeof *list);
	for (int i = 0; i < 100; i++) {
		if (i % 2) { list[i].str.tag = i; list[i].str.s = "odd"; }
		else { list[i].num.tag = i; list[i].num.d = i; }
	}
	long sum = 0;
	for (int i = 0; i < 100; i++) sum += i % 2 ? (long)strlen(list[i].str.s) + list[i].str.tag : list[i].num.tag;
	printf("%ld\n", sum);

	Variant v = { 0 };
	v.type = 1;
	var_string(&v)->text = "a string";
	printf("%s\n", var_string(&v)->text);
	Variant w = v;
	var_string(&w)->text = "another";
	printf("%s %s\n", var_string(&v)->text, var_string(&w)->text);
	v.type = 2;
	var_obj(&v)->id = 42;
	var_obj(&v)->obj = &w;
	printf("%llu %d\n", (unsigned long long)var_obj(&v)->id, var_obj(&v)->obj == &w);
	v.type = 3;
	v.data.f = 1.25;
	printf("%g\n", v.data.f);
	v.data.ptr = &v;
	printf("%d\n", v.data.ptr == &v);
	return 0;
}
