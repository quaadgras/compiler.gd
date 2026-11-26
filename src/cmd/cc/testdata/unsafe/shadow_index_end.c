#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stddef.h>
static volatile int vn; /* defeats constant folding */
static void *volatile vg;
static void *launder(void *p) { vg = p; return vg; }
static void unsafe(const char *what) { printf("UNSAFE: \n", what); exit(0); }
static void ok(void) { printf("OK\n"); exit(0); }
/* The word after the last of an object is not the object's. */
__attribute__((noinline)) static void put(long *p, long i) { p[i] = 77; }

int main(void){ long *p = malloc(4 * sizeof(long)); vn = 4; put(launder(p), vn); unsafe("a store at the end of an object by an index went through"); }
