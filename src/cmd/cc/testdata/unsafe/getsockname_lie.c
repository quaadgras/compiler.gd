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
#include <netinet/in.h>
int main(void){ int fd = socket(AF_INET, SOCK_STREAM, 0); if (fd < 0) ok(); struct sockaddr_in a; memset(&a, 0, sizeof a); a.sin_family = AF_INET; if (bind(fd, (struct sockaddr *)&a, sizeof a) != 0) ok(); char *d = malloc(2); socklen_t n = 64; if (getsockname(fd, (struct sockaddr *)launder(d), &n) == 0) unsafe("getsockname stored an address in a 2-byte object"); ok(); }
