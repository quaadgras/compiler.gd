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

static void poke(void *p, int n) { if (n > 0) { poke(p, n - 1); return; } char **pp = (char **)p; **pp = 88; }
int main(void){ long x = (long)(intptr_t)victim; poke(&x, 2); if (victim[0] == 88) unsafe("a long on the stack was used as a pointer"); ok(); }
