// A member class of a template's instance is defined when it is needed.
#include <stdio.h>

template <class T>
class List {
public:
	struct Element {
		T value;
		Element *next = nullptr;
		T &get() { return value; }
	};
	class Iter {
		Element *e;

	public:
		Iter(Element *e) : e(e) {}
		bool operator!=(const Iter &o) const { return e != o.e; }
		void operator++() { e = e->next; }
		T &operator*() const { return e->value; }
	};

private:
	Element *first = nullptr, *last = nullptr;
	int n = 0;

public:
	void push_back(const T &v) {
		Element *e = new Element{v};
		if (last) {
			last->next = e;
		} else {
			first = e;
		}
		last = e;
		n++;
	}
	Iter begin() { return Iter(first); }
	Iter end() { return Iter(nullptr); }
	Element *front() { return first; }
	int size() const { return n; }
	~List() {
		while (first) {
			Element *e = first;
			first = e->next;
			delete e;
		}
	}
};

struct Utterance; // not defined yet

struct Speaker {
	List<Utterance> queue; // List<Utterance>::Element needs no Utterance here
	int id = 3;
	void say(const char *text);
	int flush();
};

struct Utterance {
	const char *text;
	int id;
};

void Speaker::say(const char *text) { queue.push_back(Utterance{text, id++}); }
int Speaker::flush() {
	int n = 0;
	for (Utterance &u : queue) {
		printf("%d %s\n", u.id, u.text);
		n++;
	}
	return n + (int)sizeof(List<Utterance>::Element) + queue.front()->get().id;
}

int main() {
	Speaker s;
	s.say("a");
	s.say("b");
	printf("%d %d\n", s.flush(), s.queue.size());
}
