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

/* The byte after what aligned_alloc was asked for is not the object's,
   though the allocation that it is in is longer. */
int main(void){
	char *a = aligned_alloc(64, 100);
	if ((uintptr_t)a % 64 != 0) unsafe("not aligned");
	vn = 99; a[vn] = 1;
	vn = 100; a[vn] = 2;
	char *r = launder(a);
	if (r[vn] == 2) unsafe("stored in the byte after an aligned object");
	unsafe("stored in the byte after an aligned object, unseen");
}
