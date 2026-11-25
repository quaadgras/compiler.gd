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

/* (It calls itself so that it is a function of its own.) */
static void fill64(void *p, int n) { if (n > 0) { fill64(p, n - 1); return; } char (*a)[64] = (char (*)[64])p; for (int i = 0; i < vn; i++) (*a)[i] = 65; }
int main(void){ long canary[4] = {1,2,3,4}; char buf[16]; long below[4] = {5,6,7,8}; vn = 64; fill64(buf, 2); if (canary[0] != 1 || below[0] != 5) unsafe("canary"); unsafe("wrote 64 bytes in a 16-byte local through a char[64]"); }
