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

char dummy[16];
int main(void){ char **pp = malloc(sizeof(char *)); *pp = dummy; unsigned char *b = (unsigned char *)pp; unsigned long a = (unsigned long)(uintptr_t)victim; for (int i = 0; i < 8; i++) b[i] = a >> (8*i); **pp = 88; if (victim[0] == 88) unsafe("a pointer written byte by byte was used"); ok(); }
