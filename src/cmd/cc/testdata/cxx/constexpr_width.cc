// The width of a bit-field, the length of an array and a template's
// argument that calls of constexpr function templates give.
#include <cstdio>
#include <cstdint>

namespace Math {
template <typename T>
constexpr T floor_log2(T x) {
	return x < 2 ? x : 1 + floor_log2(x >> 1);
}
template <typename T>
constexpr T get_num_bits(T x) {
	return floor_log2(x);
}
} // namespace Math

template <int N>
struct Sized {
	int n = N;
};

class Material {
public:
	enum Filter { FILTER_A, FILTER_B, FILTER_C, FILTER_D, FILTER_E, FILTER_MAX };
	enum Mode { MODE_A, MODE_B, MODE_MAX };
	enum Flags { FLAG_NUM_BITS = 4 };

	struct Key {
		uint64_t filter : Math::get_num_bits(FILTER_MAX - 1);
		uint64_t mode : Math::get_num_bits(MODE_MAX - 1);
		uint64_t flags : FLAG_NUM_BITS;
		uint64_t on : 1;
	};
	Key key;
	int table[Math::get_num_bits(FILTER_MAX - 1) + 2];
	Sized<Math::get_num_bits(64)> sized;
};

int main() {
	Material m{};
	m.key.filter = 7;
	m.key.mode = 3;
	m.key.flags = 9;
	m.key.on = 1;
	printf("%d %d %d %d | %d %d %d\n", (int)m.key.filter, (int)m.key.mode, (int)m.key.flags, (int)m.key.on, (int)(sizeof(m.table) / sizeof(int)), m.sized.n, (int)sizeof(Material::Key));
	static_assert(Math::get_num_bits(Material::FILTER_MAX - 1) == 3, "bits");
	return 0;
}
