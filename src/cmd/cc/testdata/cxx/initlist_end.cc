// The end of an initializer_list of a class with pointers: a pointer to
// the end of an array that is in Go's heap, converted to the element's
// type and compared, and never loaded through.
#include <cstdio>
#include <initializer_list>

struct Mapping {
	const char *name;
	unsigned hash;
};

static const Mapping *kept;

struct Table {
	Mapping *data = nullptr;
	int n = 0;
	Table(std::initializer_list<Mapping> l) {
		data = new Mapping[l.size()];
		kept = l.begin();
		for (const Mapping *m = l.begin(); m != l.end(); m++) {
			data[n++] = *m;
		}
	}
};

static int count(std::initializer_list<Mapping> l) {
	int k = 0;
	kept = l.begin(); // the array is no variable of a frame
	for (const Mapping &m : l) {
		k += m.hash;
	}
	return k + (int)(l.end() - l.begin());
}

int main() {
	Table t({ { "a", 1 }, { "b", 2 }, { "c", 3 }, { "d", 4 }, { "e", 5 } });
	printf("%d %s %s %d\n", t.n, t.data[0].name, t.data[4].name, count({ { "x", 10 }, { "y", 20 } }));
	return 0;
}
