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

char g1[16]; char g2[16] = "next";
static void fill64(void *p) { char (*a)[64] = (char (*)[64])p; for (int i = 0; i < vn; i++) (*a)[i] = 65; }
int main(void){ vn = 64; fill64(launder(g1)); if (g2[0] == 65) unsafe("wrote in the next global through a char[64]"); unsafe("wrote 64 bytes in a 16-byte global through a char[64]"); }
