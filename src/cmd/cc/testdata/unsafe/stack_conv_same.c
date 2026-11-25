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

int main(void){ char *s = victim; long *lp = (long *)(void *)&s; *lp += 1; long x = (long)(intptr_t)victim; char **pp = (char **)(void *)&x; if (s == victim + 1 && *s == 'a') unsafe("a pointer on the stack was changed as a long"); **pp = 88; if (victim[0] == 88) unsafe("a long on the stack was used as a pointer"); ok(); }
