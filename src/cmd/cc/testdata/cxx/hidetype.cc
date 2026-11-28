#include <stdio.h>
#include <time.h>
struct S { int v; };
int main() { int S = 2; auto tm = S * 3; struct S s = {4}; printf("%d %d\n", tm, s.v + S); }
