// C23 that gcc -std=gnu2x also compiles: the output must match gcc's.
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

// Attribute declarations; an attribute may be empty.
[[]];
[[, vendor::attr, ]];
[[gnu::unused, , maybe_unused]] static int spare;

// Attributes after a function declarator appertain to its type.
int square(int x) [[unsequenced]];
int square(int x) [[unsequenced]] { return x * x; }
static int ident(int x) [[reproducible]] [[vendor::attr(1, 2)]];
static int ident(int x) { return x; }
static int (*pick(int which) [[vendor::attr]])(int) { return which ? square : ident; }

// After an array declarator they appertain to the array type; GNU
// attributes there are the declaration's.
static int table[4] [[vendor::attr]] = {1, 2, 3, 4};
static char wide[3] __attribute__((aligned(64)));

// A member's attributes after its declarator are the member's.
struct members {
	char c;
	int x __attribute__((aligned(16)));
	char d;
	int y __attribute__((packed));
	short z [[gnu::aligned(8)]];
	char e[3] __attribute__((aligned(4)));
	int bits : 3 __attribute__((packed));
};

int main(void) {
	[[]];
	[[vendor::attr]];
	int local[2] [[vendor::attr]] = {5, 6};
	void (*fp)(void) [[vendor::attr]] = 0;
	printf("square %d, ident %d, pick %d %d\n", square(3), ident(4), pick(1)(5), pick(0)(5));
	printf("table %d, local %d, fp %d\n", table[3], local[1], fp == 0);
	printf("aligned %d\n", (int)((uintptr_t)wide % 64));
	printf("members: size %zu, align %zu, x %zu, d %zu, y %zu, z %zu, e %zu\n", sizeof(struct members),
		alignof(struct members), offsetof(struct members, x), offsetof(struct members, d),
		offsetof(struct members, y), offsetof(struct members, z), offsetof(struct members, e));
	return 0;
}
