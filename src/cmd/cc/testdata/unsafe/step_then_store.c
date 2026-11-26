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

int main(void){ void *raw = malloc(64); char **pp = (char **)raw; for (int k = 0; k < 4; k++) pp[k] = 0; long *i = (long *)launder(raw); long s = 0; vn = 3; for (int k = 0; k < vn; k++) { s += *i; i++; } *i = (long)(intptr_t)victim + s; if (pp[3]) { *pp[3] = 88; if (victim[0] == 88) unsafe("a long * that was stepped while it was read stored an integer over a pointer, which was used"); } ok(); }
