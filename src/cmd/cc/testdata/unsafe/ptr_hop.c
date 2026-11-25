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

int main(void){ char *a = malloc(16), *b = malloc(16); b[0] = 1; ptrdiff_t d = b - a; char *c = a + d; *c = 88; if (b[0] == 88) unsafe("pointer arithmetic went from one object to another"); ok(); }
