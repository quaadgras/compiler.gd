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

#include <arpa/inet.h>
int main(void){ char *d = malloc(4); if (inet_pton(AF_INET6, "::1", d) == 1) unsafe("inet_pton stored 16 bytes in a 4-byte object"); ok(); }
