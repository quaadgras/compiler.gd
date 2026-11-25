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

static char *leak(void){ char buf[16]; strcpy(buf, "x"); uintptr_t u = (uintptr_t)buf; return (char *)u; }
static long spray(void){ volatile long a[64]; for (int i = 0; i < 64; i++) a[i] = 0x4141414141414141; return a[vn]; }
int main(void){ char *p = leak(); spray(); if (p[0] != 120) unsafe("a pointer to a local outlived its function"); p[0] = 1; ok(); }
