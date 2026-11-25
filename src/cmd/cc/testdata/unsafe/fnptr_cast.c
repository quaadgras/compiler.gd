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

static void take(char **pp) { **pp = 88; }
int main(void){ void (*f)(long) = (void (*)(long))launder((void *)take); f((long)(intptr_t)victim); if (victim[0] == 88) unsafe("a function was called with an integer for its pointer"); ok(); }
