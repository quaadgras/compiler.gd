// File-scope initializers: constants, strings, nested aggregates,
// designators, addresses (of earlier and later objects, and an object's
// own), tables of functions, unions and bit-fields.
#include <stdio.h>
#include <string.h>

int counter;
int table[10] = {1, 2, 3};
const char *greeting = "hello";
double ratio = 2.5;
unsigned char flags = 0x81;
long big = -9000000000L;
char name[8] = "abc";
char full[] = "initialized";
float half = 0.5f;

struct point {
	long x, y;
	char label[8];
};
struct point origin = {0, 0, "origin"};
struct point grid[3] = {{1, 2, "a"}, [2] = {.y = 7, .label = "c"}};

struct node {
	int v;
	struct node *next;
};
extern struct node second;
struct node first = {1, &second};  // an object declared later
struct node second = {2, &first};  // one declared before
struct node self = {3, &self};     // its own
int *into = &table[4];
const char *words[] = {"alpha", "beta", 0};

static int twice(int x) { return 2 * x; }
static int thrice(int x) { return 3 * x; }
int (*ops[2])(int) = {twice, thrice};
struct { const char *n; int (*f)(int); } named[] = {{"twice", twice}, {"thrice", thrice}};

union num {
	int i;
	float f;
};
union num un = {.f = 1.5f};
struct bits {
	unsigned a : 3, b : 5;
	int rest;
} bf = {5, 17, -1};

int main(void) {
	static int calls = 41;
	static struct point local = {9, 8, "loc"};
	calls++;
	printf("%d %d %d %d %s %g %d %ld %s %s %g\n", counter, table[0], table[2], table[9], greeting, ratio, flags, big, name, full, half);
	printf("%ld %ld %s | %ld %ld %s | %ld %ld [%s] | %ld %ld %s\n", origin.x, origin.y, origin.label, grid[0].x, grid[0].y, grid[0].label, grid[1].x, grid[1].y, grid[1].label, grid[2].x, grid[2].y, grid[2].label);
	printf("%d %d %d %d %d\n", first.next->v, second.next->v, self.next->v, self.next == &self, (int)(into - table));
	printf("%s %s %d %zu\n", words[0], words[1], words[2] == 0, sizeof(words) / sizeof(words[0]));
	printf("%d %d %s %d %s %d\n", ops[0](5), ops[1](5), named[0].n, named[0].f(4), named[1].n, named[1].f(4));
	printf("%g %u %u %d %d %ld %s\n", un.f, bf.a, bf.b, bf.rest, calls, local.x + local.y, local.label);
	return 0;
}
