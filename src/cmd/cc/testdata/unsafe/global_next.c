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

long ga[2] = {1, 2}; long gb[2] = {3, 4}; long gc[2] = {5, 6};
int main(void){ long *p = launder(gb); vn = 3; long *q = p + vn; *q = 77; long *r = p - vn; *r = 78; if (ga[0] == 77 || ga[1] == 77 || gc[0] == 77 || gc[1] == 77 || ga[0] == 78 || ga[1] == 78 || gc[0] == 78 || gc[1] == 78) unsafe("arithmetic from one global reached the next"); unsafe("wrote past a global"); }
