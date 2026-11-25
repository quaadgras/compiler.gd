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
int main(void){ char *p = g1; vn = 4096; for (int i = 0; i < vn; i++) p[i] = 65; unsafe("wrote 4096 bytes in a 16-byte global"); }
