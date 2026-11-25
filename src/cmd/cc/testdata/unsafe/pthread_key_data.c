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

#include <pthread.h>
#include <unistd.h>
static void *run(void *a){ pthread_setspecific(*(pthread_key_t *)a, victim); return NULL; }
int main(void){ long *d = malloc(64); d[0] = 0xc3c3c3c3c3c3c3c3; pthread_key_t k; pthread_key_create(&k, (void (*)(void *))launder(d)); pthread_t t; pthread_create(&t, NULL, run, &k); pthread_join(t, NULL); sleep(5); unsafe("a thread ran data as the destructor of a key"); }
