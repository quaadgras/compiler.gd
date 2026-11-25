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

#include <sys/select.h>
int main(void){ char *d = calloc(1, 2); struct timeval tv = {0, 0}; select(1024, (fd_set *)launder(d), NULL, NULL, &tv); unsafe("select of 1024 descriptors in a 2-byte set"); }
