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
/* A stack that was copied to a larger one still has its variables. */
__attribute__((noinline)) static void poke(long *p) { p[2] = 1; p[6] = 77; }
__attribute__((noinline)) static long deep(int n, long *p) { char pad[512]; char *q = launder(pad); q[0] = (char)n; if (n == 0) { poke(p); return q[0]; } return deep(n - 1, p) + q[0]; }

int main(void){ long buf[4] = {0, 0, 0, 0}; long other[4] = {0, 0, 0, 0}; long *o = launder(other); deep(4000, launder(buf)); if (o[0] == 77 || o[1] == 77 || o[2] == 77) unsafe("a store past a variable of a stack that had grown stored in another"); unsafe("a store past a variable of a stack that had grown went through"); }
