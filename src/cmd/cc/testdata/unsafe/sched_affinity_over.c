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

#define _GNU_SOURCE
#include <sched.h>
int main(void){ char *d = malloc(1); vn = 4096; if (sched_getaffinity(0, vn, (cpu_set_t *)launder(d)) == 0) unsafe("sched_getaffinity wrote 4096 bytes in a 1-byte object"); ok(); }
