#include <cstdio>
struct Image {
	enum UsedChannels { L, LA };
	struct Params { int q; };
	static int (*packer)(const Image &, UsedChannels, const Params &);
	static int (Image::*pm)(UsedChannels) const;
	int f(UsedChannels c) const { return c + 10; }
};
static int pk(const Image &, Image::UsedChannels c, const Image::Params &p) { return c + p.q; }
int (*Image::packer)(const Image &, Image::UsedChannels, const Params &) = nullptr;
int (Image::*Image::pm)(UsedChannels) const = nullptr;
int main() {
	Image::packer = pk;
	Image::pm = &Image::f;
	Image i;
	printf("%d %d\n", Image::packer(i, Image::LA, Image::Params{4}), (i.*Image::pm)(Image::L));
}
