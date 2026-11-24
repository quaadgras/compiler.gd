// Range-based for, operator functions by name, conversion function
// templates, scoped enumerations.
#include <cstdio>
#include <initializer_list>
#include <type_traits>

template <class T>
struct List {
	struct Node {
		T value;
		Node *next;
	};
	struct Iterator {
		Node *n;
		T &operator*() const { return n->value; }
		Iterator &operator++() {
			n = n->next;
			return *this;
		}
		bool operator!=(const Iterator &o) const { return n != o.n; }
	};
	Node *head = nullptr, *tail = nullptr;
	void push(const T &v) {
		Node *n = new Node{v, nullptr};
		if (tail)
			tail->next = n;
		else
			head = n;
		tail = n;
	}
	Iterator begin() { return Iterator{head}; }
	Iterator end() { return Iterator{nullptr}; }
	~List() {
		while (head) {
			Node *n = head->next;
			delete head;
			head = n;
		}
	}
};

struct Span {
	int *p;
	int n;
};
int *begin(Span &s) { return s.p; }
int *end(Span &s) { return s.p + s.n; }

struct Row {
	int v[4];
	int &operator[](int i) { return v[i]; }
	int sum() {
		int s = 0;
		for (int i = 0; i < 4; i++)
			s += operator[](i);
		return s;
	}
};

struct Number {
	long v;
	template <class T, std::enable_if_t<std::is_arithmetic_v<T>, int> = 0>
	explicit operator T() const { return static_cast<T>(v); }
};

enum class Key : unsigned {
	NONE = 0,
	SPECIAL = 1 << 22,
	ESCAPE = SPECIAL | 0x01,
	TAB = SPECIAL | 0x02,
};

List<int> make() {
	List<int> l;
	l.push(7);
	l.push(8);
	return l;
}

int main() {
	int arr[] = {1, 2, 3, 4};
	int total = 0;
	for (int x : arr)
		total += x;
	for (int &x : arr)
		x *= 2;
	for (const auto &x : arr)
		total += x;
	printf("%d\n", total);

	List<double> l;
	l.push(1.5);
	l.push(2.5);
	l.push(3);
	double sum = 0;
	for (double &d : l) {
		if (d == 2.5)
			continue;
		d += 1;
		sum += d;
	}
	for (auto d : l)
		printf("%g ", d);
	printf("%g\n", sum);

	for (int v : make())
		printf("%d ", v);
	Span s = {arr, 3};
	for (int v : s) {
		if (v > 4)
			break;
		printf("%d ", v);
	}
	printf("\n");

	Row r = {{1, 2, 3, 4}};
	Number n = {42};
	printf("%d %d %g %d\n", r.sum(), int(n), double(n), static_cast<char>(n));
	printf("%u %u %d\n", unsigned(Key::ESCAPE), unsigned(Key::TAB), int(Key::ESCAPE == Key::TAB));
	return 0;
}
