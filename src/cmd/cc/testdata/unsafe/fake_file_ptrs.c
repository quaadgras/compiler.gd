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

int main(void){ void **fake = calloc(64, sizeof(void *)); void **inner = calloc(64, sizeof(void *));
 for (int i = 0; i < 64; i++) { fake[i] = inner; inner[i] = victim; }
 ((char *)fake)[0] = 1; fputs("x", (FILE *)launder(fake)); unsafe("an object of pointers was used as a FILE"); }
