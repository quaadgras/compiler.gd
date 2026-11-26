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
/* An index takes a pointer to the same word of the next object, which
   is as far from where its object begins: it is still another object. */
__attribute__((noinline)) static void put(long *p, long i) { p[i] = 77; }

int main(void){ long *v[64]; for (int i = 0; i < 64; i++) { v[i] = malloc(24); v[i][0] = v[i][1] = v[i][2] = 0; } for (int i = 0; i < 64; i++) for (int j = 0; j < 64; j++) { long d = (char *)v[j] - (char *)v[i]; if (d > 0 && d <= 64 && d % 8 == 0) { vn = (int)(d / 8) + 1; put(launder(v[i]), vn); if (v[j][1] == 77) unsafe("an index from one object stored in the next"); unsafe("a store past an object by an index went through"); } } ok(); }
