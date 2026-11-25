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

int main(void){ char buf[16]; long canary[4] = {1,2,3,4}; char *p = buf; vn = 64; for (int i = 0; i < vn; i++) p[i] = 65; if (canary[0] != 1) unsafe("canary"); vn = p[vn - 1]; unsafe("wrote 64 bytes in a 16-byte local"); }
