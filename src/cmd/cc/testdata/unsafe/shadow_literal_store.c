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
/* Nothing is stored in a string literal. */
__attribute__((noinline)) static void put(char *w) { w[1] = 'X'; }
int main(void){ const char *s = "literal bytes"; put(launder((void *)s)); if (s[1] == 'X') unsafe("a byte was stored in a string literal"); ok(); }
