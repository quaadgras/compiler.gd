#include <stdio.h>
template <class C> struct traits;
template <class C, class Tr = traits<C>> class stream;
typedef stream<char> cstream;
cstream *early;
template <class C> struct traits { static int id() { return 3; } };
template <class C, class Tr> class stream {
public:
	int v = Tr::id();
};
struct derived : cstream { int w = 1; };
int main() {
	derived d;
	stream<char> s;
	early = &d;
	printf("%d %d %d\n", d.v, s.v, early->v);
}
