// A union of an integer and a pointer, the integer read after the
// pointer was stored: the pointer's address is what it is, as Godot's
// Callable asks whether it is null.
#include <cstdio>
#include <cstdint>

struct Custom {
	int refs = 1;
	const char *name;
	Custom(const char *p_name) : name(p_name) {}
};

class Handle {
	const char *method = nullptr;
	union {
		uint64_t object = 0;
		Custom *custom;
	};

public:
	Handle() {}
	Handle(uint64_t p_object, const char *p_method) : method(p_method) { object = p_object; }
	Handle(Custom *p_custom) {
		object = 0;
		custom = p_custom;
	}
	bool is_null() const { return method == nullptr && object == 0; }
	bool is_custom() const { return method == nullptr && custom != nullptr; }
	uint64_t id() const { return is_custom() ? 0 : object; }
	const char *name() const { return is_custom() ? custom->name : method; }
	bool same(const Handle &o) const { return method == o.method && object == o.object; }
	void clear() {
		method = nullptr;
		object = 0;
	}
};

static Handle make(bool custom) {
	static Custom c("custom");
	return custom ? Handle(&c) : Handle(42, "method");
}

int main() {
	Handle none, std = make(false), cus = make(true);
	printf("%d %d %d | %d %d %d\n", none.is_null(), std.is_null(), cus.is_null(), none.is_custom(), std.is_custom(), cus.is_custom());
	printf("%llu %llu %s %s\n", (unsigned long long)std.id(), (unsigned long long)cus.id(), std.name(), cus.name());
	Handle copy = cus;
	printf("%d %d %d %s\n", copy.same(cus), copy.same(std), copy.is_null(), copy.name());
	copy.clear();
	printf("%d %d %d\n", copy.is_null(), copy.is_custom(), cus.is_custom());
	copy = std;
	printf("%d %llu %d\n", copy.same(std), (unsigned long long)copy.id(), copy.is_custom());
	return 0;
}
