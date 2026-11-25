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

#include <stdatomic.h>
int main(void){ char *d = malloc(8); vn = 8; (atomic_flag_test_and_set)((atomic_flag *)(d + vn)); unsafe("atomic_flag_test_and_set stored at the end of an object"); }
