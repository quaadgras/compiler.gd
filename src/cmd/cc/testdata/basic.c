// Ordinary C: the output must match gcc's.
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define N 10

struct node {
	int v;
	struct node *next;
};

typedef struct point {
	long x, y;
	char name[8];
} point;

enum color { RED, GREEN = 5, BLUE };

int counter;
int table[N] = {1, 2, 3};
const char *greeting = "hello";

int fib(int n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }

struct node *push(struct node *head, int v) {
	struct node *n = malloc(sizeof(struct node));
	n->v = v;
	n->next = head;
	return n;
}

int sum(struct node *n) {
	int s = 0;
	for (; n; n = n->next)
		s += n->v;
	return s;
}

void swap(int *a, int *b) {
	int t = *a;
	*a = *b;
	*b = t;
}

static int next_id(void) {
	static int id = 100;
	return id++;
}

void sort(int *a, int n) {
	for (int i = 0; i < n; i++)
		for (int j = i + 1; j < n; j++)
			if (a[j] < a[i])
				swap(&a[i], &a[j]);
}

unsigned hash(const char *s) {
	unsigned h = 2166136261u;
	while (*s) {
		h ^= (unsigned char)*s++;
		h *= 16777619u;
	}
	return h;
}

const char *name(enum color c) {
	switch (c) {
	case RED:
		return "red";
	case GREEN:
	case BLUE:
		return "green or blue";
	default:
		return "?";
	}
}

int main(void) {
	int i, x, y;
	struct node *list = 0;
	point p = {1, 2, "pt"};
	point q;
	int arr[] = {5, 3, 9, 1, 7};
	char buf[32];
	int *dyn;
	long total = 0;

	printf("fib(20) = %d\n", fib(20));
	for (i = 1; i <= 5; i++)
		list = push(list, i * i);
	printf("sum = %d\n", sum(list));
	x = 3;
	y = 4;
	swap(&x, &y);
	printf("x=%d y=%d\n", x, y);
	sort(arr, 5);
	for (i = 0; i < 5; i++)
		printf("%d ", arr[i]);
	printf("\n");
	q = p;
	q.x += 10;
	printf("p=(%ld,%ld,%s) q=(%ld,%ld,%s)\n", p.x, p.y, p.name, q.x, q.y, q.name);
	i = next_id();
	x = next_id();
	printf("ids %d %d %d\n", i, x, next_id());
	printf("hash %u\n", hash(greeting));
	strcpy(buf, greeting);
	buf[0] = 'j';
	printf("%s %d %d\n", buf, (int)strlen(buf), strcmp(buf, greeting) > 0);
	printf("%s %s %s\n", name(RED), name(BLUE), name(7));
	dyn = malloc(N * sizeof(int));
	for (i = 0; i < N; i++)
		dyn[i] = table[i] * 2 + i;
	for (i = 0; i < N; i++)
		total += dyn[i];
	printf("total %ld sizeof point %d\n", total, (int)sizeof(point));
	i = 0;
	do {
		i += 3;
		if (i == 6)
			continue;
		counter++;
	} while (i < 12);
	printf("counter %d i %d\n", counter, i);
	{
		int *p1 = &arr[1], *p2 = &arr[4];
		printf("diff %ld cmp %d\n", (long)(p2 - p1), p1 < p2);
	}
	x = -7;
	printf("%d %d %u %d\n", x / 2, x % 3, (unsigned)x >> 28, x << 2);
	y = (x = 5, x + 1);
	printf("%d %d\n", y, x > 3 && y < 10 || 0);
	return 0;
}
