// std::string, whose characters are in a slice of Go's, and what is
// written with one: streams.
#include <stdio.h>
#include <string.h>
#include <string>
#include <sstream>
#include <iostream>
#include <iomanip>

struct Named {
	std::string name;
	int n;
};
static std::string greet(const std::string &who) { return "hello, " + who + '!'; }
std::ostream &operator<<(std::ostream &os, const Named &v) { return os << v.name << "#" << v.n; }

int main() {
	std::string a = "abc", b("defgh", 3), c(3, 'x'), e;
	a += b;
	a += '-';
	a.append(c).append("yz", 1);
	printf("%s %zu %d %d %s|\n", a.c_str(), a.size(), (int)a.empty(), (int)e.empty(), e.c_str());
	std::string g = greet(a.substr(0, 3));
	printf("%s %zu %zu %zu %d\n", g.c_str(), g.find("lo"), g.find('z'), g.rfind('l'), g.find("zz") == std::string::npos);
	g.insert(5, " there").erase(0, 1).replace(0, 4, "J");
	printf("%s %c %c %d %d %d\n", g.c_str(), g[0], g.back(), g == "J there, abc!", g < a, g.compare("K"));
	for (int i = 0; i < 200; i++)
		e.push_back('a' + i % 26);
	printf("%zu %s %zu\n", e.size(), e.substr(190).c_str(), strlen(e.c_str()));
	e.resize(4);
	e.pop_back();
	printf("%s %zu %s %s %d %g\n", e.c_str(), strlen(e.c_str()), std::to_string(42).c_str(), std::to_string(-7L).c_str(), std::stoi("123x"), std::stod("2.5"));
	Named *h = new Named{"heap", 7};
	Named copy = *h;
	delete h;
	std::ostringstream os;
	os << copy << ' ' << 3.25 << " " << std::fixed << std::setprecision(2) << 1.0 / 3 << " " << true << " " << 255u << std::endl;
	std::string out = os.str();
	printf("%s", out.c_str());
	std::cout << "cout " << std::setw(5) << 42 << " " << std::string("s") << std::endl;
	size_t words = 0;
	for (size_t p = 0; (p = out.find_first_of(" \n", p)) != std::string::npos; p++)
		words++;
	printf("%zu %zu %zu\n", words, out.find_first_not_of("heap"), out.find_last_of('.'));
	return 0;
}
