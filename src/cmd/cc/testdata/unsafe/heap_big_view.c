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

struct big { long a[4096]; };
int main(void){ int *ip = malloc(sizeof(int)); struct big *b = (struct big *)launder(ip); vn = 1000; b->a[vn] = 1; unsafe("stored through a pointer to a type larger than its object"); }
