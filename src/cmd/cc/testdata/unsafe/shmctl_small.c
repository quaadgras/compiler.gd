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

#include <sys/shm.h>
int main(void){ char *d = malloc(8); int id = shmget(IPC_PRIVATE, 4096, IPC_CREAT | 0600); if (shmctl(id, IPC_STAT, (struct shmid_ds *)launder(d)) == 0) unsafe("shmctl stored a struct shmid_ds in an 8-byte object"); ok(); }
