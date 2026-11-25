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

struct s { char name[8]; char *p; };
int main(void){ struct s *s = malloc(sizeof *s); s->p = 0; char *n = launder(s->name); unsigned long a = (unsigned long)(uintptr_t)victim; for (int i = 0; i < 8; i++) n[8+i] = a >> (8*i); *s->p = 88; if (victim[0] == 88) unsafe("an overflow in a struct made a pointer"); ok(); }
