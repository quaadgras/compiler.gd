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

char small[8]; char next[8];
int main(void){ vn = 16; memset(launder(small), 88, vn); if (next[0] == 88 || victim[0] == 88) unsafe("memset of 16 bytes in an 8-byte global"); ok(); }
