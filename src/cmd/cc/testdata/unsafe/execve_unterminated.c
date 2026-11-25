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

#include <unistd.h>
int main(void){ char **av = malloc(2 * sizeof(char *)); av[0] = "a"; av[1] = "b"; char *env[1] = {NULL}; execve("/nonexistent/program", (char *const *)launder(av), env); unsafe("execve read argv past its array"); }
