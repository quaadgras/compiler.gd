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

static int cmp(const void *a, const void *b){ return *(const char *)b - *(const char *)a; }
int main(void){ char *d = malloc(64); for (int i = 0; i < 64; i++) d[i] = i; vn = 4096; qsort(d, 4, vn, cmp); unsafe("qsort of 4 elements of 4096 bytes in a 64-byte object"); }
