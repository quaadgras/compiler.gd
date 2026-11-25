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

int main(void){ char *d = malloc(8); FILE *f = fopen("/dev/null", "w"); size_t n = (((size_t)-1) >> 2) + 3; if (fwrite(d, 4, n, f) == n) unsafe("fwrite of a count whose size wraps said it wrote it all"); ok(); }
