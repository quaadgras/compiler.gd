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

/* (It calls itself so that it is a function of its own, with its own frame.) */
static void under(char *p, int n) { if (n == 0) return; p[-n] = 65; under(p, n - 1); }
int main(void){ long canary[4] = {1,2,3,4}; char buf[16]; long below[4] = {5,6,7,8}; vn = 64; under(buf, vn); if (canary[0] != 1 || below[0] != 5) unsafe("canary"); unsafe("a callee wrote 64 bytes before a 16-byte local of its caller"); }
