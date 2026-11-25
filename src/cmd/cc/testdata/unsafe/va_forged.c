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
int main(void){ void **fake = calloc(8, sizeof(void *)); fake[0] = victim; va_list ap; memcpy(&ap, &fake, sizeof fake); char *p = va_arg(ap, char *); if (p) *p = 88; if (victim[0] == 88) unsafe("va_arg read from memory that is no va_list"); ok(); }
