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

int main(void){ char *e = getenv("PATH"); if (!e) ok(); vn = 1 << 20; long s = 0; for (int i = 0; i < vn; i++) s += e[i]; printf("%ld\n", s); unsafe("read a megabyte past an environment string"); }
