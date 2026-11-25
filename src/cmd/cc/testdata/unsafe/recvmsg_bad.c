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
#include <unistd.h>
int main(void){ int sv[2]; if (socketpair(AF_UNIX, SOCK_STREAM, 0, sv) != 0) ok(); char big[64]; memset(big, 65, 64); write(sv[0], big, 64); struct iovec v; v.iov_base = malloc(8); vn = 64; v.iov_len = vn; struct msghdr m; memset(&m, 0, sizeof m); m.msg_iov = &v; m.msg_iovlen = 1; if (recvmsg(sv[1], &m, 0) > 8) unsafe("recvmsg of 64 bytes in an 8-byte object"); ok(); }
