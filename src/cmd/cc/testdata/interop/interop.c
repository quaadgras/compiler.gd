// C that shares its memory with Go (demo/interop/main.go).
#include <stdlib.h>
#include <string.h>

#include "interop.h"



// C allocates; Go walks the list with plain field loads.
struct node *make_list(int n) {
	struct node *head = 0;
	for (int i = n; i > 0; i--) {
		struct node *x = malloc(sizeof *x);
		x->v = i;
		x->next = head;
		head = x;
	}
	return head;
}

// Go allocates; C walks the list, deriving each capability from the address.
long sum_list(struct node *n) {
	long s = 0;
	while (n) {
		s += n->v;
		n = n->next;
	}
	return s;
}

// C indexes memory that Go gave it: checked against the derived capability.
int read_at(int p[], int i) { return p[i]; }

// C stores a pointer outside its object where Go can see it: the word is
// tagged, so that Go cannot use it, and C keeps its capability.
void store_past(struct holder *h, int arr[], int n) { h->random_int = arr + n + 3; }

// A holder that C allocates.
struct holder *new_holder(void) { return malloc(sizeof(struct holder)); }

// C loads it back and brings it into bounds.
int load_back(struct holder *h, int k) {
	int *p = h->random_int - k;
	return *p;
}

// One-based arrays: a pointer before its object, in a register only.
int one_based_sum(int a[], int n) {
	int *b = a - 1;
	int s = 0;
	for (int i = 1; i <= n; i++)
		s += b[i];
	return s;
}

// An integer written over a pointer word: Go would read it as a pointer.
void pun(struct holder *h) {
	long *w = (long *)h;
	*w = 0x1234;
}

// A pointer made of an integer has no capability.
int forge(long addr) {
	int *p = (int *)addr;
	return *p;
}

// A pointer stored in memory with no pointer words (malloc'd as bytes).
void hide(struct node *n) {
	char *buf = malloc(16);
	struct node **slot = (struct node **)buf;
	*slot = n;
}

// C's locals on the stack: an integer over a pointer word of a local.
int stack_pun(void) {
	struct node a, b;
	long *p = (long *)&a.next;
	a.v = 1;
	b.v = 2;
	a.next = &b;
	*p = 0x41414141;
	return a.v;
}

// A pointer of the wrong type in a typed field: n->next to an int.
void confuse(struct node *n, int *x) { n->next = (struct node *)x; }

// ...which C can still use as what it is.
int unconfuse(struct node *n) { return *(int *)n->next; }

// C writes words of whatever Go passed it.
void poke(void *p, int i, long v) { ((long *)p)[i] = v; }

// Go passes a pointer to its stack; C only reads through it.
long sum_pair(struct node *n) { return n->v + n->next->v; }

void swap_ints(int *a, int *b) {
	int t = *a;
	*a = *b;
	*b = t;
}

// Address-taken locals and a local struct: all on the stack.
int locals(void) {
	int x = 1, y = 2;
	struct node a;
	a.v = 3;
	a.next = 0;
	swap_ints(&x, &y);
	return x * 100 + y * 10 + a.v;
}

// A slice's whole array, kept in an int* field.
void keep_array(struct holder *h, int a[]) { h->random_int = a; }

int at(struct holder *h, int i) { return h->random_int[i]; }

// A tagged union, like Godot's Variant: the same words hold a long, a
// double or a pointer.


struct variant *make_variant(int n) {
	struct variant *v = malloc(sizeof *v);
	v->type = 2;
	v->v.n = make_list(n);
	return v;
}

long variant_sum(struct variant *v) {
	if (v->type == 2)
		return sum_list(v->v.n);
	return v->v.i;
}

void variant_set_double(struct variant *v, double d) {
	v->type = 1;
	v->v.d = d;
}

// Reads the union as a pointer whatever it holds.
long variant_misread(struct variant *v) { return v->v.n->v; }

// Objects placed in untyped memory, as placement new does in a pool.
struct node *in_pool(void) {
	char *pool = malloc(64);
	struct node *a = (struct node *)pool;
	struct node *b = (struct node *)(pool + 16);
	a->v = 1;
	a->next = b;
	b->v = 2;
	b->next = make_list(3);
	return a;
}

// The pool's objects live while C keeps a pointer to them.
struct node *pool_root;

void make_pool(void) { pool_root = in_pool(); }

long pool_sum(void) { return sum_list(pool_root); }

// free and realloc take what malloc returned, and NULL.
int free_mallocd(void) {
	char *raw = malloc(16), *none = malloc(0), *s = strdup("x");
	int *typed = malloc(4 * sizeof *typed);
	double *aligned = aligned_alloc(64, 64);
	struct node *n = calloc(1, sizeof *n);
	raw = realloc(raw, 32);
	typed = realloc(typed, 8 * sizeof *typed);
	free(raw), free(none), free(s), free(typed), free(aligned), free(n), free(NULL);
	free_sized(malloc(8), 8);
	return realloc(NULL, 8) != NULL;
}

void free_interior(void) {
	char *p = malloc(16);
	free(p + 1);
}

void free_local(void) {
	int x = 0;
	free(&x);
}

// A pointer that went through a long has no capability: free cannot tell
// it from malloc's, and realloc cannot copy its object.
void free_lost(void) { free((void *)(long)malloc(8)); }

void realloc_lost(void) { realloc((void *)(long)malloc(8), 16); }

void realloc_interior(void) {
	char *p = malloc(16);
	realloc(p + 8, 32);
}

void free_go(int *p) { free(p); }
