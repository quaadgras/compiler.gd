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

long gx;
int main(void){ gx = (long)(intptr_t)victim; char **pp = (char **)launder(&gx); **pp = 88; if (victim[0] == 88) unsafe("a global long was used as a pointer"); ok(); }
