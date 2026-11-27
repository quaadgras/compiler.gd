// Default arguments with template arguments, in a class.
#include <stdio.h>
template <class K, class V>
struct Map {
	int n = 2;
};
struct Str {
	const char *s = "d";
};
struct FS {
	int reimport(const Str &file, const Map<Str, int> &opts = Map<Str, int>(), const Str &imp = Str(), int *gen = nullptr, bool update = true);
};
int FS::reimport(const Str &file, const Map<Str, int> &opts, const Str &imp, int *gen, bool update) {
	printf("%s %d %s %d %d\n", file.s, opts.n, imp.s, gen == nullptr, update);
	return 0;
}
int main() {
	FS fs;
	fs.reimport(Str());
}
