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

static char *lo, *hi; static int out;
static int cmp(const void *k, const void *e){ if ((char *)e < lo || (char *)e >= hi) out = 1; return 1; }
int main(void){ char *d = malloc(64); lo = d; hi = d + 64; vn = 4096; int key = 0; bsearch(&key, d, vn, 16, cmp); if (out) unsafe("bsearch gave its comparison a pointer past the array"); ok(); }
