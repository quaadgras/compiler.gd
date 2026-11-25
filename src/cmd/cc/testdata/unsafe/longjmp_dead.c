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
static jmp_buf jb;
static void f(void){ if (setjmp(jb)) { printf("back\n"); } }
int main(void){ f(); longjmp(jb, 1); unsafe("longjmp to a function that returned"); }
