// The C side of interop: what Go sees through import "C".
#ifndef INTEROP_H
#define INTEROP_H

struct node {
	int v;
	struct node *next;
};

struct holder {
	int *random_int;
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

struct node *make_list(int n);
long sum_list(struct node *n);
int read_at(int p[], int i);
void store_past(struct holder *h, int arr[], int n);
struct holder *new_holder(void);
int load_back(struct holder *h, int k);
int one_based_sum(int a[], int n);
void pun(struct holder *h);
int forge(long addr);
void hide(struct node *n);
int stack_pun(void);
void confuse(struct node *n, int *x);
int unconfuse(struct node *n);
void poke(void *p, int i, long v);
long sum_pair(struct node *n);
void swap_ints(int *a, int *b);
int locals(void);
void keep_array(struct holder *h, int a[]);
int at(struct holder *h, int i);
struct variant *make_variant(int n);
long variant_sum(struct variant *v);
void variant_set_double(struct variant *v, double d);
long variant_misread(struct variant *v);
struct node *in_pool(void);
void make_pool(void);
long pool_sum(void);
int free_mallocd(void);
void free_interior(void);
void free_local(void);
void free_lost(void);
void realloc_lost(void);
void realloc_interior(void);
void free_go(int *p);

#endif
