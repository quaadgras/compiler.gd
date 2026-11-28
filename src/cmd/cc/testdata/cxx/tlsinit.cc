#include <stdio.h>
struct Counter { int n = 0; int increment() { return ++n; } };
struct Thread {
	static Counter id_counter;
	static thread_local int caller_id;
	static bool is_main() { return caller_id == 1; }
};
Counter Thread::id_counter;
thread_local int Thread::caller_id = Thread::id_counter.increment();
struct S { int v; S() : v(7) {} };
thread_local S s;
int main() {
	printf("%d %d %d\n", Thread::is_main(), Thread::caller_id, s.v);
	Thread::caller_id = 5;
	printf("%d\n", Thread::caller_id);
}
