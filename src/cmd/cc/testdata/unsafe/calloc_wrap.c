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

int main(void){ size_t n = ((size_t)1 << 62) + 2; int *p = calloc(n, 4); if (p) { vn = 16; p[vn] = 88; unsafe("calloc of a count whose size wraps gave an object, and 16 ints fit in it"); } ok(); }
