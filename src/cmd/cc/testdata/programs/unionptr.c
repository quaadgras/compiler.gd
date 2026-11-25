#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// What C has of a union's address: it is each member's, through void *
// and back, and a member's is the union's.
typedef struct { int len; char *text; } Str;
typedef struct { char *name; long count; } Named;
typedef union {
	long i;
	double d;
	Str s;
	Named n;
	char *p;
} Value;
typedef struct { int tag; Value v; } Node;

static void set_str(void *v, char *text) {
	Str *s = v; // the union's address, as its member
	s->len = (int)strlen(text);
	s->text = text;
}

static const char *get_str(const void *v) { return ((const Str *)v)->text; }
static void set_ptr(void *v, char *p) { *(char **)v = p; }
static long get_long(void *v) { return *(long *)v; }
static Value *owner(void *member) { return member; } // a member's address, as the union's

static int by_len(const void *a, const void *b) {
	return ((const Value *)a)->s.len - ((const Value *)b)->s.len;
}

int main(void) {
	Node *n = malloc(sizeof *n);
	n->tag = 1;
	set_str(&n->v, "through void");
	printf("%d %s %s\n", n->v.s.len, n->v.s.text, get_str(&n->v));

	// A member's address is the union's.
	Str *sp = &n->v.s;
	Value *u = owner(sp);
	printf("%d %s\n", u == &n->v, u->s.text);
	Value *u2 = (Value *)(void *)&n->v.n; // of a member that it does not hold: the union still
	printf("%d\n", u2 == &n->v);

	// A pointer member, and an integer one, through void *.
	Value *w = calloc(1, sizeof *w);
	set_ptr(w, "a pointer");
	printf("%s\n", w->p);
	w->i = 42;
	printf("%ld\n", get_long(w));

	// An array of unions, sorted by the library through void *.
	Value *arr = calloc(4, sizeof *arr);
	const char *words[] = { "three", "a", "fifteen", "of" };
	for (int i = 0; i < 4; i++) set_str(&arr[i], (char *)words[i]);
	qsort(arr, 4, sizeof *arr, by_len);
	for (int i = 0; i < 4; i++) printf("%s ", arr[i].s.text);
	printf("\n");

	// In a variable that is moved to the heap, and in the program's data.
	static Value global;
	Value local;
	memset(&local, 0, sizeof local);
	set_str(&local, "local");
	set_str(&global, "global");
	printf("%s %s %s %s\n", local.s.text, get_str(&local), global.s.text, get_str(&global));
	return 0;
}
