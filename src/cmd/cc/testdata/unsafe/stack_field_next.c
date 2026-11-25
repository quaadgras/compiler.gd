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

struct box { char a[8]; };
static void fill(char *p, int n) { for (int i = 0; i < n; i++) p[i] = 65; }
int main(void){ char after[8] = "after"; struct box s; char before[8] = "before"; vn = 8; fill(s.a, vn + 24); if (after[0] == 65 || before[0] == 65) unsafe("a field's pointer wrote in the local next to its struct"); unsafe("wrote 32 bytes in an 8-byte field of a local"); }
