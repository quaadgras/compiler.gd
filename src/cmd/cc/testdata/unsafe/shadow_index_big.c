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
/* An object of more words than the shadow counts is indexed to its
   end, and no further. */
__attribute__((noinline)) static void put(long *p, long i) { p[i] = 77; }
__attribute__((noinline)) static long get(long *p, long i) { return p[i]; }

int main(void){ long *p = malloc(300 * sizeof(long)); long *q = launder(p); vn = 299; put(q, vn); put(q, 280); put(q, 3); if (get(q, vn) != 77 || get(q, 280) != 77 || get(q, 3) != 77) unsafe("a large object does not hold what was stored in it"); vn = 300; put(q, vn + 2); unsafe("a store past a large object by an index went through"); }
