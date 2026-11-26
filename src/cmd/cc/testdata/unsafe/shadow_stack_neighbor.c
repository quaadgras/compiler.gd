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
/* A step by a constant from a pointer to a variable of a frame, in
   another function, stays in the variable. */
__attribute__((noinline)) static void up3(long *p) { p[3] = 77; }
__attribute__((noinline)) static void up4(long *p) { p[4] = 77; }
__attribute__((noinline)) static void up6(long *p) { p[6] = 77; }
__attribute__((noinline)) static void down1(long *p) { p[-1] = 77; }
__attribute__((noinline)) static void down3(long *p) { p[-3] = 77; }

int main(void){ long a[2] = {0, 0}; long b[2] = {0, 0}; long c[2] = {0, 0}; long *pa = launder(a), *pb = launder(b), *pc = launder(c); vn = 0; switch (vn) { case 0: up3(pb); up4(pb); up6(pb); down1(pb); down3(pb); } if (pa[0] == 77 || pa[1] == 77 || pc[0] == 77 || pc[1] == 77) unsafe("a step out of a variable of a frame stored in another"); unsafe("a store out of a variable of a frame went through"); }
