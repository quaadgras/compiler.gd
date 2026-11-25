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
int main(void){ char *h = malloc(1 << 16); uintptr_t page = ((uintptr_t)h + 8192) & ~(uintptr_t)4095; int id = shmget(IPC_PRIVATE, 4096, IPC_CREAT | 0600); void *p = shmat(id, (void *)page, SHM_RND); if (p != (void *)-1) unsafe("shmat attached a segment where the heap is"); ok(); }
