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

static void fill(char *p, int n) { for (int i = 0; i < n; i++) p[i] = 65; }
static void f(void){ char buf[16]; vn = 16; fill(buf, vn + 200); vn = buf[vn + 100]; }
int main(void){ f(); unsafe("a callee wrote 216 bytes in a 16-byte local of its caller"); }
