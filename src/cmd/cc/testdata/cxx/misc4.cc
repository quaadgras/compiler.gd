#include <stdio.h>
struct Matrix { float e11; };
Matrix operator*(const Matrix& lhs, const Matrix& rhs) { return Matrix{lhs.e11 * rhs.e11}; }
static inline Matrix operator*(const Matrix* lhs, const Matrix& rhs) { return *lhs * rhs; }
struct Paint { Matrix m; Matrix& transform() { return m; } };
namespace manifold { enum class OpType { Add, Subtract }; }
struct Op {
	int manifold;
	static manifold::OpType conv(int v) { return v ? manifold::OpType::Subtract : manifold::OpType::Add; }
};
template <class T> struct Scoped { T *p; operator T *() { return p; } };
static bool fast(Paint* cmpTarget, const Matrix& pm) {
	auto tm = pm * cmpTarget->transform();
	return tm.e11 > 1;
}
int main() {
	Paint p{{3}};
	Matrix m{2};
	unsigned short arr[4] = {1, 2, 3, 4};
	Scoped<unsigned short> buf{arr};
	unsigned short *q = buf + 2;
	printf("%d %d %d\n", fast(&p, m), (int)Op::conv(1), *q);
}
