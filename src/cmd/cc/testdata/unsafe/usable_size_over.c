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

#include <malloc.h>
int main(void){ char *p = malloc(5); size_t n = malloc_usable_size(p); vn = 2; p[n - 1 + vn] = 88; unsafe("stored past what malloc_usable_size says"); }
