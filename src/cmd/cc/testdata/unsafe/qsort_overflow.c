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

static int cmp(const void *a, const void *b){ return *(const char *)a - *(const char *)b; }
int main(void){ char *d = malloc(64); for (int i = 0; i < 64; i++) d[i] = 64 - i; size_t n = (((size_t)-1) >> 2) + 5; qsort(d, n, 4, cmp); unsafe("qsort of a count whose size in bytes wraps"); }
