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

/* The local is three frames up from the store. */
static void put(char *p, int i) { if (i < 0) put(p, i); p[i] = 65; }
static void mid(char *p, int n, int depth) { if (depth > 0) { mid(p, n, depth - 1); return; } for (int i = 0; i < n; i++) put(p, i); }
int main(void){ long canary[4] = {1,2,3,4}; char buf[16]; vn = 16; mid(buf, vn, 2); if (buf[15] != 65) unsafe("the local was not written"); mid(buf, vn + 64, 2); if (canary[0] != 1) unsafe("canary"); unsafe("a callee's callee wrote 80 bytes in a 16-byte local"); }
