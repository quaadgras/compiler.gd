#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stddef.h>
static volatile int vn; /* defeats constant folding */
static void *volatile vg;
static void *launder(void *p) { vg = p; return vg; }
static void unsafe(const char *what) { printf("UNSAFE: %s\n", what); exit(0); }
static void ok(void) { printf("OK\n"); exit(0); }
char victim[16] = "safe";

static long get(void *p, int n) { if (n > 0) return get(p, n - 1); return *(long *)p; }
int main(void){ int below = 2; int x = 1; int above = 3; long v = get(&x, 2); if (v == 1 || below + above != 5) ok(); unsafe("an int on the stack was read as a long"); }
