// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// More of C++'s library: what is made of the containers of
// headers_stl.go.
func init() {
	sysHeaders["string"] = `#ifndef __CC_STRING
#define __CC_STRING
#include <__go_slice>
#include <initializer_list>
#include <iterator>
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
namespace std {
template <class C>
struct char_traits {
	typedef C char_type;
	static size_t length(const C *s) {
		size_t n = 0;
		while (s[n] != C())
			n++;
		return n;
	}
};
// The characters are in a slice of Go's, with a zero after the last:
// the array has one more element than its capacity says, and what a
// string drops is zeroed.
template <class C, class Tr = char_traits<C>, class A = allocator<C>>
class basic_string : protected __seq<C> {
	using __seq<C>::__s;
	static const C *__empty() {
		static const C z[1] = {C()};
		return z;
	}
	void __add(const C *p, size_t n) {
		if (n == 0)
			return;
		size_t len = size();
		if (len + n > capacity()) {
			// (p may be of this string.)
			basic_string t;
			t.__room(len + n);
			t.__add(data(), len);
			t.__add(p, n);
			__go_SliceSwap(&__s, &t.__s);
			return;
		}
		__go_SliceSetLen(&__s, len + n);
		C *d = __go_SliceData(&__s) + len;
		for (size_t i = 0; i < n; i++)
			d[i] = p[i];
	}

public:
	typedef Tr traits_type;
	typedef C value_type;
	typedef A allocator_type;
	typedef size_t size_type;
	typedef ptrdiff_t difference_type;
	typedef C &reference;
	typedef const C &const_reference;
	typedef C *pointer;
	typedef const C *const_pointer;
	typedef C *iterator;
	typedef const C *const_iterator;
	typedef std::reverse_iterator<C *> reverse_iterator;
	typedef std::reverse_iterator<const C *> const_reverse_iterator;
	static const size_t npos = (size_t)-1;

	basic_string() noexcept {}
	basic_string(const basic_string &o) { __add(o.data(), o.size()); }
	basic_string(basic_string &&o) noexcept { __go_SliceTake(&__s, &o.__s); }
	basic_string(const basic_string &o, size_t pos, size_t n = npos) {
		size_t len = o.size();
		if (pos > len)
			pos = len;
		if (n > len - pos)
			n = len - pos;
		__add(o.data() + pos, n);
	}
	basic_string(const C *s) { __add(s, Tr::length(s)); }
	basic_string(const C *s, size_t n) { __add(s, n); }
	basic_string(size_t n, C c) { append(n, c); }
	basic_string(initializer_list<C> l) { __add(l.begin(), l.size()); }
	template <class I, class = enable_if_t<!is_integral_v<I>>>
	basic_string(I first, I last) {
		for (; first != last; ++first)
			push_back(*first);
	}
	~basic_string() {}
	basic_string &operator=(const basic_string &o) {
		if (this != &o) {
			clear();
			__add(o.data(), o.size());
		}
		return *this;
	}
	basic_string &operator=(basic_string &&o) noexcept {
		if (this != &o) {
			clear();
			__go_SliceTake(&__s, &o.__s);
		}
		return *this;
	}
	basic_string &operator=(const C *s) {
		basic_string t(s);
		__go_SliceSwap(&__s, &t.__s);
		return *this;
	}
	basic_string &operator=(C c) {
		clear();
		push_back(c);
		return *this;
	}
	basic_string &assign(const basic_string &o) { return *this = o; }
	basic_string &assign(const C *s) { return *this = s; }
	basic_string &assign(const C *s, size_t n) {
		basic_string t(s, n);
		__go_SliceSwap(&__s, &t.__s);
		return *this;
	}
	basic_string &assign(size_t n, C c) {
		clear();
		return append(n, c);
	}

	size_t size() const noexcept { return __go_SliceLen(&__s); }
	size_t length() const noexcept { return __go_SliceLen(&__s); }
	size_t max_size() const noexcept { return (size_t)-1 / 2; }
	size_t capacity() const noexcept { return __go_SliceCap(&__s); }
	bool empty() const noexcept { return __go_SliceLen(&__s) == 0; }
	void reserve(size_t n = 0) { this->__room(n); }
	void shrink_to_fit() {}
	void clear() noexcept {
		if (size() != 0)
			__go_SliceSetLen(&__s, 0);
	}
	void resize(size_t n, C c = C()) {
		size_t len = size();
		if (n <= len) {
			if (n < len)
				__go_SliceSetLen(&__s, n);
			return;
		}
		append(n - len, c);
	}

	C &operator[](size_t i) { return __go_SliceData(&__s)[i]; }
	const C &operator[](size_t i) const { return data()[i]; }
	C &at(size_t i) { return *__go_SliceAt(&__s, i); }
	const C &at(size_t i) const { return *__go_SliceAt(&__s, i); }
	C &front() { return *__go_SliceAt(&__s, 0); }
	const C &front() const { return *__go_SliceAt(&__s, 0); }
	C &back() { return *__go_SliceAt(&__s, size() - 1); }
	const C &back() const { return *__go_SliceAt(&__s, size() - 1); }
	const C *c_str() const noexcept {
		const C *p = __go_SliceData(&__s);
		return p ? p : __empty();
	}
	const C *data() const noexcept {
		const C *p = __go_SliceData(&__s);
		return p ? p : __empty();
	}
	C *data() noexcept { return __go_SliceData(&__s); }

	C *begin() noexcept { return __go_SliceData(&__s); }
	const C *begin() const noexcept { return __go_SliceData(&__s); }
	C *end() noexcept { return __go_SliceEnd(&__s); }
	const C *end() const noexcept { return __go_SliceEnd(&__s); }
	const C *cbegin() const noexcept { return __go_SliceData(&__s); }
	const C *cend() const noexcept { return __go_SliceEnd(&__s); }
	reverse_iterator rbegin() noexcept { return reverse_iterator(end()); }
	const_reverse_iterator rbegin() const noexcept { return const_reverse_iterator(end()); }
	reverse_iterator rend() noexcept { return reverse_iterator(begin()); }
	const_reverse_iterator rend() const noexcept { return const_reverse_iterator(begin()); }

	void push_back(C c) { __add(&c, 1); }
	void pop_back() { __go_SliceSetLen(&__s, size() - 1); }
	basic_string &append(const basic_string &o) {
		__add(o.data(), o.size());
		return *this;
	}
	basic_string &append(const basic_string &o, size_t pos, size_t n = npos) {
		size_t len = o.size();
		if (pos > len)
			pos = len;
		if (n > len - pos)
			n = len - pos;
		__add(o.data() + pos, n);
		return *this;
	}
	basic_string &append(const C *s) {
		__add(s, Tr::length(s));
		return *this;
	}
	basic_string &append(const C *s, size_t n) {
		__add(s, n);
		return *this;
	}
	basic_string &append(size_t n, C c) {
		this->__room(size() + n);
		for (size_t i = 0; i < n; i++)
			__add(&c, 1);
		return *this;
	}
	template <class I, class = enable_if_t<!is_integral_v<I>>>
	basic_string &append(I first, I last) {
		for (; first != last; ++first)
			push_back(*first);
		return *this;
	}
	basic_string &operator+=(const basic_string &o) { return append(o); }
	basic_string &operator+=(const C *s) { return append(s); }
	basic_string &operator+=(C c) {
		push_back(c);
		return *this;
	}
	basic_string &insert(size_t pos, const basic_string &o) { return insert(pos, o.data(), o.size()); }
	basic_string &insert(size_t pos, const C *s) { return insert(pos, s, Tr::length(s)); }
	basic_string &insert(size_t pos, const C *s, size_t n) {
		basic_string t(*this, 0, pos);
		t.__add(s, n);
		t.append(*this, pos);
		__go_SliceSwap(&__s, &t.__s);
		return *this;
	}
	basic_string &insert(size_t pos, size_t n, C c) {
		basic_string t(n, c);
		return insert(pos, t);
	}
	C *insert(const C *p, C c) {
		size_t pos = p - begin();
		insert(pos, 1, c);
		return begin() + pos;
	}
	basic_string &erase(size_t pos = 0, size_t n = npos) {
		size_t len = size();
		if (pos > len)
			pos = len;
		if (n > len - pos)
			n = len - pos;
		if (n != 0)
			this->__close(pos, n);
		return *this;
	}
	C *erase(const C *p) {
		size_t pos = p - begin();
		erase(pos, 1);
		return begin() + pos;
	}
	C *erase(const C *first, const C *last) {
		size_t pos = first - begin();
		erase(pos, last - first);
		return begin() + pos;
	}
	basic_string &replace(size_t pos, size_t n, const basic_string &o) { return replace(pos, n, o.data(), o.size()); }
	basic_string &replace(size_t pos, size_t n, const C *s) { return replace(pos, n, s, Tr::length(s)); }
	basic_string &replace(size_t pos, size_t n, const C *s, size_t k) {
		size_t len = size();
		if (pos > len)
			pos = len;
		if (n > len - pos)
			n = len - pos;
		basic_string t(*this, 0, pos);
		t.__add(s, k);
		t.append(*this, pos + n);
		__go_SliceSwap(&__s, &t.__s);
		return *this;
	}
	basic_string substr(size_t pos = 0, size_t n = npos) const { return basic_string(*this, pos, n); }
	void swap(basic_string &o) noexcept { __go_SliceSwap(&__s, &o.__s); }
	size_t copy(C *dst, size_t n, size_t pos = 0) const {
		size_t len = size();
		if (pos > len)
			pos = len;
		if (n > len - pos)
			n = len - pos;
		const C *d = data();
		for (size_t i = 0; i < n; i++)
			dst[i] = d[pos + i];
		return n;
	}

	int compare(const C *s, size_t n) const {
		const C *d = data();
		size_t len = size(), m = len < n ? len : n;
		for (size_t i = 0; i < m; i++) {
			if (d[i] < s[i])
				return -1;
			if (s[i] < d[i])
				return 1;
		}
		return len < n ? -1 : len > n ? 1 : 0;
	}
	int compare(const basic_string &o) const { return compare(o.data(), o.size()); }
	int compare(const C *s) const { return compare(s, Tr::length(s)); }
	int compare(size_t pos, size_t n, const basic_string &o) const { return substr(pos, n).compare(o); }
	int compare(size_t pos, size_t n, const C *s) const { return substr(pos, n).compare(s); }

	size_t find(const C *s, size_t pos, size_t n) const {
		size_t len = size();
		const C *d = data();
		if (n > len)
			return npos;
		for (size_t i = pos; i + n <= len; i++) {
			size_t j = 0;
			while (j < n && d[i + j] == s[j])
				j++;
			if (j == n)
				return i;
		}
		return npos;
	}
	size_t find(const basic_string &o, size_t pos = 0) const { return find(o.data(), pos, o.size()); }
	size_t find(const C *s, size_t pos = 0) const { return find(s, pos, Tr::length(s)); }
	size_t find(C c, size_t pos = 0) const { return find(&c, pos, 1); }
	size_t rfind(const C *s, size_t pos, size_t n) const {
		size_t len = size();
		const C *d = data();
		if (n > len)
			return npos;
		size_t i = len - n;
		if (pos < i)
			i = pos;
		for (;; i--) {
			size_t j = 0;
			while (j < n && d[i + j] == s[j])
				j++;
			if (j == n)
				return i;
			if (i == 0)
				return npos;
		}
	}
	size_t rfind(const basic_string &o, size_t pos = npos) const { return rfind(o.data(), pos, o.size()); }
	size_t rfind(const C *s, size_t pos = npos) const { return rfind(s, pos, Tr::length(s)); }
	size_t rfind(C c, size_t pos = npos) const { return rfind(&c, pos, 1); }
	size_t __find_of(const C *s, size_t n, size_t pos, bool in) const {
		size_t len = size();
		const C *d = data();
		for (size_t i = pos; i < len; i++) {
			bool has = false;
			for (size_t j = 0; j < n; j++)
				has = has || d[i] == s[j];
			if (has == in)
				return i;
		}
		return npos;
	}
	size_t __rfind_of(const C *s, size_t n, size_t pos, bool in) const {
		size_t len = size();
		const C *d = data();
		if (len == 0)
			return npos;
		size_t i = len - 1;
		if (pos < i)
			i = pos;
		for (;; i--) {
			bool has = false;
			for (size_t j = 0; j < n; j++)
				has = has || d[i] == s[j];
			if (has == in)
				return i;
			if (i == 0)
				return npos;
		}
	}
	size_t find_first_of(const basic_string &o, size_t pos = 0) const { return __find_of(o.data(), o.size(), pos, true); }
	size_t find_first_of(const C *s, size_t pos = 0) const { return __find_of(s, Tr::length(s), pos, true); }
	size_t find_first_of(C c, size_t pos = 0) const { return __find_of(&c, 1, pos, true); }
	size_t find_first_not_of(const basic_string &o, size_t pos = 0) const { return __find_of(o.data(), o.size(), pos, false); }
	size_t find_first_not_of(const C *s, size_t pos = 0) const { return __find_of(s, Tr::length(s), pos, false); }
	size_t find_first_not_of(C c, size_t pos = 0) const { return __find_of(&c, 1, pos, false); }
	size_t find_last_of(const basic_string &o, size_t pos = npos) const { return __rfind_of(o.data(), o.size(), pos, true); }
	size_t find_last_of(const C *s, size_t pos = npos) const { return __rfind_of(s, Tr::length(s), pos, true); }
	size_t find_last_of(C c, size_t pos = npos) const { return __rfind_of(&c, 1, pos, true); }
	size_t find_last_not_of(const basic_string &o, size_t pos = npos) const { return __rfind_of(o.data(), o.size(), pos, false); }
	size_t find_last_not_of(const C *s, size_t pos = npos) const { return __rfind_of(s, Tr::length(s), pos, false); }
	size_t find_last_not_of(C c, size_t pos = npos) const { return __rfind_of(&c, 1, pos, false); }
};
template <class C, class Tr, class A> basic_string<C, Tr, A> operator+(const basic_string<C, Tr, A> &a, const basic_string<C, Tr, A> &b) {
	basic_string<C, Tr, A> r(a);
	r.append(b);
	return r;
}
template <class C, class Tr, class A> basic_string<C, Tr, A> operator+(const basic_string<C, Tr, A> &a, const C *b) {
	basic_string<C, Tr, A> r(a);
	r.append(b);
	return r;
}
template <class C, class Tr, class A> basic_string<C, Tr, A> operator+(const C *a, const basic_string<C, Tr, A> &b) {
	basic_string<C, Tr, A> r(a);
	r.append(b);
	return r;
}
template <class C, class Tr, class A> basic_string<C, Tr, A> operator+(const basic_string<C, Tr, A> &a, C b) {
	basic_string<C, Tr, A> r(a);
	r.push_back(b);
	return r;
}
template <class C, class Tr, class A> basic_string<C, Tr, A> operator+(C a, const basic_string<C, Tr, A> &b) {
	basic_string<C, Tr, A> r(1, a);
	r.append(b);
	return r;
}
template <class C, class Tr, class A> bool operator==(const basic_string<C, Tr, A> &a, const basic_string<C, Tr, A> &b) { return a.compare(b) == 0; }
template <class C, class Tr, class A> bool operator==(const basic_string<C, Tr, A> &a, const C *b) { return a.compare(b) == 0; }
template <class C, class Tr, class A> bool operator==(const C *a, const basic_string<C, Tr, A> &b) { return b.compare(a) == 0; }
template <class C, class Tr, class A> bool operator!=(const basic_string<C, Tr, A> &a, const basic_string<C, Tr, A> &b) { return a.compare(b) != 0; }
template <class C, class Tr, class A> bool operator!=(const basic_string<C, Tr, A> &a, const C *b) { return a.compare(b) != 0; }
template <class C, class Tr, class A> bool operator!=(const C *a, const basic_string<C, Tr, A> &b) { return b.compare(a) != 0; }
template <class C, class Tr, class A> bool operator<(const basic_string<C, Tr, A> &a, const basic_string<C, Tr, A> &b) { return a.compare(b) < 0; }
template <class C, class Tr, class A> bool operator<(const basic_string<C, Tr, A> &a, const C *b) { return a.compare(b) < 0; }
template <class C, class Tr, class A> bool operator>(const basic_string<C, Tr, A> &a, const basic_string<C, Tr, A> &b) { return a.compare(b) > 0; }
template <class C, class Tr, class A> bool operator<=(const basic_string<C, Tr, A> &a, const basic_string<C, Tr, A> &b) { return a.compare(b) <= 0; }
template <class C, class Tr, class A> bool operator>=(const basic_string<C, Tr, A> &a, const basic_string<C, Tr, A> &b) { return a.compare(b) >= 0; }
template <class C, class Tr, class A> void swap(basic_string<C, Tr, A> &a, basic_string<C, Tr, A> &b) noexcept { a.swap(b); }
}
#include <ctype.h>
#include <algorithm>
namespace std {
typedef basic_string<char> string;
typedef basic_string<wchar_t> wstring;
typedef basic_string<char16_t> u16string;
typedef basic_string<char32_t> u32string;
inline string to_string(int v) {
	char b[32];
	snprintf(b, sizeof b, "%d", v);
	return string(b);
}
inline string to_string(unsigned v) {
	char b[32];
	snprintf(b, sizeof b, "%u", v);
	return string(b);
}
inline string to_string(long v) {
	char b[32];
	snprintf(b, sizeof b, "%ld", v);
	return string(b);
}
inline string to_string(unsigned long v) {
	char b[32];
	snprintf(b, sizeof b, "%lu", v);
	return string(b);
}
inline string to_string(long long v) {
	char b[32];
	snprintf(b, sizeof b, "%lld", v);
	return string(b);
}
inline string to_string(unsigned long long v) {
	char b[32];
	snprintf(b, sizeof b, "%llu", v);
	return string(b);
}
inline string to_string(float v) {
	char b[64];
	snprintf(b, sizeof b, "%f", v);
	return string(b);
}
inline string to_string(double v) {
	char b[400];
	snprintf(b, sizeof b, "%f", v);
	return string(b);
}
inline int stoi(const string &s, size_t *pos = nullptr, int base = 10) {
	char *e;
	long v = strtol(s.c_str(), &e, base);
	if (pos)
		*pos = e - s.c_str();
	return (int)v;
}
inline long stol(const string &s, size_t *pos = nullptr, int base = 10) {
	char *e;
	long v = strtol(s.c_str(), &e, base);
	if (pos)
		*pos = e - s.c_str();
	return v;
}
inline long long stoll(const string &s, size_t *pos = nullptr, int base = 10) {
	char *e;
	long long v = strtoll(s.c_str(), &e, base);
	if (pos)
		*pos = e - s.c_str();
	return v;
}
inline unsigned long stoul(const string &s, size_t *pos = nullptr, int base = 10) {
	char *e;
	unsigned long v = strtoul(s.c_str(), &e, base);
	if (pos)
		*pos = e - s.c_str();
	return v;
}
inline unsigned long long stoull(const string &s, size_t *pos = nullptr, int base = 10) {
	char *e;
	unsigned long long v = strtoull(s.c_str(), &e, base);
	if (pos)
		*pos = e - s.c_str();
	return v;
}
inline float stof(const string &s, size_t *pos = nullptr) {
	char *e;
	float v = strtof(s.c_str(), &e);
	if (pos)
		*pos = e - s.c_str();
	return v;
}
inline double stod(const string &s, size_t *pos = nullptr) {
	char *e;
	double v = strtod(s.c_str(), &e);
	if (pos)
		*pos = e - s.c_str();
	return v;
}
template <class T> struct hash;
template <> struct hash<string> {
	size_t operator()(const string &s) const noexcept {
		size_t h = 14695981039346656037ull;
		for (size_t i = 0, n = s.size(); i < n; i++)
			h = (h ^ (unsigned char)s[i]) * 1099511628211ull;
		return h;
	}
};
}
#endif
`
	sysHeaders["memory"] = `#ifndef __CC_MEMORY
#define __CC_MEMORY
#include <__go_slice>
#include <type_traits>
namespace std {
template <class T> T *addressof(T &v) noexcept { return reinterpret_cast<T *>(&const_cast<char &>(reinterpret_cast<const volatile char &>(v))); }
template <class T> struct default_delete {
	default_delete() = default;
	template <class U> default_delete(const default_delete<U> &) {}
	void operator()(T *p) const { delete p; }
};
template <class T> struct default_delete<T[]> {
	void operator()(T *p) const { delete[] p; }
};
template <class T, class D = default_delete<T>>
class unique_ptr {
	T *__p;

public:
	typedef T *pointer;
	typedef T element_type;
	typedef D deleter_type;
	constexpr unique_ptr() noexcept : __p(nullptr) {}
	constexpr unique_ptr(nullptr_t) noexcept : __p(nullptr) {}
	explicit unique_ptr(T *p) noexcept : __p(p) {}
	unique_ptr(unique_ptr &&o) noexcept : __p(o.__p) { o.__p = nullptr; }
	template <class U, class E, class = enable_if_t<is_convertible_v<U *, T *>>>
	unique_ptr(unique_ptr<U, E> &&o) noexcept : __p(o.release()) {}
	unique_ptr(const unique_ptr &) = delete;
	unique_ptr &operator=(const unique_ptr &) = delete;
	~unique_ptr() {
		if (__p)
			D()(__p);
	}
	unique_ptr &operator=(unique_ptr &&o) noexcept {
		reset(o.release());
		return *this;
	}
	template <class U, class E, class = enable_if_t<is_convertible_v<U *, T *>>>
	unique_ptr &operator=(unique_ptr<U, E> &&o) noexcept {
		reset(o.release());
		return *this;
	}
	unique_ptr &operator=(nullptr_t) noexcept {
		reset();
		return *this;
	}
	T *release() noexcept {
		T *p = __p;
		__p = nullptr;
		return p;
	}
	void reset(T *p = nullptr) noexcept {
		T *old = __p;
		__p = p;
		if (old)
			D()(old);
	}
	void swap(unique_ptr &o) noexcept {
		T *t = __p;
		__p = o.__p;
		o.__p = t;
	}
	T *get() const noexcept { return __p; }
	explicit operator bool() const noexcept { return __p != nullptr; }
	T &operator*() const { return *__p; }
	T *operator->() const noexcept { return __p; }
};
template <class T, class D>
class unique_ptr<T[], D> {
	T *__p;

public:
	typedef T *pointer;
	typedef T element_type;
	constexpr unique_ptr() noexcept : __p(nullptr) {}
	constexpr unique_ptr(nullptr_t) noexcept : __p(nullptr) {}
	explicit unique_ptr(T *p) noexcept : __p(p) {}
	unique_ptr(unique_ptr &&o) noexcept : __p(o.__p) { o.__p = nullptr; }
	unique_ptr(const unique_ptr &) = delete;
	unique_ptr &operator=(const unique_ptr &) = delete;
	~unique_ptr() {
		if (__p)
			delete[] __p;
	}
	unique_ptr &operator=(unique_ptr &&o) noexcept {
		reset(o.release());
		return *this;
	}
	T *release() noexcept {
		T *p = __p;
		__p = nullptr;
		return p;
	}
	void reset(T *p = nullptr) noexcept {
		T *old = __p;
		__p = p;
		if (old)
			delete[] old;
	}
	T *get() const noexcept { return __p; }
	explicit operator bool() const noexcept { return __p != nullptr; }
	T &operator[](size_t i) const { return __p[i]; }
};
template <class T, class D, class U, class E> bool operator==(const unique_ptr<T, D> &a, const unique_ptr<U, E> &b) { return a.get() == b.get(); }
template <class T, class D, class U, class E> bool operator!=(const unique_ptr<T, D> &a, const unique_ptr<U, E> &b) { return a.get() != b.get(); }
template <class T, class D> bool operator==(const unique_ptr<T, D> &a, nullptr_t) noexcept { return !a; }
template <class T, class D> bool operator==(nullptr_t, const unique_ptr<T, D> &a) noexcept { return !a; }
template <class T, class D> bool operator!=(const unique_ptr<T, D> &a, nullptr_t) noexcept { return bool(a); }
template <class T, class D> bool operator!=(nullptr_t, const unique_ptr<T, D> &a) noexcept { return bool(a); }
template <class T, class... Args, class = enable_if_t<!is_array_v<T>>>
unique_ptr<T> make_unique(Args &&...args) { return unique_ptr<T>(new T(static_cast<Args &&>(args)...)); }
template <class T, class = enable_if_t<is_array_v<T>>>
unique_ptr<T> make_unique(size_t n) { return unique_ptr<T>(new remove_extent_t<T>[n]()); }
}
#endif
`
	sysHeaders["optional"] = `#ifndef __CC_OPTIONAL
#define __CC_OPTIONAL
#include <new>
#include <utility>
namespace std {
struct nullopt_t {
	explicit constexpr nullopt_t(int) {}
};
inline constexpr nullopt_t nullopt{0};
template <class T>
class optional {
	union {
		T __v;
		char __d;
	};
	bool __has;
	T *__ptr() { return &__v; }
	const T *__ptr() const { return &__v; }

public:
	typedef T value_type;
	optional() noexcept : __d(0), __has(false) {}
	optional(nullopt_t) noexcept : __d(0), __has(false) {}
	optional(const T &v) : __has(true) { new ((void *)&__v) T(v); }
	optional(T &&v) : __has(true) { new ((void *)&__v) T(static_cast<T &&>(v)); }
	optional(const optional &o) : __d(0), __has(o.__has) {
		if (__has)
			new ((void *)&__v) T(*o.__ptr());
	}
	optional(optional &&o) : __d(0), __has(o.__has) {
		if (__has)
			new ((void *)&__v) T(static_cast<T &&>(*o.__ptr()));
	}
	~optional() { reset(); }
	optional &operator=(nullopt_t) noexcept {
		reset();
		return *this;
	}
	optional &operator=(const optional &o) {
		if (this != &o) {
			reset();
			if (o.__has) {
				new ((void *)&__v) T(*o.__ptr());
				__has = true;
			}
		}
		return *this;
	}
	optional &operator=(optional &&o) {
		if (this != &o) {
			reset();
			if (o.__has) {
				new ((void *)&__v) T(static_cast<T &&>(*o.__ptr()));
				__has = true;
			}
		}
		return *this;
	}
	optional &operator=(const T &v) {
		reset();
		new ((void *)&__v) T(v);
		__has = true;
		return *this;
	}
	optional &operator=(T &&v) {
		reset();
		new ((void *)&__v) T(static_cast<T &&>(v));
		__has = true;
		return *this;
	}
	template <class... Args> T &emplace(Args &&...args) {
		reset();
		new ((void *)&__v) T(static_cast<Args &&>(args)...);
		__has = true;
		return *__ptr();
	}
	void reset() noexcept {
		if (__has) {
			__ptr()->~T();
			__has = false;
		}
	}
	bool has_value() const noexcept { return __has; }
	explicit operator bool() const noexcept { return __has; }
	T &value() { return *__ptr(); }
	const T &value() const { return *__ptr(); }
	T &operator*() { return *__ptr(); }
	const T &operator*() const { return *__ptr(); }
	T *operator->() { return __ptr(); }
	const T *operator->() const { return __ptr(); }
	template <class U> T value_or(U &&d) const { return __has ? *__ptr() : static_cast<T>(static_cast<U &&>(d)); }
};
template <class T> bool operator==(const optional<T> &a, const optional<T> &b) { return a.has_value() == b.has_value() && (!a.has_value() || *a == *b); }
template <class T> bool operator!=(const optional<T> &a, const optional<T> &b) { return !(a == b); }
template <class T> bool operator==(const optional<T> &a, nullopt_t) noexcept { return !a.has_value(); }
template <class T> bool operator!=(const optional<T> &a, nullopt_t) noexcept { return a.has_value(); }
template <class T> bool operator==(const optional<T> &a, const T &b) { return a.has_value() && *a == b; }
template <class T> bool operator!=(const optional<T> &a, const T &b) { return !(a == b); }
template <class T> optional<decay_t<T>> make_optional(T &&v) { return optional<decay_t<T>>(static_cast<T &&>(v)); }
}
#endif
`
	sysHeaders["numeric"] = `#ifndef __CC_NUMERIC
#define __CC_NUMERIC
namespace std {
template <class I, class T> T accumulate(I first, I last, T init) {
	for (; first != last; ++first)
		init = init + *first;
	return init;
}
template <class I, class T, class F> T accumulate(I first, I last, T init, F f) {
	for (; first != last; ++first)
		init = f(init, *first);
	return init;
}
template <class I, class T> void iota(I first, I last, T v) {
	for (; first != last; ++first, ++v)
		*first = v;
}
}
#endif
`
	sysHeaders["exception"] = `#ifndef __CC_EXCEPTION
#define __CC_EXCEPTION
#include <stdlib.h>
namespace std {
class exception {
public:
	exception() noexcept {}
	virtual ~exception() {}
	virtual const char *what() const noexcept { return "std::exception"; }
};
class bad_alloc : public exception {
public:
	const char *what() const noexcept override { return "std::bad_alloc"; }
};
[[noreturn]] inline void terminate() noexcept { abort(); }
}
#endif
`
	sysHeaders["stdexcept"] = `#ifndef __CC_STDEXCEPT
#define __CC_STDEXCEPT
#include <exception>
#include <string>
namespace std {
class logic_error : public exception {
	string __m;

public:
	explicit logic_error(const string &m) : __m(m) {}
	explicit logic_error(const char *m) : __m(m) {}
	const char *what() const noexcept override { return __m.c_str(); }
};
class runtime_error : public exception {
	string __m;

public:
	explicit runtime_error(const string &m) : __m(m) {}
	explicit runtime_error(const char *m) : __m(m) {}
	const char *what() const noexcept override { return __m.c_str(); }
};
class invalid_argument : public logic_error { public: using logic_error::logic_error; };
class domain_error : public logic_error { public: using logic_error::logic_error; };
class length_error : public logic_error { public: using logic_error::logic_error; };
class out_of_range : public logic_error { public: using logic_error::logic_error; };
class range_error : public runtime_error { public: using runtime_error::runtime_error; };
class overflow_error : public runtime_error { public: using runtime_error::runtime_error; };
class underflow_error : public runtime_error { public: using runtime_error::runtime_error; };
}
#endif
`
	sysHeaders["deque"] = `#ifndef __CC_DEQUE
#define __CC_DEQUE
#include <vector>
namespace std {
// The elements are in blocks, which stay where they are: an element's
// address is its own until it is erased. The blocks are a vector's.
template <class T, class A = allocator<T>>
class deque {
	static const size_t __B = 16;
	vector<T *> __blocks;
	size_t __start, __n;
	T *__at(size_t i) const {
		i += __start;
		return __blocks[i / __B] + i % __B;
	}

public:
	typedef T value_type;
	typedef size_t size_type;
	typedef ptrdiff_t difference_type;
	typedef T &reference;
	typedef const T &const_reference;
	template <class D, class R>
	class __iter {
	public:
		D *__d;
		size_t __i;
		typedef random_access_iterator_tag iterator_category;
		typedef T value_type;
		typedef ptrdiff_t difference_type;
		typedef R *pointer;
		typedef R &reference;
		__iter() : __d(nullptr), __i(0) {}
		__iter(D *d, size_t i) : __d(d), __i(i) {}
		template <class D2, class R2> __iter(const __iter<D2, R2> &o) : __d(o.__d), __i(o.__i) {}
		R &operator*() const { return *__d->__at(__i); }
		R *operator->() const { return __d->__at(__i); }
		R &operator[](ptrdiff_t n) const { return *__d->__at(__i + n); }
		__iter &operator++() {
			__i++;
			return *this;
		}
		__iter operator++(int) {
			__iter t = *this;
			__i++;
			return t;
		}
		__iter &operator--() {
			__i--;
			return *this;
		}
		__iter operator--(int) {
			__iter t = *this;
			__i--;
			return t;
		}
		__iter &operator+=(ptrdiff_t n) {
			__i += n;
			return *this;
		}
		__iter &operator-=(ptrdiff_t n) {
			__i -= n;
			return *this;
		}
		__iter operator+(ptrdiff_t n) const { return __iter(__d, __i + n); }
		__iter operator-(ptrdiff_t n) const { return __iter(__d, __i - n); }
		ptrdiff_t operator-(const __iter &o) const { return (ptrdiff_t)__i - (ptrdiff_t)o.__i; }
		bool operator==(const __iter &o) const { return __i == o.__i; }
		bool operator!=(const __iter &o) const { return __i != o.__i; }
		bool operator<(const __iter &o) const { return __i < o.__i; }
		bool operator>(const __iter &o) const { return __i > o.__i; }
		bool operator<=(const __iter &o) const { return __i <= o.__i; }
		bool operator>=(const __iter &o) const { return __i >= o.__i; }
	};
	typedef __iter<deque, T> iterator;
	typedef __iter<const deque, const T> const_iterator;
	typedef std::reverse_iterator<iterator> reverse_iterator;
	typedef std::reverse_iterator<const_iterator> const_reverse_iterator;

	deque() : __start(0), __n(0) {}
	explicit deque(size_t n) : __start(0), __n(0) { resize(n); }
	deque(size_t n, const T &v) : __start(0), __n(0) {
		for (size_t i = 0; i < n; i++)
			push_back(v);
	}
	deque(const deque &o) : __start(0), __n(0) {
		for (size_t i = 0; i < o.__n; i++)
			push_back(o[i]);
	}
	deque(deque &&o) noexcept : __blocks(static_cast<vector<T *> &&>(o.__blocks)), __start(o.__start), __n(o.__n) {
		o.__start = 0;
		o.__n = 0;
	}
	deque(initializer_list<T> l) : __start(0), __n(0) {
		for (const T *p = l.begin(); p != l.end(); ++p)
			push_back(*p);
	}
	~deque() {
		clear();
		for (size_t i = 0; i < __blocks.size(); i++)
			::operator delete(__blocks[i]);
	}
	deque &operator=(const deque &o) {
		if (this != &o) {
			clear();
			for (size_t i = 0; i < o.__n; i++)
				push_back(o[i]);
		}
		return *this;
	}
	deque &operator=(deque &&o) noexcept {
		if (this != &o) {
			clear();
			__blocks.swap(o.__blocks);
			size_t s = __start;
			__start = o.__start;
			o.__start = s;
			__n = o.__n;
			o.__n = 0;
		}
		return *this;
	}
	size_t size() const noexcept { return __n; }
	bool empty() const noexcept { return __n == 0; }
	T &operator[](size_t i) { return *__at(i); }
	const T &operator[](size_t i) const { return *__at(i); }
	T &at(size_t i) { return *__at(i); }
	const T &at(size_t i) const { return *__at(i); }
	T &front() { return *__at(0); }
	const T &front() const { return *__at(0); }
	T &back() { return *__at(__n - 1); }
	const T &back() const { return *__at(__n - 1); }
	iterator begin() noexcept { return iterator(this, 0); }
	const_iterator begin() const noexcept { return const_iterator(this, 0); }
	iterator end() noexcept { return iterator(this, __n); }
	const_iterator end() const noexcept { return const_iterator(this, __n); }
	const_iterator cbegin() const noexcept { return const_iterator(this, 0); }
	const_iterator cend() const noexcept { return const_iterator(this, __n); }
	reverse_iterator rbegin() noexcept { return reverse_iterator(end()); }
	reverse_iterator rend() noexcept { return reverse_iterator(begin()); }
	void __grow_back() {
		if (__start + __n == __blocks.size() * __B)
			__blocks.push_back((T *)::operator new(__B * sizeof(T)));
	}
	void __grow_front() {
		if (__start == 0) {
			__blocks.insert(__blocks.begin(), (T *)::operator new(__B * sizeof(T)));
			__start = __B;
		}
	}
	void push_back(const T &v) {
		__grow_back();
		new ((void *)__at(__n)) T(v);
		__n++;
	}
	void push_back(T &&v) {
		__grow_back();
		new ((void *)__at(__n)) T(static_cast<T &&>(v));
		__n++;
	}
	template <class... Args> T &emplace_back(Args &&...args) {
		__grow_back();
		T *p = new ((void *)__at(__n)) T(static_cast<Args &&>(args)...);
		__n++;
		return *p;
	}
	void push_front(const T &v) {
		__grow_front();
		__start--;
		new ((void *)__at(0)) T(v);
		__n++;
	}
	void push_front(T &&v) {
		__grow_front();
		__start--;
		new ((void *)__at(0)) T(static_cast<T &&>(v));
		__n++;
	}
	template <class... Args> T &emplace_front(Args &&...args) {
		__grow_front();
		__start--;
		T *p = new ((void *)__at(0)) T(static_cast<Args &&>(args)...);
		__n++;
		return *p;
	}
	void pop_back() {
		__at(__n - 1)->~T();
		__n--;
	}
	void pop_front() {
		__at(0)->~T();
		__start++;
		__n--;
		if (__n == 0)
			__start = 0;
	}
	void resize(size_t n) {
		while (__n > n)
			pop_back();
		while (__n < n)
			emplace_back();
	}
	void clear() noexcept {
		while (__n > 0)
			pop_back();
		__start = 0;
	}
	iterator erase(const_iterator pos) {
		size_t i = pos.__i;
		for (size_t j = i; j + 1 < __n; j++)
			*__at(j) = static_cast<T &&>(*__at(j + 1));
		pop_back();
		return iterator(this, i);
	}
	iterator insert(const_iterator pos, const T &v) {
		size_t i = pos.__i;
		T c(v);
		emplace_back();
		for (size_t j = __n - 1; j > i; j--)
			*__at(j) = static_cast<T &&>(*__at(j - 1));
		*__at(i) = static_cast<T &&>(c);
		return iterator(this, i);
	}
	void swap(deque &o) noexcept {
		__blocks.swap(o.__blocks);
		size_t s = __start, n = __n;
		__start = o.__start;
		__n = o.__n;
		o.__start = s;
		o.__n = n;
	}
};
}
#endif
`
	sysHeaders["queue"] = `#ifndef __CC_QUEUE
#define __CC_QUEUE
#include <deque>
#include <vector>
#include <functional>
namespace std {
template <class T, class C = deque<T>>
class queue {
protected:
	C c;

public:
	typedef T value_type;
	typedef C container_type;
	queue() {}
	bool empty() const { return c.empty(); }
	size_t size() const { return c.size(); }
	T &front() { return c.front(); }
	const T &front() const { return c.front(); }
	T &back() { return c.back(); }
	const T &back() const { return c.back(); }
	void push(const T &v) { c.push_back(v); }
	void push(T &&v) { c.push_back(static_cast<T &&>(v)); }
	template <class... Args> void emplace(Args &&...args) { c.emplace_back(static_cast<Args &&>(args)...); }
	void pop() { c.pop_front(); }
	void swap(queue &o) { c.swap(o.c); }
};
template <class T, class C = vector<T>, class Cmp = less<T>>
class priority_queue {
protected:
	C c;
	Cmp comp;
	void __up(size_t i) {
		while (i > 0) {
			size_t p = (i - 1) / 2;
			if (!comp(c[p], c[i]))
				break;
			T t = static_cast<T &&>(c[p]);
			c[p] = static_cast<T &&>(c[i]);
			c[i] = static_cast<T &&>(t);
			i = p;
		}
	}
	void __down(size_t i) {
		size_t n = c.size();
		for (;;) {
			size_t l = 2 * i + 1, m = i;
			if (l < n && comp(c[m], c[l]))
				m = l;
			if (l + 1 < n && comp(c[m], c[l + 1]))
				m = l + 1;
			if (m == i)
				break;
			T t = static_cast<T &&>(c[m]);
			c[m] = static_cast<T &&>(c[i]);
			c[i] = static_cast<T &&>(t);
			i = m;
		}
	}

public:
	typedef T value_type;
	typedef C container_type;
	priority_queue() {}
	explicit priority_queue(const Cmp &cmp) : comp(cmp) {}
	bool empty() const { return c.empty(); }
	size_t size() const { return c.size(); }
	const T &top() const { return c.front(); }
	void push(const T &v) {
		c.push_back(v);
		__up(c.size() - 1);
	}
	void push(T &&v) {
		c.push_back(static_cast<T &&>(v));
		__up(c.size() - 1);
	}
	template <class... Args> void emplace(Args &&...args) {
		c.emplace_back(static_cast<Args &&>(args)...);
		__up(c.size() - 1);
	}
	void pop() {
		size_t n = c.size();
		if (n > 1) {
			T t = static_cast<T &&>(c[0]);
			c[0] = static_cast<T &&>(c[n - 1]);
			c[n - 1] = static_cast<T &&>(t);
		}
		c.pop_back();
		if (n > 2)
			__down(0);
	}
	void swap(priority_queue &o) { c.swap(o.c); }
};
}
#endif
`
	// A stream writes to a FILE, or to a string: what a library's
	// operator<< for its own types needs.
	sysHeaders["ostream"] = `#ifndef __CC_OSTREAM
#define __CC_OSTREAM
#include <stdio.h>
#include <string>
namespace std {
class ios_base {
public:
	typedef int fmtflags;
	static const fmtflags dec = 1, hex = 2, oct = 4, fixed = 8, scientific = 16, boolalpha = 32, showpoint = 64, floatfield = 24, basefield = 7;
	typedef int openmode;
	static const openmode in = 1, out = 2, app = 4, binary = 8, trunc = 16, ate = 32;

protected:
	fmtflags __flags;
	int __prec, __width;
	char __fill;
	ios_base() : __flags(dec), __prec(6), __width(0), __fill(' ') {}

public:
	fmtflags flags() const { return __flags; }
	fmtflags flags(fmtflags f) {
		fmtflags o = __flags;
		__flags = f;
		return o;
	}
	fmtflags setf(fmtflags f) {
		fmtflags o = __flags;
		__flags |= f;
		return o;
	}
	fmtflags setf(fmtflags f, fmtflags mask) {
		fmtflags o = __flags;
		__flags = (__flags & ~mask) | (f & mask);
		return o;
	}
	void unsetf(fmtflags f) { __flags &= ~f; }
	int precision() const { return __prec; }
	int precision(int p) {
		int o = __prec;
		__prec = p;
		return o;
	}
	int width() const { return __width; }
	int width(int w) {
		int o = __width;
		__width = w;
		return o;
	}
};
template <class C, class Tr = char_traits<C>>
class basic_ostream : public ios_base {
protected:
	FILE *__f;
	basic_string<C> *__str;
	void __put(const char *s, size_t n) {
		for (int i = (int)n; i < __width; i++) {
			if (__f)
				fputc(__fill, __f);
			else if (__str)
				__str->push_back(__fill);
		}
		__width = 0;
		if (__f)
			fwrite(s, 1, n, __f);
		else if (__str)
			__str->append(s, n);
	}
	void __num(const char *len, char conv, long long v) {
		char fmt[8], b[40];
		if (conv != 'c' && (__flags & hex))
			conv = 'x';
		else if (conv != 'c' && (__flags & oct))
			conv = 'o';
		snprintf(fmt, sizeof fmt, "%%%s%c", len, conv);
		int n = snprintf(b, sizeof b, fmt, v);
		__put(b, n);
	}
	void __flt(double v) {
		char fmt[16], b[400];
		char conv = (__flags & fixed) ? 'f' : (__flags & scientific) ? 'e' : 'g';
		snprintf(fmt, sizeof fmt, "%%.%d%c", __prec, conv);
		int n = snprintf(b, sizeof b, fmt, v);
		__put(b, n);
	}

public:
	typedef C char_type;
	basic_ostream() : __f(nullptr), __str(nullptr) {}
	explicit basic_ostream(FILE *f) : __f(f), __str(nullptr) {}
	virtual ~basic_ostream() {}
	char fill() const { return __fill; }
	char fill(char c) {
		char o = __fill;
		__fill = c;
		return o;
	}
	basic_ostream &put(char c) {
		__put(&c, 1);
		return *this;
	}
	basic_ostream &write(const char *s, long n) {
		__put(s, n);
		return *this;
	}
	basic_ostream &flush() {
		if (__f)
			fflush(__f);
		return *this;
	}
	bool good() const { return true; }
	bool fail() const { return false; }
	explicit operator bool() const { return true; }
	basic_ostream &operator<<(basic_ostream &(*f)(basic_ostream &)) { return f(*this); }
	basic_ostream &operator<<(ios_base &(*f)(ios_base &)) {
		f(*this);
		return *this;
	}
	basic_ostream &operator<<(bool v) {
		if (__flags & boolalpha)
			__put(v ? "true" : "false", v ? 4 : 5);
		else
			__put(v ? "1" : "0", 1);
		return *this;
	}
	basic_ostream &operator<<(short v) {
		__num("ll", 'd', v);
		return *this;
	}
	basic_ostream &operator<<(unsigned short v) {
		__num("ll", 'u', v);
		return *this;
	}
	basic_ostream &operator<<(int v) {
		__num("ll", 'd', v);
		return *this;
	}
	basic_ostream &operator<<(unsigned v) {
		__num("ll", 'u', v);
		return *this;
	}
	basic_ostream &operator<<(long v) {
		__num("ll", 'd', v);
		return *this;
	}
	basic_ostream &operator<<(unsigned long v) {
		__num("ll", 'u', (long long)v);
		return *this;
	}
	basic_ostream &operator<<(long long v) {
		__num("ll", 'd', v);
		return *this;
	}
	basic_ostream &operator<<(unsigned long long v) {
		__num("ll", 'u', (long long)v);
		return *this;
	}
	basic_ostream &operator<<(float v) {
		__flt(v);
		return *this;
	}
	basic_ostream &operator<<(double v) {
		__flt(v);
		return *this;
	}
	basic_ostream &operator<<(long double v) {
		__flt((double)v);
		return *this;
	}
	basic_ostream &operator<<(const void *p) {
		char b[40];
		int n = snprintf(b, sizeof b, "%p", p);
		__put(b, n);
		return *this;
	}
};
typedef basic_ostream<char> ostream;
template <class Tr> basic_ostream<char, Tr> &operator<<(basic_ostream<char, Tr> &os, char c) { return os.put(c); }
template <class Tr> basic_ostream<char, Tr> &operator<<(basic_ostream<char, Tr> &os, signed char c) { return os.put((char)c); }
template <class Tr> basic_ostream<char, Tr> &operator<<(basic_ostream<char, Tr> &os, unsigned char c) { return os.put((char)c); }
template <class Tr> basic_ostream<char, Tr> &operator<<(basic_ostream<char, Tr> &os, const char *s) { return os.write(s, (long)strlen(s)); }
template <class Tr, class A> basic_ostream<char, Tr> &operator<<(basic_ostream<char, Tr> &os, const basic_string<char, Tr, A> &s) { return os.write(s.data(), (long)s.size()); }
// (Functions, not templates: a stream of char is the only one.)
inline ostream &endl(ostream &os) {
	os.put('\n');
	return os.flush();
}
inline ostream &flush(ostream &os) { return os.flush(); }
inline ostream &ends(ostream &os) { return os.put('\0'); }
inline ios_base &fixed(ios_base &b) {
	b.setf(ios_base::fixed, ios_base::floatfield);
	return b;
}
inline ios_base &scientific(ios_base &b) {
	b.setf(ios_base::scientific, ios_base::floatfield);
	return b;
}
inline ios_base &hex(ios_base &b) {
	b.setf(ios_base::hex, ios_base::basefield);
	return b;
}
inline ios_base &dec(ios_base &b) {
	b.setf(ios_base::dec, ios_base::basefield);
	return b;
}
inline ios_base &oct(ios_base &b) {
	b.setf(ios_base::oct, ios_base::basefield);
	return b;
}
inline ios_base &boolalpha(ios_base &b) {
	b.setf(ios_base::boolalpha);
	return b;
}
}
#endif
`
	sysHeaders["iostream"] = `#ifndef __CC_IOSTREAM
#define __CC_IOSTREAM
#include <ostream>
namespace std {
class __file_ostream : public ostream {
public:
	explicit __file_ostream(int fd) { __f = fd == 1 ? stdout : stderr; }
};
inline __file_ostream cout(1);
inline __file_ostream cerr(2);
inline __file_ostream clog(2);
}
#endif
`
	sysHeaders["sstream"] = `#ifndef __CC_SSTREAM
#define __CC_SSTREAM
#include <ostream>
namespace std {
class ostringstream : public ostream {
	string __buf;

public:
	ostringstream() { __str = &__buf; }
	explicit ostringstream(const string &s) : __buf(s) { __str = &__buf; }
	string str() const { return __buf; }
	void str(const string &s) { __buf = s; }
};
typedef ostringstream stringstream;
}
#endif
`
	sysHeaders["iomanip"] = `#ifndef __CC_IOMANIP
#define __CC_IOMANIP
#include <ostream>
namespace std {
struct __setprecision { int n; };
struct __setw { int n; };
struct __setfill { char c; };
inline __setprecision setprecision(int n) { return __setprecision{n}; }
inline __setw setw(int n) { return __setw{n}; }
inline __setfill setfill(char c) { return __setfill{c}; }
template <class C, class Tr> basic_ostream<C, Tr> &operator<<(basic_ostream<C, Tr> &os, __setprecision p) {
	os.precision(p.n);
	return os;
}
template <class C, class Tr> basic_ostream<C, Tr> &operator<<(basic_ostream<C, Tr> &os, __setw p) {
	os.width(p.n);
	return os;
}
template <class C, class Tr> basic_ostream<C, Tr> &operator<<(basic_ostream<C, Tr> &os, __setfill p) {
	os.fill(p.c);
	return os;
}
}
#endif
`
	sysHeaders["iosfwd"] = "#ifndef __CC_IOSFWD\n#define __CC_IOSFWD\n#include <ostream>\n#endif\n"
}
