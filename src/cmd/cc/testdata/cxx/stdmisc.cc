// deque, queue, priority_queue, optional, unique_ptr and the algorithms.
#include <stdio.h>
#include <algorithm>
#include <deque>
#include <memory>
#include <numeric>
#include <optional>
#include <queue>
#include <string>
#include <vector>
#include <functional>

static int live;
struct Node {
	int v;
	std::unique_ptr<Node> next;
	explicit Node(int v) : v(v) { live++; }
	~Node() { live--; }
};
struct Item {
	int key;
	std::string name;
};

int main() {
	std::deque<Item> d;
	Item *first = nullptr;
	for (int i = 0; i < 100; i++) {
		d.push_back(Item{i, "n" + std::to_string(i)});
		if (i == 0)
			first = &d.front();
		if (i % 3 == 0)
			d.push_front(Item{-i, "f"});
	}
	printf("%zu %d %d %s %d %d\n", d.size(), d.front().key, d.back().key, d[40].name.c_str(), first->key, (int)(d.end() - d.begin()));
	d.pop_front();
	d.pop_back();
	int s = 0;
	for (auto &it : d)
		s += it.key;
	printf("%d %zu\n", s, d.size());
	d = std::deque<Item>();
	printf("%zu\n", d.size());

	std::priority_queue<long> pq;
	for (long v : {5L, 1L, 9L, 3L, 9L, 7L})
		pq.push(v);
	while (!pq.empty()) {
		printf("%ld ", pq.top());
		pq.pop();
	}
	std::priority_queue<int, std::vector<int>, std::greater<int>> mn;
	mn.push(4);
	mn.push(2);
	mn.push(8);
	std::queue<int> q;
	q.push(1);
	q.push(2);
	q.pop();
	printf("| %d %d %zu\n", mn.top(), q.front(), q.size());

	std::optional<size_t> o;
	printf("%d ", (int)o.has_value());
	o = 5;
	std::optional<std::string> os = std::string("opt");
	std::optional<std::string> os2 = os;
	os.reset();
	printf("%zu %d %s %d %s\n", *o, (int)os.has_value(), os2->c_str(), o == std::optional<size_t>(5), os.value_or("none").c_str());

	{
		std::unique_ptr<Node> head = std::make_unique<Node>(1);
		head->next = std::make_unique<Node>(2);
		head->next->next.reset(new Node(3));
		std::vector<std::unique_ptr<Node>> owned;
		owned.push_back(std::move(head));
		owned.emplace_back(new Node(4));
		printf("%d %d %d %d\n", owned[0]->next->next->v, owned[1]->v, (int)!head, live);
		auto arr = std::make_unique<int[]>(4);
		arr[3] = 9;
		printf("%d\n", arr[3] + arr[0]);
	}
	printf("%d\n", live);

	std::vector<int> v = {5, 3, 8, 3, 1, 9, 3};
	std::vector<int> w;
	std::transform(v.begin(), v.end(), std::back_inserter(w), [](int x) { return x * 2; });
	std::sort(v.begin(), v.end());
	v.erase(std::unique(v.begin(), v.end()), v.end());
	std::reverse(w.begin(), w.end());
	printf("%zu %d %d %d %ld %d\n", v.size(), v[1], w[0], std::accumulate(w.begin(), w.end(), 0), (long)std::count_if(w.begin(), w.end(), [](int x) { return x > 6; }), *std::lower_bound(v.begin(), v.end(), 4));
	std::vector<Item> items;
	for (int i = 0; i < 40; i++)
		items.push_back(Item{i % 4, std::to_string(i)});
	std::stable_sort(items.begin(), items.end(), [](const Item &a, const Item &b) { return a.key < b.key; });
	printf("%s %s %s %s\n", items[0].name.c_str(), items[9].name.c_str(), items[10].name.c_str(), items[39].name.c_str());
	std::function<int(int)> twice = [](int x) { return 2 * x; };
	printf("%d %d\n", twice(21), (int)std::any_of(v.begin(), v.end(), [](int x) { return x == 9; }));
	return 0;
}
