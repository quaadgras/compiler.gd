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

static long give(void) { return (long)(intptr_t)victim; }
int main(void){ char *(*f)(void) = (char *(*)(void))launder((void *)give); char *p = f(); *p = 88; if (victim[0] == 88) unsafe("a function that returns an integer was called for a pointer"); ok(); }
