#include <stdio.h>
#include <stdint.h>
#include <string.h>
struct Base {
	const uint32_t *comp = nullptr;
	uint32_t size = 0, h = 0;
	void setup(const uint32_t *p, uint32_t n) {
		comp = p;
		size = n / 4;
		for (uint32_t i = 0; i < size; i++)
			h = h * 31 + comp[i];
	}
};
template <class R> struct Method : Base {
	struct Data {
		R (*method)();
		long pad;
	} data;
	explicit Method(R (*m)()) {
		memset(&data, 0, sizeof(Data));
		data.method = m;
		data.pad = 5;
		setup((const uint32_t *)&data, sizeof(Data));
	}
	R call() { return data.method(); }
};
static int seven() { return 7; }
int main() {
	int sum = 0, same = 0;
	Method<int> *keep[64];
	for (int i = 0; i < 64; i++) {
		keep[i] = new Method<int>(seven);
		sum += keep[i]->call();
		same += keep[i]->h == keep[0]->h && memcmp(keep[i]->comp, keep[0]->comp, 16) == 0;
	}
	// Taking the const off is what claims the memory for integers:
	// done for an object that is not used as a Method after.
	Method<int> *spare = new Method<int>(seven);
	uint32_t *w = const_cast<uint32_t *>(spare->comp);
	w[2] = 9;
	printf("%d %d %ld %d %u\n", sum, same, keep[1]->data.pad, keep[2]->call(), w[2]);
	return 0;
}
