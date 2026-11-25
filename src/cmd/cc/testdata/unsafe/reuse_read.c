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
struct a { char *p; long n; }; struct b { long x; char *q; };
static struct a *keep;
int main(void){ void *block = malloc(sizeof(struct a)); keep = block; keep->p = victim; keep->n = (long)(intptr_t)victim;
 struct b *b = block; char *got = b->q; if (got) { *got = 88; if (victim[0] == 88) unsafe("an integer was read as a pointer after the memory was used again"); }
 b->x = 5; b->q = victim; long n = keep->n; printf("%%ld\\n", n == (long)(intptr_t)victim); ok(); }
