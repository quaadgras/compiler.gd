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

int main(void){ char *s = malloc(8), *t = malloc(8); memset(s, 65, 8); memset(t, 65, 8); if (strcmp(launder(s), t) == 0) unsafe("strcmp read past two heap objects with no end"); ok(); }
