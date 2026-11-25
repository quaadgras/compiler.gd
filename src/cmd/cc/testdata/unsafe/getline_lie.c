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

int main(void){ FILE *f = tmpfile(); for (int i = 0; i < 100; i++) fputc(65, f); fputc(10, f); rewind(f);
 char *line = malloc(8); size_t n = 4096; getline(&line, &n, f); unsafe("getline wrote a line where *n was more than the object"); }
