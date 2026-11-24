// C that shares its memory with Go (main.go).
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

// Go allocates; C walks the list.
long sum_list(struct node *n) {
	long s = 0;
	while (n) {
		s += n->v;
		n = n->next;
	}
	return s;
}

// C indexes memory that Go gave it.
int read_at(int p[], int i) { return p[i]; }

// A holder that C allocates, and a slice's array kept in its int* field.
struct holder *new_holder(void) { return malloc(sizeof(struct holder)); }
void keep_array(struct holder *h, int a[]) { h->ints = a; }
int at(struct holder *h, int i) { return h->ints[i]; }

// A pointer stored in memory that malloc was given no type for.
static char *hidden;
void hide(struct node *n) {
	hidden = malloc(16);
	struct node **slot = (struct node **)hidden;
	*slot = n;
}
long hidden_sum(void) { return sum_list(*(struct node **)hidden); }

// C writes words of whatever Go passed it.
void poke(void *p, int i, long v) { ((long *)p)[i] = v; }

void *as_void(node_t *n) { return n; }

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

// A variable of C's that Go sets.
int counter;
int count(void) { return counter++; }

enum color next_color(enum color c) { return c == BLUE ? RED : c == RED ? GREEN : BLUE; }

char *shout(char *s) {
	for (char *p = s; *p; p++)
		if (*p >= 'a' && *p <= 'z')
			*p -= 'a' - 'A';
	return s;
}

// The pointer one past the end of an array, which is all that is kept
// of it while it is summed, last to first.
int backwards_sum(int n) {
	int *a = malloc(n * sizeof *a);
	for (int i = 0; i < n; i++)
		a[i] = i + 1;
	int *first = a, *p = a + n, s = 0;
	a = 0;
	while (p != first)
		s += *--p;
	return s;
}

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
void variant_set_long(struct variant *v, long i) {
	v->type = 0;
	v->v.i = i;
}

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
