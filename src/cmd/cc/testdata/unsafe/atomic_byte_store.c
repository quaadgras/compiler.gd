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
char other[16] = "other";
int main(void){ char **slot = malloc(sizeof(char *)); *slot = other; _Atomic unsigned char *b = (_Atomic unsigned char *)launder(slot); intptr_t want = (intptr_t)victim; for (int i = 0; i < (int)sizeof(char *); i++) atomic_store(&b[i], (unsigned char)(want >> (8 * i))); char *p = *slot; *p = 88; if (victim[0] == 88) unsafe("a pointer whose bytes were stored atomically was used"); ok(); }
