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

#include <time.h>
int main(void){ char *d = malloc(2); time_t t = 0; vn = 4096; if (strftime(d, vn, "%Y-%m-%d %H:%M:%S", gmtime(&t)) > 2) unsafe("strftime wrote a date in a 2-byte object"); ok(); }
