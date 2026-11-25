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

struct node { struct node *next; long v; };
long gl[8] = {1, 2, 3, 4, 5, 6, 7, 8};
struct node gnodes[4];
char gs[5] = "abcd";
static long sum(const long *p, const long *end) { long s = 0; while (p != end) s += *p++; return s; }
static long back(const long *a, const long *end) { long s = 0; while (end != a) s += *--end; return s; }
static int count(struct node *p, struct node *end) { int n = 0; for (; p < end; p++) n++; return n; }
int main(void){
	long l[8] = {1, 2, 3, 4, 5, 6, 7, 8};
	struct node nodes[4];
	struct node *after = nodes;
	char s[5] = "abcd";
	vn = 8;
	if (sum(l, l + vn) != 36 || sum(launder(l), (long *)launder(l) + vn) != 36 || back(l, l + vn) != 36) unsafe("the sum of a local array is wrong");
	if (sum(gl, gl + vn) != 36 || sum(launder(gl), (long *)launder(gl) + vn) != 36 || back(gl, gl + vn) != 36) unsafe("the sum of a global array is wrong");
	vn = 4;
	if (count(nodes, nodes + vn) != 4 || count(launder(nodes), (struct node *)launder(nodes) + vn) != 4) unsafe("the count of a local array is wrong");
	if (count(gnodes, gnodes + vn) != 4 || count(launder(gnodes), (struct node *)launder(gnodes) + vn) != 4) unsafe("the count of a global array is wrong");
	int n = 0;
	for (char *p = launder(s), *e = p + 5; p != e; p++) n += *p != 0;
	for (char *p = launder(gs), *e = p + 5; p != e; p++) n += *p != 0;
	char *e = (char *)launder(l) + sizeof l;
	long *le = (long *)e;
	if (n != 8 || le - l != 8 || after != nodes) unsafe("the ends are wrong");
	ok();
}
