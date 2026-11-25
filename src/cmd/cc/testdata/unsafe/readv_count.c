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

#include <sys/uio.h>
#include <fcntl.h>
int main(void){ struct iovec *v = malloc(sizeof *v); v->iov_base = malloc(8); v->iov_len = 8; int fd = open("/dev/zero", O_RDONLY); vn = 64; if (readv(fd, v, vn) >= 8) unsafe("readv read 64 iovecs where there is one"); ok(); }
