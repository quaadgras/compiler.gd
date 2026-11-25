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
int main(void){ void *block = malloc(sizeof(struct a)); struct a *a = block; a->p = victim; a->n = 1;
 struct b *b = block; b->x = (long)(intptr_t)victim + 1; b->q = 0;
 *a->p = 88; if (victim[1] == 88) unsafe("a pointer that was converted before the memory was used again stored through an integer"); ok(); }
