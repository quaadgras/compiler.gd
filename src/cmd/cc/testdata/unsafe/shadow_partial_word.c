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
/* The last word of an object of 12 bytes is not whole in it. */
int main(void){ char *raw = malloc(12); long *l = launder(raw); l[1] = 0x7777777777777777; unsafe("a long was stored in the last four bytes of an object and the four after"); }
