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
static char *get(int n, ...) { va_list ap; va_start(ap, n); char *p = va_arg(ap, char *); va_end(ap); return p; }
int main(void){ union { double d; long l; } u; u.l = (long)(intptr_t)victim; char *p = get(1, u.d); if (p) *p = 88; if (victim[0] == 88) unsafe("va_arg read a double as a pointer"); ok(); }
