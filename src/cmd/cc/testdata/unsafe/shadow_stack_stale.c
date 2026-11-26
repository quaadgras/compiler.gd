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
/* What a frame that returned said of its variables says nothing of the
   variables of the frame that is there next. */
__attribute__((noinline)) static long first(void) { long big[16]; long *p = launder(big); for (int i = 0; i < 16; i++) p[i] = i; return p[vn]; }
__attribute__((noinline)) static void poke(long *p) { p[5] = 77; }
__attribute__((noinline)) static long second(void) { long small[2] = {0, 0}; long *p = launder(small); poke(p); return p[0]; }

int main(void){ long s = first(); s += second(); if (s >= 0) unsafe("a store past a small variable, where a larger one had been, went through"); ok(); }
