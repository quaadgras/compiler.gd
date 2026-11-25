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

#include <dirent.h>
int main(void){ DIR *d = opendir("/"); char *e = malloc(8); struct dirent *res; readdir_r(d, (struct dirent *)launder(e), &res); unsafe("readdir_r stored an entry in an 8-byte object"); }
