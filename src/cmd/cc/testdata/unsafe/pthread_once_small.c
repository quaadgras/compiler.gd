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

#include <pthread.h>
static void f(void){}
int main(void){ char *d = calloc(1, 2); pthread_once((pthread_once_t *)launder(d), f); unsafe("pthread_once used a 2-byte object as its flag"); }
