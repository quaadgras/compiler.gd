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

int main(void){ const char *s = launder("abc"); vn = 5000; long sum = 0; for (int i = 0; i < vn; i++) sum += s[i]; printf("%ld\n", sum); unsafe("read 5000 bytes of a 4-byte string literal"); }
