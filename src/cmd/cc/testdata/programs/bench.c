// A small C program of the kind Godot's core is made of: a growing
// array of structs that is sorted, a tree and a chained hash map of
// malloc'd nodes with pointers, and byte strings.
//
//	bench [vec|tree|map|str|all]
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static uint32_t seed;
static uint32_t rnd(void) {
	seed = seed * 1664525u + 1013904223u;
	return seed >> 8;
}

// A vector of structs.
typedef struct {
	int32_t id;
	double w;
} Item;
typedef struct {
	Item *data;
	long len, cap;
} Vec;

static void vec_push(Vec *v, Item it) {
	if (v->len == v->cap) {
		v->cap = v->cap ? v->cap * 2 : 8;
		v->data = (Item *)realloc(v->data, v->cap * sizeof(Item));
	}
	v->data[v->len++] = it;
}

static void sort_items(Item *lo, Item *hi) {
	while (hi - lo > 1) {
		double pivot = lo[(hi - lo) / 2].w;
		Item *i = lo, *j = hi - 1;
		while (i <= j) {
			while (i->w < pivot) i++;
			while (j->w > pivot) j--;
			if (i <= j) {
				Item t = *i;
				*i = *j;
				*j = t;
				i++;
				j--;
			}
		}
		if (j + 1 - lo < hi - i) {
			sort_items(lo, j + 1);
			lo = i;
		} else {
			sort_items(i, hi);
			hi = j + 1;
		}
	}
}

static double vec_run(int n) {
	Vec v = {0, 0, 0};
	for (int i = 0; i < n; i++) {
		Item it = {i, (double)(rnd() % 1000003) / 7.0};
		vec_push(&v, it);
	}
	sort_items(v.data, v.data + v.len);
	double sum = 0;
	for (long i = 0; i < v.len; i += 97) {
		sum += v.data[i].w * (double)(v.data[i].id % 13);
	}
	free(v.data);
	return sum;
}

// A binary tree of malloc'd nodes.
typedef struct Node {
	struct Node *left, *right;
	int64_t key;
	int32_t count;
} Node;

static void tree_insert(Node **root, int64_t key) {
	Node **p = root;
	while (*p) {
		Node *n = *p;
		if (key == n->key) {
			n->count++;
			return;
		}
		p = key < n->key ? &n->left : &n->right;
	}
	Node *n = (Node *)malloc(sizeof(Node));
	n->left = n->right = 0;
	n->key = key;
	n->count = 1;
	*p = n;
}

static int64_t tree_sum(const Node *n, int depth) {
	if (!n) return 0;
	return tree_sum(n->left, depth + 1) + n->key % 1000 * n->count + depth + tree_sum(n->right, depth + 1);
}

static void tree_free(Node *n) {
	if (!n) return;
	tree_free(n->left);
	tree_free(n->right);
	free(n);
}

static int64_t tree_run(int n) {
	Node *root = 0;
	for (int i = 0; i < n; i++) {
		tree_insert(&root, (int64_t)(rnd() % (uint32_t)(n * 2)));
	}
	int64_t s = tree_sum(root, 0);
	tree_free(root);
	return s;
}

// A chained hash map with string keys.
typedef struct Entry {
	struct Entry *next;
	char *key;
	uint32_t hash;
	int32_t value;
} Entry;
typedef struct {
	Entry **buckets;
	uint32_t nb, count;
} Map;

static uint32_t hash_str(const char *s) {
	uint32_t h = 2166136261u;
	for (; *s; s++) {
		h = (h ^ (uint8_t)*s) * 16777619u;
	}
	return h;
}

static void map_grow(Map *m) {
	uint32_t nb = m->nb ? m->nb * 2 : 16;
	Entry **b = (Entry **)calloc(nb, sizeof(Entry *));
	for (uint32_t i = 0; i < m->nb; i++) {
		Entry *e = m->buckets[i];
		while (e) {
			Entry *next = e->next;
			e->next = b[e->hash & (nb - 1)];
			b[e->hash & (nb - 1)] = e;
			e = next;
		}
	}
	free(m->buckets);
	m->buckets = b;
	m->nb = nb;
}

static Entry *map_find(const Map *m, const char *key, uint32_t h) {
	if (!m->nb) return 0;
	for (Entry *e = m->buckets[h & (m->nb - 1)]; e; e = e->next) {
		if (e->hash == h && strcmp(e->key, key) == 0) return e;
	}
	return 0;
}

static void map_put(Map *m, const char *key, int32_t value) {
	uint32_t h = hash_str(key);
	Entry *e = map_find(m, key, h);
	if (e) {
		e->value += value;
		return;
	}
	if (m->count >= m->nb) map_grow(m);
	e = (Entry *)malloc(sizeof(Entry));
	size_t n = strlen(key) + 1;
	e->key = (char *)malloc(n);
	memcpy(e->key, key, n);
	e->hash = h;
	e->value = value;
	e->next = m->buckets[h & (m->nb - 1)];
	m->buckets[h & (m->nb - 1)] = e;
	m->count++;
}

static char *name_of(char *buf, uint32_t x) {
	char *p = buf;
	*p++ = 'k';
	*p++ = '_';
	char tmp[12];
	int n = 0;
	do {
		tmp[n++] = (char)('0' + x % 10);
		x /= 10;
	} while (x);
	while (n) *p++ = tmp[--n];
	*p = 0;
	return buf;
}

static int64_t map_run(int n) {
	Map m = {0, 0, 0};
	char buf[32];
	for (int i = 0; i < n; i++) {
		map_put(&m, name_of(buf, rnd() % (uint32_t)n), i & 7);
	}
	int64_t s = m.count;
	for (int i = 0; i < 3 * n; i++) {
		name_of(buf, rnd() % (uint32_t)(2 * n));
		Entry *e = map_find(&m, buf, hash_str(buf));
		if (e) s += e->value;
	}
	for (uint32_t i = 0; i < m.nb; i++) {
		Entry *e = m.buckets[i];
		while (e) {
			Entry *next = e->next;
			free(e->key);
			free(e);
			e = next;
		}
	}
	free(m.buckets);
	return s;
}

// Byte strings.
static int64_t str_run(int n) {
	static const char *words[] = {"node", "viewport", "x", "transform_3d", "physics", "ab", "resource_loader"};
	size_t cap = 64, len = 0;
	char *s = (char *)malloc(cap);
	for (int i = 0; i < n; i++) {
		const char *w = words[rnd() % 7];
		size_t wl = strlen(w);
		if (len + wl + 2 > cap) {
			cap *= 2;
			s = (char *)realloc(s, cap);
		}
		memcpy(s + len, w, wl);
		len += wl;
		s[len++] = ' ';
	}
	s[len] = 0;
	int64_t count = 0;
	for (char *p = s; *p; p++) {
		if (*p == 'o') count++;
	}
	for (char *a = s, *b = s + len - 1; a < b; a++, b--) {
		char t = *a;
		*a = *b;
		*b = t;
	}
	count += (int64_t)hash_str(s) % 1000 + (int64_t)strlen(s);
	free(s);
	return count;
}

int main(int argc, char **argv) {
	const char *what = argc > 1 ? argv[1] : "all";
	int all = strcmp(what, "all") == 0;
	seed = 12345;
	if (all || strcmp(what, "vec") == 0) {
		double s = 0;
		for (int r = 0; r < 20; r++) s += vec_run(200000);
		printf("vec %.3f\n", s);
	}
	if (all || strcmp(what, "tree") == 0) {
		int64_t s = 0;
		for (int r = 0; r < 5; r++) s += tree_run(200000);
		printf("tree %lld\n", (long long)s);
	}
	if (all || strcmp(what, "map") == 0) {
		int64_t s = 0;
		for (int r = 0; r < 5; r++) s += map_run(100000);
		printf("map %lld\n", (long long)s);
	}
	if (all || strcmp(what, "str") == 0) {
		int64_t s = 0;
		for (int r = 0; r < 20; r++) s += str_run(300000);
		printf("str %lld\n", (long long)s);
	}
	return 0;
}
