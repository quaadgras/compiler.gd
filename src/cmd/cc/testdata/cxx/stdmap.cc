// The associative containers, list, stack and string_view. (Nothing
// here depends on the order of an unordered container.)
#include <stdio.h>
#include <list>
#include <map>
#include <set>
#include <stack>
#include <string>
#include <string_view>
#include <unordered_map>
#include <unordered_set>
#include <vector>

struct Key {
	int a, b;
	bool operator==(const Key &o) const { return a == o.a && b == o.b; }
};
struct KeyHash {
	size_t operator()(const Key &k) const { return k.a * 31 + k.b; }
};
static size_t len(std::string_view v) { return v.size(); }

int main() {
	std::unordered_map<std::string, int> words;
	const char *text[] = {"the", "quick", "the", "lazy", "dog", "the", "dog"};
	for (const char *w : text)
		words[w]++;
	int total = 0;
	for (auto &kv : words)
		total += kv.second * (int)kv.first.size();
	printf("%zu %d %d %d %d %d\n", words.size(), words["the"], words.at("dog"), (int)words.count("cat"), total, words.find("quick") != words.end());
	words.erase("the");
	auto ins = words.insert({"fox", 9});
	auto again = words.emplace("fox", 1);
	printf("%zu %d %d %d\n", words.size(), ins.second, again.second, again.first->second);

	std::unordered_map<Key, std::vector<int>, KeyHash> grid;
	for (int i = 0; i < 1000; i++)
		grid[Key{i % 10, i % 7}].push_back(i);
	std::unordered_map<int, int *> ptrs;
	int cell = 5;
	ptrs[3] = &cell;
	*ptrs[3] += 1;
	std::unordered_map<Key, std::vector<int>, KeyHash> copy = grid;
	grid.clear();
	printf("%zu %zu %zu %d %d\n", copy.size(), copy[Key{3, 3}].size(), grid.size(), copy[Key{9, 6}].back(), cell);

	std::unordered_set<int> seen;
	int dups = 0;
	for (int v : {4, 8, 4, 15, 16, 8, 23, 42})
		dups += !seen.insert(v).second;
	std::unordered_set<std::string> names = {"a", "b"};
	printf("%zu %d %d %d\n", seen.size(), dups, (int)seen.count(15), (int)names.count("b"));

	std::map<std::string, double> prices;
	prices["pear"] = 2.5;
	prices["apple"] = 1.25;
	prices["fig"] = 4;
	prices.insert({"apple", 99});
	for (auto &kv : prices)
		printf("%s=%g ", kv.first.c_str(), kv.second);
	auto lb = prices.lower_bound("b");
	auto last = prices.end();
	--last;
	printf("| %s %s %zu\n", lb->first.c_str(), last->first.c_str(), prices.size());
	prices.erase("fig");
	std::map<int, int> sq;
	for (int i = 20; i > 0; i--)
		sq[i * 7 % 20] = i;
	int s = 0, prev = -1, ordered = 1;
	for (auto it = sq.begin(); it != sq.end(); ++it) {
		ordered = ordered && it->first > prev;
		prev = it->first;
		s += it->second;
	}
	printf("%zu %d %d %d %d\n", sq.size(), ordered, s, sq.begin()->first, sq.find(7) == sq.end());

	std::set<int> ordset = {5, 1, 9, 1, 3};
	std::multiset<int> multi = {5, 1, 9, 1, 3};
	for (int v : ordset)
		printf("%d ", v);
	printf("| %zu %zu %d\n", multi.size(), multi.count(1), *ordset.upper_bound(3));

	std::list<std::string> l = {"b", "c"};
	l.push_front("a");
	l.push_back("d");
	auto it = l.begin();
	++it;
	l.insert(it, "x");
	l.remove("c");
	l.reverse();
	for (auto &v : l)
		printf("%s", v.c_str());
	std::stack<int> st;
	st.push(1);
	st.push(2);
	st.pop();
	std::string str = "hello world";
	std::string_view sv = str;
	printf(" %zu %d %zu %zu %d %s\n", l.size(), st.top(), len(str), sv.find("wor"), sv.substr(0, 5) == std::string_view("hello"), std::string(sv.substr(6)).c_str());
	return 0;
}
