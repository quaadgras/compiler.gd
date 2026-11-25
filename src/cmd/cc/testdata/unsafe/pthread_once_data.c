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
int main(void){ long *d = malloc(64); d[0] = 0xc3c3c3c3c3c3c3c3; pthread_once_t once = PTHREAD_ONCE_INIT; pthread_once(&once, (void (*)(void))launder(d)); unsafe("pthread_once ran data as a function"); }
