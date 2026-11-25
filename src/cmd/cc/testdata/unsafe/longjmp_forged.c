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

#include <setjmp.h>
int main(void){ jmp_buf jb; void **fake = calloc(8, sizeof(void *)); char *live = calloc(1, 64); memset(live, 1, 64); fake[0] = live; memcpy(&jb, &fake, sizeof fake); longjmp(jb, 1); unsafe("longjmp to a jmp_buf that points to made-up memory"); }
