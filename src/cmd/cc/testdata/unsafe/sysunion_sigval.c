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
int main(void){ union sigval v; v.sival_ptr = 0; long *l = (long *)launder(&v); *l = (long)(intptr_t)victim; char *p = v.sival_ptr; *p = 88; if (victim[0] == 88) unsafe("a sigval was written as a long"); ok(); }
