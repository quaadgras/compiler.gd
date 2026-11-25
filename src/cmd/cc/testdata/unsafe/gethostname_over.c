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

#include <unistd.h>
int main(void){ char *d = malloc(1); vn = 4096; if (gethostname(d, vn) == 0 && d[0] != 0) unsafe("gethostname wrote the name in a 1-byte object"); ok(); }
