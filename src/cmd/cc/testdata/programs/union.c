#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

typedef union {
	int64_t i;
	double d;
	void *p;
	char *s;
	uint8_t mem[16];
} Value;

typedef struct {
	int tag;
	Value v;
} TValue;

typedef struct Node { struct Node *next; TValue val; } Node;

static Value global = { .s = "global" };
static TValue table[3] = { {1, {.i = 42}}, {2, {.d = 1.5}}, {3, {.s = "three"}} };

static void show(const TValue *t) {
	switch (t->tag) {
	case 1: printf("int %lld\n", (long long)t->v.i); break;
	case 2: printf("double %g\n", t->v.d); break;
	case 3: printf("string %s\n", t->v.s); break;
	case 4: printf("ptr %s\n", t->v.p ? "set" : "null"); break;
	}
}

int main(void) {
	printf("%s %d\n", global.s, (int)(sizeof(Value) >= 16));
	for (int i = 0; i < 3; i++) show(&table[i]);

	// A list of values in malloc's memory, whose members change kind.
	Node *head = NULL;
	for (int i = 0; i < 1000; i++) {
		Node *n = malloc(sizeof *n);
		n->next = head;
		n->val.tag = 1 + i % 3;
		if (i % 3 == 0) n->val.v.i = i;
		else if (i % 3 == 1) n->val.v.d = i / 2.0;
		else { char *s = malloc(16); snprintf(s, 16, "s%d", i); n->val.v.s = s; }
		head = n;
	}
	long long si = 0; double sd = 0; size_t sl = 0;
	for (int round = 0; round < 4; round++) {
		for (Node *n = head; n; n = n->next) {
			switch (n->val.tag) {
			case 1: si += n->val.v.i; n->val.tag = 2; n->val.v.d = 0.25; break;
			case 2: sd += n->val.v.d; n->val.tag = 3; n->val.v.s = "x"; break;
			case 3: sl += strlen(n->val.v.s); n->val.tag = 1; n->val.v.i = 7; break;
			}
		}
	}
	printf("%lld %g %zu\n", si, sd, sl);

	// The address of a member, a copy of a union, memset and memcpy.
	Value a;
	memset(&a, 0, sizeof a);
	char **ps = &a.s;
	*ps = "through a pointer";
	Value b = a;
	Value c;
	memcpy(&c, &b, sizeof c);
	printf("%s|%s|%s\n", a.s, b.s, c.s);
	void *vp = c.p;               // another pointer member: the same pointer
	printf("%s\n", (char *)vp);
	int64_t *pi = &a.i;
	*pi = 99;
	a.mem[1] = 1;
	printf("%lld %s\n", (long long)a.i, a.s ? "kept" : "null");
	TValue t = {4, {.p = &a}};
	show(&t);
	t.v.p = NULL;
	show(&t);
	return 0;
}
