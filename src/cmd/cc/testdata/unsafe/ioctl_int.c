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

#include <sys/ioctl.h>
#include <fcntl.h>
int main(void){ int x = -7; int fd = open("/proc/self/exe", O_RDONLY); ioctl(fd, FIONREAD, (long)(intptr_t)&x); if (x != -7) unsafe("ioctl stored through an integer"); ok(); }
