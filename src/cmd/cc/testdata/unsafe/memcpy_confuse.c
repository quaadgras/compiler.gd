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

struct big { long a[4096]; }; struct A { int *p; }; struct B { struct big *q; };
int main(void){ struct A *a = malloc(sizeof *a); struct B *b = malloc(sizeof *b); a->p = malloc(sizeof(int)); b->q = 0; memcpy(b, a, sizeof *a); vn = 1000; b->q->a[vn] = 1; unsafe("a pointer copied by memcpy over one of another type was stored through, past its object"); }
