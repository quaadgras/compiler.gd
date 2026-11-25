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

#include <sys/random.h>
int main(void){ char *d = malloc(8); vn = 4096; if (getrandom(d, vn, 0) == 4096) unsafe("getrandom of 4096 bytes in an 8-byte object"); ok(); }
