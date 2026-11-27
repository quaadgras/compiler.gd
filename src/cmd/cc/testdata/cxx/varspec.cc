// Partial specializations of variable templates.
#include <stdio.h>
#include <type_traits>

struct V2 {
	static const int AXIS_COUNT = 2;
};
struct V3 {
	enum { AXIS_COUNT = 3 };
};
struct Plain {};

template <typename T, typename = void>
inline constexpr bool is_vector_v = false;
template <typename T>
inline constexpr bool is_vector_v<T, std::void_t<decltype(T::AXIS_COUNT)>> = true;

template <typename T>
inline constexpr int rank_v = 0;
template <typename T>
inline constexpr int rank_v<T *> = 1 + rank_v<T>;
template <typename T, int N>
inline constexpr int rank_v<T[N]> = 10 + rank_v<T>;

template <typename T>
static int axes() {
	if constexpr (is_vector_v<T>) {
		return T::AXIS_COUNT;
	} else {
		return -1;
	}
}

int main() {
	printf("%d %d %d %d\n", is_vector_v<V2>, is_vector_v<V3>, is_vector_v<Plain>, is_vector_v<int>);
	printf("%d %d %d\n", axes<V2>(), axes<V3>(), axes<Plain>());
	printf("%d %d %d\n", rank_v<int>, rank_v<int **>, rank_v<char *[4]>);
}
