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

#include <regex.h>
int main(void){ regex_t re; memset(&re, 0x41, sizeof re); regexec(&re, "x", 0, NULL, 0); void **fake = calloc(8, sizeof(void *)); void **slots = (void **)&re; for (unsigned i = 0; i < sizeof re / sizeof(void *); i++) slots[i] = fake; if (regexec(&re, "x", 0, NULL, 0) == 0) unsafe("regexec ran a regex_t that regcomp did not make"); ok(); }
