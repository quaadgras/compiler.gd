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
int main(void){ char *d = malloc(2); int fd = open("/proc/self/exe", O_RDONLY); if (ioctl(fd, FIONREAD, (int *)launder(d)) == 0) unsafe("ioctl FIONREAD stored an int in a 2-byte object"); ok(); }
