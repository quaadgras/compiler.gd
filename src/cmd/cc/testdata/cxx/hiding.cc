#include <cstdio>
struct Obj { int id = 1; };
struct Ev : Obj { int e = 2; };
template <typename T> struct Rf { T *p = nullptr; Rf() {} Rf(T *q) : p(q) {} bool is_valid() const { return p != nullptr; } };
struct DB {
	static Obj *get_instance(int id) { static Ev ev; return id ? &ev : nullptr; }
	template <typename T> static Rf<T> get_ref(int id) { return Rf<T>(static_cast<T *>(get_instance(id))); }
};
struct VI {
	static int assign(int *v, Obj *o) { return 1; }
	static int assign(int *v, const Obj *o) { return 2; }
	static int assign(int *v, const int *vo) { return 3; }
};
struct Node {
	struct Data { int depth = 5; bool ready : 1; } data;
	int count = 1;
};
struct Node3D : Node {
	struct Data { double scale = 2.5; bool gizmo : 1; } data;
	int count = 2;
	void set(bool b) { data.gizmo = b; }
	double get() const { return data.scale + data.gizmo + count + Node::count + Node::data.depth; }
};
struct Th {
	Rf<Ev> t;
	bool run(int id) {
		t = DB::get_ref<Ev>(id);
		return t.is_valid();
	}
};
int main() {
	Ev ev; const Ev cev; int v = 0;
	printf("%d %d %d\n", VI::assign(&v, &ev), VI::assign(&v, &cev), VI::assign(&v, &v));
	Node3D n; n.set(true);
	Th th;
	printf("%g %d %d %d\n", n.get(), n.data.gizmo, th.run(1), th.run(0));
}
