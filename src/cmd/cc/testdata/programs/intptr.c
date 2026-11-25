#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>

typedef struct Node { struct Node *next; uintptr_t tagged; intptr_t off; int v; } Node;

static uintptr_t keep;       // a pointer, in an integer of the program's data
static uintptr_t table[8];   // and in an array of them

int main(void) {
	Node *n = malloc(sizeof *n);
	n->v = 7;
	// A pointer through uintptr_t and back.
	uintptr_t u = (uintptr_t)n;
	Node *m = (Node *)u;
	printf("%d %d\n", m == n, m->v);
	// With a tag in its low bits, as programs keep one.
	uintptr_t t = u | 3;
	printf("%d %d\n", (int)(t & 3), ((Node *)(t & ~(uintptr_t)3))->v);
	// Kept in memory, where only the integer keeps the object.
	keep = (uintptr_t)malloc(sizeof(Node));
	((Node *)keep)->v = 9;
	for (int i = 0; i < 8; i++) {
		Node *x = malloc(sizeof *x);
		x->v = i;
		x->tagged = (uintptr_t)x | 1;
		table[i] = (uintptr_t)x;
	}
	for (int i = 0; i < 20000; i++) free(malloc(64 + i % 512)); // garbage, and collections
	int sum = ((Node *)keep)->v;
	for (int i = 0; i < 8; i++) {
		Node *x = (Node *)table[i];
		sum += x->v + (int)(x->tagged & 1) + (((Node *)(x->tagged - 1)) == x);
	}
	printf("%d\n", sum);
	// Arithmetic that stays in the object, and integers.
	char *buf = malloc(64);
	uintptr_t b = (uintptr_t)buf;
	char *mid = (char *)(b + 32);
	*mid = 'x';
	printf("%c %d %d\n", buf[32], (int)((uintptr_t)mid - b), (int)((b + 63) / 64 * 64 >= b));
	intptr_t neg = -5;
	uintptr_t big = 1000000007u;
	printf("%ld %lu %ld %d\n", (long)neg, (unsigned long)(big * 3 % 1000), (long)(neg * 2 + (intptr_t)big), neg < 0);
	// An intptr_t that is a distance.
	n->off = (intptr_t)((char *)&n->v - (char *)n);
	printf("%d\n", *(int *)((char *)n + n->off));
	// Sentinels.
	void *bad = (void *)(intptr_t)-1;
	void *one = (void *)(uintptr_t)1;
	printf("%d %d %d\n", bad == (void *)-1, one != NULL, (int)(uintptr_t)one);
	return 0;
}
