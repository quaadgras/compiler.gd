#include <stdio.h>
struct N { N *prev, *next; int v; };
struct L {
	N *head, *tail;
	N *pick(N *after) { N *r; r = after ? after->prev : tail; return r; }
	int val(N *after) { return after ? after->v : 7; }
};
int main() {
	N a = {nullptr, nullptr, 1};
	L l = {&a, &a};
	printf("%d %d %d\n", l.pick(nullptr)->v, l.val(nullptr), l.pick(&a) == nullptr);
}
