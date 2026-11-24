// The C side of interop: what Go sees through import "C".
#ifndef INTEROP_H
#define INTEROP_H

struct node {
	int v;
	struct node *next;
};

typedef struct node node_t;

enum color { RED, GREEN = 5, BLUE };

struct holder {
	int *ints;
	long tag;
};

union value {
	long i;
	double d;
	struct node *n;
};

struct variant {
	int type; // 0 long, 1 double, 2 list
	union value v;
};

extern int counter;
extern struct node *pool_root;

struct node *make_list(int n);
long sum_list(struct node *n);
int read_at(int p[], int i);
struct holder *new_holder(void);
void keep_array(struct holder *h, int a[]);
int at(struct holder *h, int i);
void hide(struct node *n);
long hidden_sum(void);
void poke(void *p, int i, long v);
void *as_void(node_t *n);
long sum_pair(struct node *n);
void swap_ints(int *a, int *b);
int locals(void);
int count(void);
enum color next_color(enum color c);
char *shout(char *s);
int backwards_sum(int n);
long sum_back(int *end, int n);
struct variant *make_variant(int n);
long variant_sum(struct variant *v);
void variant_set_long(struct variant *v, long i);
struct node *in_pool(void);
void make_pool(void);
long pool_sum(void);
int free_mallocd(void);

#endif
