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

int main(void){ char *a = malloc(16), *b = malloc(16); b[0] = 1; uintptr_t u = (uintptr_t)a; u += (uintptr_t)b - (uintptr_t)a; char *c = (char *)u; *c = 88; if (b[0] == 88) unsafe("uintptr_t arithmetic went from one object to another"); ok(); }
