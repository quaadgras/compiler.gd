// An unnamed enumeration is a type of its own to templates, and other
// things C++ takes as constants or conversions.
#include <stdio.h>

template <typename T, typename T2>
constexpr auto MAX(const T a, const T2 b) { return a > b ? a : b; }

struct Limits {
	enum {
		STATIC_LIGHTS = 1024,
		DYNAMIC_LIGHTS = 128,
	};
};

template <class U>
struct Grow {
	U capacity = 0;
	void reserve() { capacity = MAX((U)2, capacity + ((1 + capacity) >> 1)); }
};

#define FOURCC(s) ((unsigned)(((s)[3] << 24U) | ((s)[2] << 16U) | ((s)[1] << 8U) | ((s)[0])))
enum Codes {
	DXT1 = FOURCC("DXT1"),
};

struct V {
	int x = 1;
	V operator*(int k) const {
		V r;
		r.x = x * k;
		return r;
	}
	V operator-(const V &o) const {
		V r;
		r.x = x - o.x;
		return r;
	}
};

typedef void (*Fn)();
struct FuncPtr {
	Fn f = nullptr;
	operator Fn() const { return f; }
};

static const char *chars(const char *s) { return s; }

int main() {
	printf("%d\n", (int)MAX(Limits::STATIC_LIGHTS, Limits::DYNAMIC_LIGHTS));
	Grow<unsigned> g;
	g.reserve();
	g.reserve();
	printf("%u %x\n", g.capacity, (unsigned)DXT1);
	V b;
	b.x = 3;
	V r = (V() * 5 - b) * 2;
	FuncPtr fp;
	printf("%d %d %s\n", r.x, fp != nullptr, chars(u8"plain"));
}
