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
#include <stdatomic.h>
int main(void){ long *cell = malloc(sizeof(long)); *cell = 5; _Atomic(char *) *ap = (_Atomic(char *) *)launder(cell); long *again = (long *)launder(cell); atomic_store(ap, victim); *again += 1; char *p = atomic_load(ap); *p = 88; if (victim[1] == 88) unsafe("a pointer that was stored atomically was changed as a long"); ok(); }
