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

int main(void){ char out[64]; long x = 0; snprintf(out, sizeof out, "abcd%n", (long)(intptr_t)&x); if (x == 4) unsafe("printf %n stored through an integer"); ok(); }
