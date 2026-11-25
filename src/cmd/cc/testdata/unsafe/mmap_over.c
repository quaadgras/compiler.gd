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

#include <sys/mman.h>
#include <unistd.h>
int main(void){ char *m = mmap(NULL, 100000, PROT_READ|PROT_WRITE, MAP_ANONYMOUS|MAP_PRIVATE, -1, 0); if (m == MAP_FAILED) ok(); long pg = sysconf(_SC_PAGESIZE); vn = (100000 + pg - 1) / pg * pg + pg; m[vn] = 88; vn = m[vn]; unsafe("stored a page past a mapping"); }
