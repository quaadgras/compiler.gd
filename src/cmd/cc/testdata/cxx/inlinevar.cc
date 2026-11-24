#include <cstdio>
struct Map { int n; Map() : n(7) { printf("Map()\n"); } void add() { n++; } };
struct Dbg {
	static inline Dbg *singleton = nullptr;
	static inline Map profilers;
	static inline int count;
	static inline double scale[2];
	static void reg() { profilers.add(); count++; scale[1] += 0.5; }
};
int main() { Dbg::reg(); Dbg::reg(); printf("%d %d %g %d\n", Dbg::profilers.n, Dbg::count, Dbg::scale[1], Dbg::singleton == nullptr); }
