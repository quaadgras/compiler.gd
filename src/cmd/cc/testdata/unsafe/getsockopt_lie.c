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

#include <sys/socket.h>
int main(void){ int fd = socket(AF_INET, SOCK_STREAM, 0); if (fd < 0) ok(); char *d = malloc(2); socklen_t n = 64; if (getsockopt(fd, SOL_SOCKET, SO_TYPE, d, &n) == 0) unsafe("getsockopt stored an int in a 2-byte object"); ok(); }
