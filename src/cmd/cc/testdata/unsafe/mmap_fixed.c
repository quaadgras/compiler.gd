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
char dummy[16];
int main(void){ char **pp = malloc(1 << 16); uintptr_t page = ((uintptr_t)pp + 8192) & ~(uintptr_t)4095; char **slot = (char **)((char *)pp + (page - (uintptr_t)pp)); *slot = dummy;
 void *m = mmap((void *)page, 4096, PROT_READ|PROT_WRITE, MAP_FIXED|MAP_ANONYMOUS|MAP_PRIVATE, -1, 0);
 if (m == MAP_FAILED) ok(); long *l = m; l[0] = (long)(intptr_t)victim; **slot = 88; if (victim[0] == 88) unsafe("mmap MAP_FIXED over the heap made a pointer"); ok(); }
