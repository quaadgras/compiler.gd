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

int main(void){ char *p = malloc(64); strcpy(p, "safe"); char *q = realloc(p + 60, 4096); if (q && q[5] != 0) unsafe("realloc of an interior pointer copied what is after the object"); if (strcmp(p, "safe") != 0) unsafe("realloc of an interior pointer corrupted the object"); ok(); }
