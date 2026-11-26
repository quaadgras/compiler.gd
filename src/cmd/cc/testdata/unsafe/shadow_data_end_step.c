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
long ga[4], gb[4], gc[4], gd[4];

/* The end of a variable of the data is where the next one begins: a
   step from the pointer to the end is not a step in the next one. */
__attribute__((noinline)) static void step(long *end) { end[1] = 77; }

int main(void){ long *v[4] = { ga, gb, gc, gd }; for (int i = 0; i < 4; i++) for (int j = 0; j < 4; j++) if (v[i] + 4 == v[j]) { long *e = (long *)launder(v[i]) + 4; step(e); if (v[j][1] == 77) unsafe("a step from the end of a variable stored in the next one"); } ok(); }
