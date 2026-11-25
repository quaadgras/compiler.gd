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

#include <fcntl.h>
#include <unistd.h>
int main(void){ char *d = calloc(1, 2); int fd = open("/proc/self/exe", O_RDONLY); if (fcntl(fd, F_GETLK, (struct flock *)launder(d)) == 0) unsafe("fcntl F_GETLK used a 2-byte object as a struct flock"); ok(); }
