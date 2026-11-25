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
int main(void){ regex_t re; if (regcomp(&re, "(a)(b)", REG_EXTENDED) != 0) ok(); regmatch_t *m = malloc(sizeof *m); vn = 64; regexec(&re, "ab", vn, m, 0); unsafe("regexec stored 64 matches where there is room for one"); }
