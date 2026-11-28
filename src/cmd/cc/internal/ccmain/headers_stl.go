// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The containers of C++'s library. Their elements are in a slice of
// Go's (std::__go_slice<T> is []T: libcpkg.go's goTypes), which
// internal/c23/cxx makes and grows; the headers construct, move and
// destroy the elements, as C++ says. A header never names a member of
// __go_slice: they only give it a slice's size.
func init() {
	sysHeaders["__go_slice"] = `#ifndef __CC_GO_SLICE
#define __CC_GO_SLICE
#include <stddef.h>
#include <new>
#include <utility>
#include <limits>
namespace std {
// As large as Go's slice, and aligned as it: two words that are aligned
// to 16 bytes where a pointer is 8 (doc/gd/slice16.md), three where it
// is 4.
#ifdef __ILP32__
template <class T> struct __go_slice {
	void *__p;
	long __n, __c;
};
#else
template <class T> struct alignas(16) __go_slice {
	void *__p;
	long __n;
};
#endif
template <class T> size_t __go_SliceLen(const __go_slice<T> *s);
template <class T> size_t __go_SliceCap(const __go_slice<T> *s);
template <class T> T *__go_SliceData(const __go_slice<T> *s);
template <class T> T *__go_SliceEnd(const __go_slice<T> *s);
template <class T> T *__go_SliceAt(const __go_slice<T> *s, size_t i);
template <class T> void __go_SliceReset(__go_slice<T> *s);
template <class T> T *__go_SliceRealloc(__go_slice<T> *s, size_t n);
template <class T> void __go_SliceSetLen(__go_slice<T> *s, size_t n);
template <class T> void __go_SliceSwap(__go_slice<T> *a, __go_slice<T> *b);
template <class T> void __go_SliceTake(__go_slice<T> *a, __go_slice<T> *b);
template <class T> struct allocator {
	typedef T value_type;
	allocator() = default;
	template <class U> allocator(const allocator<U> &) {}
};
// What a vector, a string and a deque are made of: elements one after
// another in a slice.
template <class T>
class __seq {
protected:
	__go_slice<T> __s;
	// Room for n elements: a new array, with the elements moved to it.
	void __room(size_t n) {
		size_t c = __go_SliceCap(&__s);
		if (n <= c)
			return;
		if (n < 2 * c)
			n = 2 * c;
		if (n < 4)
			n = 4;
		size_t len = __go_SliceLen(&__s);
		T *old = __go_SliceRealloc(&__s, n);
		T *d = __go_SliceData(&__s);
		for (size_t i = 0; i < len; i++) {
			new ((void *)(d + i)) T(static_cast<T &&>(old[i]));
			old[i].~T();
		}
	}
	// One more element at the end, not constructed.
	T *__more() {
		size_t n = __go_SliceLen(&__s);
		__room(n + 1);
		__go_SliceSetLen(&__s, n + 1);
		return __go_SliceData(&__s) + n;
	}
	// The elements from n on are destroyed.
	void __cut(size_t n) {
		size_t len = __go_SliceLen(&__s);
		T *d = __go_SliceData(&__s);
		for (size_t i = n; i < len; i++)
			d[i].~T();
		if (n < len)
			__go_SliceSetLen(&__s, n);
	}
	// A gap of k elements at i: what was there is moved up. The gap's
	// elements are not constructed.
	T *__gap(size_t i, size_t k) {
		size_t len = __go_SliceLen(&__s);
		__room(len + k);
		__go_SliceSetLen(&__s, len + k);
		T *d = __go_SliceData(&__s);
		for (size_t j = len; j > i; j--) {
			new ((void *)(d + j - 1 + k)) T(static_cast<T &&>(d[j - 1]));
			d[j - 1].~T();
		}
		return d + i;
	}
	// The k elements at i are destroyed, and what was after them is
	// moved down.
	void __close(size_t i, size_t k) {
		size_t len = __go_SliceLen(&__s);
		T *d = __go_SliceData(&__s);
		for (size_t j = i; j < i + k; j++)
			d[j].~T();
		for (size_t j = i + k; j < len; j++) {
			new ((void *)(d + j - k)) T(static_cast<T &&>(d[j]));
			d[j].~T();
		}
		__go_SliceSetLen(&__s, len - k);
	}
	__seq() { __go_SliceReset(&__s); }
	~__seq() {
		__cut(0);
		__go_SliceReset(&__s);
	}
};
}
#endif
`
	sysHeaders["vector"] = `#ifndef __CC_VECTOR
#define __CC_VECTOR
#include <__go_slice>
#include <initializer_list>
#include <iterator>
namespace std {
template <class T, class A = allocator<T>>
class vector : protected __seq<T> {
	using __seq<T>::__s;

public:
	typedef T value_type;
	typedef A allocator_type;
	typedef size_t size_type;
	typedef ptrdiff_t difference_type;
	typedef T &reference;
	typedef const T &const_reference;
	typedef T *pointer;
	typedef const T *const_pointer;
	typedef T *iterator;
	typedef const T *const_iterator;
	typedef std::reverse_iterator<T *> reverse_iterator;
	typedef std::reverse_iterator<const T *> const_reverse_iterator;

	vector() noexcept {}
	explicit vector(const A &) noexcept {}
	explicit vector(size_t n) { resize(n); }
	vector(size_t n, const T &v) { assign(n, v); }
	vector(const vector &o) {
		this->__room(o.size());
		for (const T *p = o.begin(), *e = o.end(); p != e; ++p)
			new ((void *)this->__more()) T(*p);
	}
	vector(vector &&o) noexcept { __go_SliceTake(&__s, &o.__s); }
	vector(initializer_list<T> l) {
		this->__room(l.size());
		for (const T *p = l.begin(), *e = l.end(); p != e; ++p)
			new ((void *)this->__more()) T(*p);
	}
	template <class I, class = enable_if_t<!is_integral_v<I>>>
	vector(I first, I last) {
		for (; first != last; ++first)
			new ((void *)this->__more()) T(*first);
	}
	~vector() {}
	vector &operator=(const vector &o) {
		if (this != &o) {
			clear();
			this->__room(o.size());
			for (const T *p = o.begin(), *e = o.end(); p != e; ++p)
				new ((void *)this->__more()) T(*p);
		}
		return *this;
	}
	vector &operator=(vector &&o) noexcept {
		if (this != &o) {
			clear();
			__go_SliceTake(&__s, &o.__s);
		}
		return *this;
	}
	vector &operator=(initializer_list<T> l) {
		assign(l.begin(), l.end());
		return *this;
	}
	void assign(size_t n, const T &v) {
		clear();
		this->__room(n);
		for (size_t i = 0; i < n; i++)
			new ((void *)this->__more()) T(v);
	}
	template <class I, class = enable_if_t<!is_integral_v<I>>>
	void assign(I first, I last) {
		clear();
		for (; first != last; ++first)
			new ((void *)this->__more()) T(*first);
	}
	void assign(initializer_list<T> l) { assign(l.begin(), l.end()); }

	size_t size() const noexcept { return __go_SliceLen(&__s); }
	size_t max_size() const noexcept { return (size_t)-1 / 2 / sizeof(T); }
	size_t capacity() const noexcept { return __go_SliceCap(&__s); }
	bool empty() const noexcept { return __go_SliceLen(&__s) == 0; }
	void reserve(size_t n) { this->__room(n); }
	void shrink_to_fit() {}
	void resize(size_t n) {
		size_t len = size();
		if (n < len) {
			this->__cut(n);
			return;
		}
		this->__room(n);
		for (size_t i = len; i < n; i++)
			new ((void *)this->__more()) T();
	}
	void resize(size_t n, const T &v) {
		size_t len = size();
		if (n < len) {
			this->__cut(n);
			return;
		}
		this->__room(n);
		for (size_t i = len; i < n; i++)
			new ((void *)this->__more()) T(v);
	}
	void clear() noexcept { this->__cut(0); }

	T &operator[](size_t i) { return *__go_SliceAt(&__s, i); }
	const T &operator[](size_t i) const { return *__go_SliceAt(&__s, i); }
	T &at(size_t i) { return *__go_SliceAt(&__s, i); }
	const T &at(size_t i) const { return *__go_SliceAt(&__s, i); }
	T &front() { return *__go_SliceAt(&__s, 0); }
	const T &front() const { return *__go_SliceAt(&__s, 0); }
	T &back() { return *__go_SliceAt(&__s, size() - 1); }
	const T &back() const { return *__go_SliceAt(&__s, size() - 1); }
	T *data() noexcept { return __go_SliceData(&__s); }
	const T *data() const noexcept { return __go_SliceData(&__s); }

	T *begin() noexcept { return __go_SliceData(&__s); }
	const T *begin() const noexcept { return __go_SliceData(&__s); }
	T *end() noexcept { return __go_SliceEnd(&__s); }
	const T *end() const noexcept { return __go_SliceEnd(&__s); }
	const T *cbegin() const noexcept { return __go_SliceData(&__s); }
	const T *cend() const noexcept { return __go_SliceEnd(&__s); }
	reverse_iterator rbegin() noexcept { return reverse_iterator(end()); }
	const_reverse_iterator rbegin() const noexcept { return const_reverse_iterator(end()); }
	reverse_iterator rend() noexcept { return reverse_iterator(begin()); }
	const_reverse_iterator rend() const noexcept { return const_reverse_iterator(begin()); }
	const_reverse_iterator crbegin() const noexcept { return const_reverse_iterator(end()); }
	const_reverse_iterator crend() const noexcept { return const_reverse_iterator(begin()); }

	void push_back(const T &v) {
		if (size() == capacity()) {
			// (v may be an element: it is copied before they move.)
			T c(v);
			new ((void *)this->__more()) T(static_cast<T &&>(c));
			return;
		}
		new ((void *)this->__more()) T(v);
	}
	void push_back(T &&v) {
		if (size() == capacity()) {
			T c(static_cast<T &&>(v));
			new ((void *)this->__more()) T(static_cast<T &&>(c));
			return;
		}
		new ((void *)this->__more()) T(static_cast<T &&>(v));
	}
	template <class... Args>
	T &emplace_back(Args &&...args) {
		if (size() == capacity()) {
			T c(static_cast<Args &&>(args)...);
			return *new ((void *)this->__more()) T(static_cast<T &&>(c));
		}
		return *new ((void *)this->__more()) T(static_cast<Args &&>(args)...);
	}
	void pop_back() { this->__cut(size() - 1); }

	T *insert(const T *pos, const T &v) {
		size_t i = pos - begin();
		T c(v);
		return new ((void *)this->__gap(i, 1)) T(static_cast<T &&>(c));
	}
	T *insert(const T *pos, T &&v) {
		size_t i = pos - begin();
		T c(static_cast<T &&>(v));
		return new ((void *)this->__gap(i, 1)) T(static_cast<T &&>(c));
	}
	T *insert(const T *pos, size_t n, const T &v) {
		size_t i = pos - begin();
		T c(v);
		T *g = this->__gap(i, n);
		for (size_t j = 0; j < n; j++)
			new ((void *)(g + j)) T(c);
		return g;
	}
	template <class I, class = enable_if_t<!is_integral_v<I>>>
	T *insert(const T *pos, I first, I last) {
		size_t i = pos - begin(), n = 0;
		for (I it = first; it != last; ++it)
			n++;
		T *g = this->__gap(i, n);
		for (size_t j = 0; j < n; j++, ++first)
			new ((void *)(g + j)) T(*first);
		return g;
	}
	T *insert(const T *pos, initializer_list<T> l) { return insert(pos, l.begin(), l.end()); }
	template <class... Args>
	T *emplace(const T *pos, Args &&...args) {
		size_t i = pos - begin();
		T c(static_cast<Args &&>(args)...);
		return new ((void *)this->__gap(i, 1)) T(static_cast<T &&>(c));
	}
	T *erase(const T *pos) {
		size_t i = pos - begin();
		this->__close(i, 1);
		return begin() + i;
	}
	T *erase(const T *first, const T *last) {
		size_t i = first - begin();
		if (first != last)
			this->__close(i, last - first);
		return begin() + i;
	}
	void swap(vector &o) noexcept { __go_SliceSwap(&__s, &o.__s); }
};
template <class T, class A>
bool operator==(const vector<T, A> &a, const vector<T, A> &b) {
	if (a.size() != b.size())
		return false;
	for (size_t i = 0, n = a.size(); i < n; i++)
		if (!(a[i] == b[i]))
			return false;
	return true;
}
template <class T, class A> bool operator!=(const vector<T, A> &a, const vector<T, A> &b) { return !(a == b); }
template <class T, class A>
bool operator<(const vector<T, A> &a, const vector<T, A> &b) {
	size_t n = a.size() < b.size() ? a.size() : b.size();
	for (size_t i = 0; i < n; i++) {
		if (a[i] < b[i])
			return true;
		if (b[i] < a[i])
			return false;
	}
	return a.size() < b.size();
}
template <class T, class A> void swap(vector<T, A> &a, vector<T, A> &b) noexcept { a.swap(b); }
}
#endif
`
	sysHeaders["iterator"] = `#ifndef __CC_ITERATOR
#define __CC_ITERATOR
#include <stddef.h>
#include <initializer_list>
namespace std {
struct input_iterator_tag {};
struct output_iterator_tag {};
struct forward_iterator_tag : input_iterator_tag {};
struct bidirectional_iterator_tag : forward_iterator_tag {};
struct random_access_iterator_tag : bidirectional_iterator_tag {};
template <class I>
struct iterator_traits {
	typedef typename I::iterator_category iterator_category;
	typedef typename I::value_type value_type;
	typedef typename I::difference_type difference_type;
	typedef typename I::pointer pointer;
	typedef typename I::reference reference;
};
template <class T>
struct iterator_traits<T *> {
	typedef random_access_iterator_tag iterator_category;
	typedef T value_type;
	typedef ptrdiff_t difference_type;
	typedef T *pointer;
	typedef T &reference;
};
template <class T>
struct iterator_traits<const T *> {
	typedef random_access_iterator_tag iterator_category;
	typedef T value_type;
	typedef ptrdiff_t difference_type;
	typedef const T *pointer;
	typedef const T &reference;
};
template <class I>
class reverse_iterator {
	I __i;

public:
	typedef I iterator_type;
	typedef typename iterator_traits<I>::iterator_category iterator_category;
	typedef typename iterator_traits<I>::value_type value_type;
	typedef typename iterator_traits<I>::difference_type difference_type;
	typedef typename iterator_traits<I>::pointer pointer;
	typedef typename iterator_traits<I>::reference reference;
	reverse_iterator() : __i() {}
	explicit reverse_iterator(I i) : __i(i) {}
	template <class U> reverse_iterator(const reverse_iterator<U> &o) : __i(o.base()) {}
	I base() const { return __i; }
	reference operator*() const {
		I t = __i;
		--t;
		return *t;
	}
	pointer operator->() const { return &**this; }
	reverse_iterator &operator++() {
		--__i;
		return *this;
	}
	reverse_iterator operator++(int) {
		reverse_iterator t = *this;
		--__i;
		return t;
	}
	reverse_iterator &operator--() {
		++__i;
		return *this;
	}
	reverse_iterator operator--(int) {
		reverse_iterator t = *this;
		++__i;
		return t;
	}
	reverse_iterator operator+(difference_type n) const { return reverse_iterator(__i - n); }
	reverse_iterator operator-(difference_type n) const { return reverse_iterator(__i + n); }
	reverse_iterator &operator+=(difference_type n) {
		__i -= n;
		return *this;
	}
	reverse_iterator &operator-=(difference_type n) {
		__i += n;
		return *this;
	}
	reference operator[](difference_type n) const { return *(*this + n); }
};
template <class I, class J> bool operator==(const reverse_iterator<I> &a, const reverse_iterator<J> &b) { return a.base() == b.base(); }
template <class I, class J> bool operator!=(const reverse_iterator<I> &a, const reverse_iterator<J> &b) { return a.base() != b.base(); }
template <class I, class J> bool operator<(const reverse_iterator<I> &a, const reverse_iterator<J> &b) { return b.base() < a.base(); }
template <class I, class J> auto operator-(const reverse_iterator<I> &a, const reverse_iterator<J> &b) -> decltype(b.base() - a.base()) { return b.base() - a.base(); }
template <class C>
class back_insert_iterator {
	C *__c;

public:
	typedef output_iterator_tag iterator_category;
	typedef void value_type;
	typedef ptrdiff_t difference_type;
	typedef void pointer;
	typedef void reference;
	typedef C container_type;
	explicit back_insert_iterator(C &c) : __c(&c) {}
	back_insert_iterator &operator=(const typename C::value_type &v) {
		__c->push_back(v);
		return *this;
	}
	back_insert_iterator &operator=(typename C::value_type &&v) {
		__c->push_back(static_cast<typename C::value_type &&>(v));
		return *this;
	}
	back_insert_iterator &operator*() { return *this; }
	back_insert_iterator &operator++() { return *this; }
	back_insert_iterator operator++(int) { return *this; }
};
template <class C> back_insert_iterator<C> back_inserter(C &c) { return back_insert_iterator<C>(c); }
template <class I> typename iterator_traits<I>::difference_type distance(I first, I last) {
	typename iterator_traits<I>::difference_type n = 0;
	for (; first != last; ++first)
		n++;
	return n;
}
template <class I, class D> void advance(I &i, D n) {
	for (; n > 0; n--)
		++i;
	for (; n < 0; n++)
		--i;
}
template <class I> I next(I i, typename iterator_traits<I>::difference_type n = 1) {
	advance(i, n);
	return i;
}
template <class I> I prev(I i, typename iterator_traits<I>::difference_type n = 1) {
	advance(i, -n);
	return i;
}
template <class C> auto begin(C &c) -> decltype(c.begin()) { return c.begin(); }
template <class C> auto begin(const C &c) -> decltype(c.begin()) { return c.begin(); }
template <class C> auto end(C &c) -> decltype(c.end()) { return c.end(); }
template <class C> auto end(const C &c) -> decltype(c.end()) { return c.end(); }
template <class T, size_t N> T *begin(T (&a)[N]) { return a; }
template <class T, size_t N> T *end(T (&a)[N]) { return a + N; }
template <class C> auto size(const C &c) -> decltype(c.size()) { return c.size(); }
template <class T, size_t N> size_t size(const T (&)[N]) { return N; }
}
#endif
`
}
