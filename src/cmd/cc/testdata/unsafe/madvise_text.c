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
int main(void){ uintptr_t page = (uintptr_t)(void *)main & ~(uintptr_t)4095; if (madvise((void *)page, 4096, MADV_DONTNEED) == 0) unsafe("madvise took the text away"); ok(); }
