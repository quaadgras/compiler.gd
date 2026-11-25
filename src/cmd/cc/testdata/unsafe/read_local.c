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

#include <unistd.h>
#include <fcntl.h>
static long rd(int fd){ char d[8]; vn = 256; long n = read(fd, d, vn); return n + d[0]; }
int main(void){ int fd = open("/dev/zero", O_RDONLY); if (rd(fd) == 256) unsafe("read(2) of 256 bytes in an 8-byte local"); ok(); }
