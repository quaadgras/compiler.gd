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

#include <signal.h>
int main(void){ long *d = malloc(64); d[0] = 0xc3c3c3c3c3c3c3c3; struct sigaction sa; memset(&sa, 0, sizeof sa); sa.sa_handler = (void (*)(int))launder(d); sigaction(SIGUSR1, &sa, NULL); raise(SIGUSR1); unsafe("sigaction took data for a handler"); }
