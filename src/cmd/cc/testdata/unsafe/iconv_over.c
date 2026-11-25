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

#include <iconv.h>
int main(void){ iconv_t cd = iconv_open("UTF-16LE", "UTF-8"); if (cd == (iconv_t)-1) ok(); char in[32]; memset(in, 65, 32); char *out = malloc(4); char *ip = in, *op = out; size_t il = 32, ol = 4096; iconv(cd, &ip, &il, &op, &ol); unsafe("iconv wrote 64 bytes in a 4-byte object"); }
