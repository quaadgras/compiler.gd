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

int main(void){ char *d = malloc(8); vn = 4096; FILE *f = fmemopen(d, vn, "w"); for (int i = 0; i < 100; i++) fputc(65, f); fflush(f); unsafe("fmemopen wrote 100 bytes in an 8-byte object"); }
