// The library headers cmd/cc has for C++: function, mutex, thread,
// condition_variable, atomic, limits.
#include <atomic>
#include <condition_variable>
#include <cstdio>
#include <functional>
#include <limits>
#include <mutex>
#include <shared_mutex>
#include <thread>

int twice(int x) { return 2 * x; }
struct Adder {
	int k;
	int operator()(int x) const { return x + k; }
};
int call_fn(const std::function<int(int)> &f, int x) { return f ? f(x) : -1; }

std::mutex mu;
std::recursive_mutex rmu;
std::condition_variable cv;
std::atomic<int> hits(0);
int shared_total = 0;
bool ready = false;

void worker(int id, int n) {
	for (int i = 0; i < n; i++) {
		std::lock_guard<std::mutex> lock(mu);
		shared_total += id;
		hits++;
	}
}

int depth(int n) {
	std::lock_guard<std::recursive_mutex> lock(rmu);
	return n == 0 ? 0 : 1 + depth(n - 1);
}

int main() {
	std::function<int(int)> f = twice;
	printf("%d", f(4));
	f = Adder{10};
	printf(" %d", f(4));
	int base = 100;
	f = [base](int x) { return base - x; };
	std::function<int(int)> g = f;
	printf(" %d %d", f(4), g(1));
	std::function<void()> none;
	printf(" %d %d %d\n", call_fn(f, 0), call_fn(nullptr, 0), bool(none));
	std::function<void(int, const char *)> say = [](int n, const char *s) { printf("%d %s\n", n, s); };
	say(2, "arguments");

	std::thread a(worker, 1, 1000), b(worker, 2, 1000);
	std::thread c([]() {
		std::unique_lock<std::mutex> lock(mu);
		ready = true;
		cv.notify_one();
	});
	{
		std::unique_lock<std::mutex> lock(mu);
		cv.wait(lock, [] { return ready; });
	}
	a.join();
	b.join();
	c.join();
	printf("%d %d %d %d\n", shared_total, hits.load(), depth(5), int(a.joinable()));

	std::atomic<long> counter(5);
	counter += 3;
	long expected = 8;
	bool swapped = counter.compare_exchange_strong(expected, 20);
	std::atomic<bool> flag(false);
	long before = counter.fetch_sub(1);
	printf("%ld %d %d %ld\n", counter.load(), swapped, flag.exchange(true), before);
	std::shared_mutex sm;
	{
		std::shared_lock<std::shared_mutex> r(sm);
	}
	sm.lock();
	sm.unlock();

	printf("%d %d %u %d\n", std::numeric_limits<int>::max(), std::numeric_limits<signed char>::min(),
		std::numeric_limits<unsigned char>::max(), std::numeric_limits<long>::digits);
	printf("%g %d %d\n", double(std::numeric_limits<float>::epsilon()), std::numeric_limits<double>::is_integer,
		int(std::numeric_limits<double>::infinity() > 1e308));
	return 0;
}
