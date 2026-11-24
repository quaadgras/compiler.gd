// What malloc gives has no type: pointers are kept in it wherever C puts
// them, and what they point to lives as long as it is reached that way.
// Garbage is made all along, so that the collector runs many times.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

typedef struct node {
	struct node *next;
	long val;
	char *name;
} node;

// churn makes garbage of every size, with words in it that look like
// nothing in particular.
static unsigned long churned;
static void churn(int n) {
	for (int i = 0; i < n; i++) {
		size_t size = 1 + (size_t)(i * 37 % 3000);
		unsigned char *p = malloc(size);
		memset(p, i, size);
		churned += p[size - 1];
		if (i % 64 == 0) {
			void *big = malloc(40000 + i);
			memset(big, 1, 40000 + i);
		}
	}
}

// An arena: nodes are placed in a block of bytes, and the names they
// point to are reached from nowhere else.
static char *arena;
static size_t arena_used, arena_size;

static void *arena_alloc(size_t n) {
	n = (n + 15) & ~(size_t)15;
	if (arena_used + n > arena_size) {
		arena_size = arena_size ? arena_size * 2 : 256;
		while (arena_used + n > arena_size)
			arena_size *= 2;
		arena = realloc(arena, arena_size);
	}
	void *p = arena + arena_used;
	arena_used += n;
	return p;
}

static char *name_of(long i) {
	char buf[32];
	int n = snprintf(buf, sizeof buf, "node-%ld", i);
	char *s = malloc(n + 1);
	memcpy(s, buf, n + 1);
	return s;
}

static long list_sum(node *head, int *count) {
	long sum = 0;
	*count = 0;
	for (node *p = head; p; p = p->next) {
		long v;
		if (sscanf(p->name, "node-%ld", &v) != 1 || v != p->val) {
			printf("bad name %s for %ld\n", p->name, p->val);
			exit(1);
		}
		sum += p->val;
		(*count)++;
	}
	return sum;
}

int main(void) {
	// A list whose nodes come from malloc without a type.
	node *head = NULL;
	for (long i = 0; i < 2000; i++) {
		void *mem = malloc(sizeof(node));
		node *n = mem;
		n->val = i;
		n->name = name_of(i);
		n->next = head;
		head = n;
		if (i % 100 == 0)
			churn(2000);
	}
	int count;
	long sum = list_sum(head, &count);
	printf("list: %d nodes, sum %ld\n", count, sum);

	// The same in an arena that grows: realloc moves the nodes, and the
	// offsets stand for the links.
	size_t *offs = NULL;
	int noffs = 0;
	for (long i = 0; i < 3000; i++) {
		size_t before = arena_used;
		node *n = arena_alloc(sizeof(node));
		n->val = i;
		n->name = name_of(i);
		n->next = NULL;
		offs = realloc(offs, (noffs + 1) * sizeof *offs);
		offs[noffs++] = (size_t)((char *)n - arena);
		(void)before;
		if (i % 150 == 0)
			churn(2000);
	}
	sum = 0;
	for (int i = 0; i < noffs; i++) {
		node *n = (node *)(arena + offs[i]);
		long v;
		if (sscanf(n->name, "node-%ld", &v) != 1 || v != n->val) {
			printf("bad arena name %s for %ld\n", n->name, n->val);
			return 1;
		}
		sum += v;
	}
	printf("arena: %d nodes in %zu bytes, sum %ld\n", noffs, arena_used, sum);

	// A table of pointers that is large, grown by realloc, copied by
	// memcpy, and then the only way to what it points to.
	size_t cap = 4, len = 0;
	char **table = malloc(cap * sizeof *table);
	for (long i = 0; i < 60000; i++) {
		if (len == cap) {
			cap *= 2;
			table = realloc(table, cap * sizeof *table);
		}
		table[len++] = name_of(i);
		if (i % 5000 == 0)
			churn(1000);
	}
	void *copy = malloc(len * sizeof *table);
	memcpy(copy, table, len * sizeof *table);
	memset(table, 0, len * sizeof *table);
	table = NULL;
	churn(20000);
	char **t2 = copy;
	sum = 0;
	for (size_t i = 0; i < len; i++) {
		long v;
		if (sscanf(t2[i], "node-%ld", &v) != 1 || v != (long)i) {
			printf("bad table name %s at %zu\n", t2[i], i);
			return 1;
		}
		sum += v;
	}
	printf("table: %zu names (%zu bytes), sum %ld\n", len, len * sizeof *table, sum);

	// Integers that are addresses of what is gone, copied to where
	// pointers may be: they are not pointers, and nothing is found
	// at them.
	uintptr_t *stale = malloc(4096 * sizeof *stale);
	for (int i = 0; i < 4096; i++) {
		char *p = malloc(24 + i % 200);
		stale[i] = (uintptr_t)p + (i & 7);
	}
	churn(20000);
	void *raw = malloc(4096 * sizeof *stale);
	memcpy(raw, stale, 4096 * sizeof *stale);
	node *live = malloc(sizeof *live);
	live->val = 42;
	live->name = name_of(42);
	void **slots = raw;
	slots[17] = live;
	live = NULL;
	churn(20000);
	uintptr_t x = 0;
	for (int i = 0; i < 4096; i++)
		if (i != 17)
			x += ((uintptr_t *)raw)[i] == stale[i];
	live = slots[17];
	printf("stale: %lu the same, and %s is %ld\n", (unsigned long)x, live->name, live->val);

	// The end of an object is a pointer of its own.
	int *a = malloc(1000 * sizeof *a);
	int *end = a + 1000;
	long total = 0;
	for (int *p = a; p != end; p++)
		*p = (int)(p - a);
	for (int *p = end; p != a;)
		total += *--p;
	printf("ends: %ld\n", total);

	// Nothing of any size.
	char *z = malloc(0);
	printf("zero: %d\n", z != NULL || z == NULL);
	printf("churned: %lu\n", churned);
	return 0;
}
