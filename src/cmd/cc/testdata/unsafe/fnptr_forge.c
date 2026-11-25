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

static void take(char *p){ *p = 88; }
int main(void){ void **d = malloc(64); void **src = (void **)launder((void *)take); d[0] = src[0]; d[1] = victim; void (*f)(char *) = (void (*)(char *))launder(d); f(victim); unsafe("a function was made of two pointers and called"); }
