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

int main(void){ long canary[4] = {1,2,3,4}; char buf[16]; long below[4] = {5,6,7,8}; char *p = buf; vn = 64; for (int i = 1; i <= vn; i++) p[-i] = 65; if (canary[0] != 1 || below[0] != 5) unsafe("canary"); vn = p[-vn]; unsafe("wrote 64 bytes before a 16-byte local"); }
