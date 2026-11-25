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
int main(void){ char *fake = calloc(1, 512); char in[4] = "abc", out[8]; char *ip = in, *op = out; size_t il = 3, ol = 8; if (iconv((iconv_t)launder(fake), &ip, &il, &op, &ol) != (size_t)-1) unsafe("zeroed bytes were used as an iconv_t"); ok(); }
