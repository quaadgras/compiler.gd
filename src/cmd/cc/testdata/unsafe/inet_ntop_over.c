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
int main(void){ unsigned char a[4] = {127, 0, 0, 1}; char *d = malloc(2); vn = 4096; if (inet_ntop(AF_INET, a, d, vn)) unsafe("inet_ntop wrote an address in a 2-byte object"); ok(); }
