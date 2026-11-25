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

#include <stdarg.h>
static long get(int n, ...) { va_list ap; va_start(ap, n); long a = va_arg(ap, long); long b = va_arg(ap, long); va_end(ap); return a + b; }
int main(void){ long r = get(1, 5L); printf("%ld\n", r); unsafe("va_arg read an argument that was not passed"); }
