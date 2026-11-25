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

union u { long i; char *p; };
int main(void){ union u u; u.i = (long)(intptr_t)victim; union u *q = launder(&u); *q->p = 88; if (victim[0] == 88) unsafe("the integer of a union on the stack was used as its pointer"); ok(); }
