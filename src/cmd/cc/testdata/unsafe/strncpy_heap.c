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

int main(void){ char *d = malloc(8); vn = 64; strncpy(d, launder("this string is a good deal longer than eight bytes, and then some more of it"), vn); unsafe("strncpy of 64 bytes to an 8-byte object"); }
