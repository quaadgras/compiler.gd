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
/* An index from a pointer to a variable of a frame, in another
   function, stays in the variable. */
__attribute__((noinline)) static void put(long *p, long i) { p[i] = 77; }

int main(void){ long a[2] = {0, 0}; long b[2] = {0, 0}; long c[2] = {0, 0}; long *pa = launder(a), *pb = launder(b), *pc = launder(c); for (vn = 2; vn < 8; vn++) put(pb, vn); for (vn = 1; vn < 8; vn++) put(pb, -vn); if (pa[0] == 77 || pa[1] == 77 || pc[0] == 77 || pc[1] == 77) unsafe("an index out of a variable of a frame stored in another"); unsafe("a store out of a variable of a frame by an index went through"); }
