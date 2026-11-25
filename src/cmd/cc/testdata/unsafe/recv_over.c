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
int main(void){ int sv[2]; if (socketpair(AF_UNIX, SOCK_STREAM, 0, sv) != 0) ok(); char big[64]; memset(big, 65, 64); write(sv[0], big, 64); char *d = malloc(8); vn = 64; if (recv(sv[1], d, vn, 0) > 8) unsafe("recv of 64 bytes in an 8-byte object"); ok(); }
