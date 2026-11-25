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

/* (They call themselves so that they are functions of their own.) */
static void bump(void *p, int n) { if (n > 0) { bump(p, n - 1); return; } long *lp = (long *)p; *lp += 1; }
int main(void){ char *s = victim; bump(&s, 2); if (s == victim + 1 && *s == 'a') unsafe("a pointer on the stack was changed as a long"); ok(); }
