// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// The containers of C++'s library whose elements are nodes: each stays
// where it was made. An unordered one finds them with a map of Go's
// (std::__go_map<K, V> is map[K]V), from a key's hash; an ordered one
// with a vector of them in order.
func init() {
	sysHeaders["__hash"] = `#ifndef __CC_HASH
#define __CC_HASH
#include <stddef.h>
namespace std {
template <class T> struct hash {
	size_t operator()(const T &v) const noexcept { return (size_t)v; }
};
template <> struct hash<float> {
	size_t operator()(float v) const noexcept {
		union { float f; unsigned u; } x;
		x.u = 0;
		x.f = v == 0 ? 0 : v;
		return x.u;
	}
};
template <> struct hash<double> {
	size_t operator()(double v) const noexcept {
		union { double f; size_t u; } x;
		x.u = 0;
		x.f = v == 0 ? 0 : v;
		return x.u;
	}
};
}
#endif
`
	sysHeaders["__go_map"] = `#ifndef __CC_GO_MAP
#define __CC_GO_MAP
#include <stddef.h>
#include <new>
#include <utility>
#include <iterator>
#include <initializer_list>
#include <__hash>
#include <__go_slice>
namespace std {
template <class K, class V> struct __go_map {
	void *__p;
};
template <class K, class V> V __go_MapGet(const __go_map<K, V> *m, K k);
template <class K, class V> void __go_MapSet(__go_map<K, V> *m, K k, V v);
template <class K, class V> void __go_MapDelete(__go_map<K, V> *m, K k);
template <class K, class V> size_t __go_MapLen(const __go_map<K, V> *m);
template <class K, class V> void __go_MapReset(__go_map<K, V> *m);
template <class T = void> struct __cc_equal {
	bool operator()(const T &a, const T &b) const { return a == b; }
};
struct __key_self {
	template <class V> const V &operator()(const V &v) const { return v; }
};
struct __key_first {
	template <class V> auto operator()(const V &v) const -> decltype((v.first)) { return v.first; }
};
// The elements of a table are nodes in a list, in the order they were
// put in; those of one hash are a chain, which Go's map has the first
// of.
template <class K, class V, class GetKey, class H, class E>
class __hashtab {
public:
	struct __node {
		__node *__chain, *__prev, *__next;
		size_t __h;
		V __v;
	};

protected:
	__go_map<size_t, __node *> __idx;
	__node *__head, *__tail;
	size_t __n;
	void __link(__node *n) {
		n->__chain = __go_MapGet(&__idx, n->__h);
		__go_MapSet(&__idx, n->__h, n);
		n->__next = nullptr;
		n->__prev = __tail;
		if (__tail)
			__tail->__next = n;
		else
			__head = n;
		__tail = n;
		__n++;
	}
	void __unlink(__node *n) {
		__node *c = __go_MapGet(&__idx, n->__h);
		if (c == n) {
			if (n->__chain)
				__go_MapSet(&__idx, n->__h, n->__chain);
			else
				__go_MapDelete(&__idx, n->__h);
		} else {
			while (c->__chain != n)
				c = c->__chain;
			c->__chain = n->__chain;
		}
		if (n->__prev)
			n->__prev->__next = n->__next;
		else
			__head = n->__next;
		if (n->__next)
			n->__next->__prev = n->__prev;
		else
			__tail = n->__prev;
		__n--;
	}
	static __node *__alloc() { return (__node *)::operator new(sizeof(__node)); }
	void __drop(__node *n) {
		n->__v.~V();
		::operator delete(n);
	}

public:
	template <class R>
	class __iter {
	public:
		__node *__p;
		typedef forward_iterator_tag iterator_category;
		typedef V value_type;
		typedef ptrdiff_t difference_type;
		typedef R *pointer;
		typedef R &reference;
		__iter() : __p(nullptr) {}
		__iter(__node *p) : __p(p) {}
		template <class R2> __iter(const __iter<R2> &o) : __p(o.__p) {}
		R &operator*() const { return __p->__v; }
		R *operator->() const { return &__p->__v; }
		__iter &operator++() {
			__p = __p->__next;
			return *this;
		}
		__iter operator++(int) {
			__iter t = *this;
			__p = __p->__next;
			return t;
		}
		bool operator==(const __iter &o) const { return __p == o.__p; }
		bool operator!=(const __iter &o) const { return __p != o.__p; }
	};
	typedef size_t size_type;
	typedef ptrdiff_t difference_type;
	typedef V value_type;
	typedef K key_type;
	typedef H hasher;
	typedef E key_equal;
	typedef V &reference;
	typedef const V &const_reference;

	__hashtab() : __head(nullptr), __tail(nullptr), __n(0) { __go_MapReset(&__idx); }
	~__hashtab() {
		clear();
		__go_MapReset(&__idx);
	}
	size_t size() const noexcept { return __n; }
	bool empty() const noexcept { return __n == 0; }
	size_t max_size() const noexcept { return (size_t)-1 / sizeof(__node); }
	size_t bucket_count() const noexcept { return __go_MapLen(&__idx) + 1; }
	float load_factor() const noexcept { return 1; }
	float max_load_factor() const noexcept { return 1; }
	void max_load_factor(float) {}
	void reserve(size_t) {}
	void rehash(size_t) {}
	void clear() noexcept {
		for (__node *n = __head; n;) {
			__node *next = n->__next;
			__drop(n);
			n = next;
		}
		__head = __tail = nullptr;
		__n = 0;
		__go_MapReset(&__idx);
	}
	__node *__find(const K &k) const {
		size_t h = H()(k);
		for (__node *n = __go_MapGet(&__idx, h); n; n = n->__chain)
			if (E()(GetKey()(n->__v), k))
				return n;
		return nullptr;
	}
	size_t count(const K &k) const { return __find(k) ? 1 : 0; }
	bool contains(const K &k) const { return __find(k) != nullptr; }
	void __erase(__node *n) {
		__unlink(n);
		__drop(n);
	}
	size_t erase(const K &k) {
		__node *n = __find(k);
		if (!n)
			return 0;
		__erase(n);
		return 1;
	}
	void __copy(const __hashtab &o) {
		for (__node *n = o.__head; n; n = n->__next) {
			__node *c = __alloc();
			new ((void *)&c->__v) V(n->__v);
			c->__h = n->__h;
			__link(c);
		}
	}
	void __take(__hashtab &o) {
		for (__node *n = o.__head; n;) {
			__node *next = n->__next;
			__link(n);
			n = next;
		}
		o.__head = o.__tail = nullptr;
		o.__n = 0;
		__go_MapReset(&o.__idx);
	}
	void swap(__hashtab &o) {
		__hashtab t;
		t.__take(o);
		o.__take(*this);
		__head = __tail = nullptr;
		__n = 0;
		__go_MapReset(&__idx);
		__take(t);
	}
};
// The elements of an ordered table are nodes in a list, in order, and
// a vector has them in that order too: it is searched.
template <class K, class V, class GetKey, class L>
class __ordtab {
public:
	struct __node {
		__node *__prev, *__next;
		V __v;
	};

protected:
	__seq<__node *> __ord;
	__node *__head, *__tail;
	__go_slice<__node *> *__sl() const { return const_cast<__go_slice<__node *> *>(&__ord.__s); }
	// The index of the first element whose key is not less than k.
	size_t __lower(const K &k) const {
		size_t lo = 0, hi = __go_SliceLen(__sl());
		__node **d = __go_SliceData(__sl());
		while (lo < hi) {
			size_t m = lo + (hi - lo) / 2;
			if (L()(GetKey()(d[m]->__v), k))
				lo = m + 1;
			else
				hi = m;
		}
		return lo;
	}
	size_t __upper(const K &k) const {
		size_t lo = 0, hi = __go_SliceLen(__sl());
		__node **d = __go_SliceData(__sl());
		while (lo < hi) {
			size_t m = lo + (hi - lo) / 2;
			if (!L()(k, GetKey()(d[m]->__v)))
				lo = m + 1;
			else
				hi = m;
		}
		return lo;
	}
	__node *__at(size_t i) const { return i < __go_SliceLen(__sl()) ? __go_SliceData(__sl())[i] : nullptr; }
	void __put(size_t i, __node *n) {
		__node *after = __at(i);
		*__ord.__gap(i, 1) = n;
		n->__next = after;
		n->__prev = after ? after->__prev : __tail;
		if (n->__prev)
			n->__prev->__next = n;
		else
			__head = n;
		if (after)
			after->__prev = n;
		else
			__tail = n;
	}
	static __node *__alloc() { return (__node *)::operator new(sizeof(__node)); }
	void __drop(__node *n) {
		n->__v.~V();
		::operator delete(n);
	}

public:
	template <class R>
	class __iter {
	public:
		__node *__p;
		const __ordtab *__t;
		typedef bidirectional_iterator_tag iterator_category;
		typedef V value_type;
		typedef ptrdiff_t difference_type;
		typedef R *pointer;
		typedef R &reference;
		__iter() : __p(nullptr), __t(nullptr) {}
		__iter(__node *p, const __ordtab *t) : __p(p), __t(t) {}
		template <class R2> __iter(const __iter<R2> &o) : __p(o.__p), __t(o.__t) {}
		R &operator*() const { return __p->__v; }
		R *operator->() const { return &__p->__v; }
		__iter &operator++() {
			__p = __p->__next;
			return *this;
		}
		__iter operator++(int) {
			__iter t = *this;
			__p = __p->__next;
			return t;
		}
		__iter &operator--() {
			__p = __p ? __p->__prev : __t->__tail;
			return *this;
		}
		__iter operator--(int) {
			__iter t = *this;
			__p = __p ? __p->__prev : __t->__tail;
			return t;
		}
		bool operator==(const __iter &o) const { return __p == o.__p; }
		bool operator!=(const __iter &o) const { return __p != o.__p; }
	};
	typedef size_t size_type;
	typedef ptrdiff_t difference_type;
	typedef V value_type;
	typedef K key_type;
	typedef L key_compare;
	typedef V &reference;
	typedef const V &const_reference;

	__ordtab() : __head(nullptr), __tail(nullptr) {}
	~__ordtab() { clear(); }
	size_t size() const noexcept { return __go_SliceLen(__sl()); }
	bool empty() const noexcept { return __go_SliceLen(__sl()) == 0; }
	size_t max_size() const noexcept { return (size_t)-1 / sizeof(__node); }
	void clear() noexcept {
		for (__node *n = __head; n;) {
			__node *next = n->__next;
			__drop(n);
			n = next;
		}
		__head = __tail = nullptr;
		__ord.__cut(0);
	}
	__node *__find(const K &k) const {
		__node *n = __at(__lower(k));
		return n && !L()(k, GetKey()(n->__v)) ? n : nullptr;
	}
	size_t count(const K &k) const { return __upper(k) - __lower(k); }
	bool contains(const K &k) const { return __find(k) != nullptr; }
	void __erase(__node *n) {
		size_t i = __lower(GetKey()(n->__v));
		while (__at(i) != n)
			i++;
		__ord.__close(i, 1);
		if (n->__prev)
			n->__prev->__next = n->__next;
		else
			__head = n->__next;
		if (n->__next)
			n->__next->__prev = n->__prev;
		else
			__tail = n->__prev;
		__drop(n);
	}
	size_t erase(const K &k) {
		size_t c = 0;
		for (__node *n; (n = __find(k)) != nullptr; c++)
			__erase(n);
		return c;
	}
	void __copy(const __ordtab &o) {
		for (__node *n = o.__head; n; n = n->__next) {
			__node *c = __alloc();
			new ((void *)&c->__v) V(n->__v);
			__put(size(), c);
		}
	}
	void swap(__ordtab &o) {
		__go_SliceSwap(&__ord.__s, &o.__ord.__s);
		__node *h = __head, *t = __tail;
		__head = o.__head;
		__tail = o.__tail;
		o.__head = h;
		o.__tail = t;
	}
};
}
#endif
`
	sysHeaders["unordered_map"] = `#ifndef __CC_UNORDERED_MAP
#define __CC_UNORDERED_MAP
#include <__go_map>
namespace std {
template <class K, class T, class H = hash<K>, class E = __cc_equal<K>, class A = allocator<pair<const K, T>>>
class unordered_map : public __hashtab<K, pair<const K, T>, __key_first, H, E> {
	typedef __hashtab<K, pair<const K, T>, __key_first, H, E> __base;
	typedef typename __base::__node __node;
	__node *__make(const K &k) {
		__node *n = this->__alloc();
		new ((void *)&n->__v.first) K(k);
		new ((void *)&n->__v.second) T();
		n->__h = H()(k);
		this->__link(n);
		return n;
	}

public:
	typedef T mapped_type;
	typedef pair<const K, T> value_type;
	typedef typename __base::template __iter<value_type> iterator;
	typedef typename __base::template __iter<const value_type> const_iterator;
	unordered_map() {}
	explicit unordered_map(size_t) {}
	unordered_map(const unordered_map &o) { this->__copy(o); }
	unordered_map(unordered_map &&o) noexcept { this->__take(o); }
	unordered_map(initializer_list<value_type> l) {
		for (const value_type *p = l.begin(); p != l.end(); ++p)
			insert(*p);
	}
	unordered_map &operator=(const unordered_map &o) {
		if (this != &o) {
			this->clear();
			this->__copy(o);
		}
		return *this;
	}
	unordered_map &operator=(unordered_map &&o) noexcept {
		if (this != &o) {
			this->clear();
			this->__take(o);
		}
		return *this;
	}
	iterator begin() noexcept { return iterator(this->__head); }
	const_iterator begin() const noexcept { return const_iterator(this->__head); }
	iterator end() noexcept { return iterator(nullptr); }
	const_iterator end() const noexcept { return const_iterator(nullptr); }
	const_iterator cbegin() const noexcept { return const_iterator(this->__head); }
	const_iterator cend() const noexcept { return const_iterator(nullptr); }
	iterator find(const K &k) { return iterator(this->__find(k)); }
	const_iterator find(const K &k) const { return const_iterator(this->__find(k)); }
	T &operator[](const K &k) {
		__node *n = this->__find(k);
		if (!n)
			n = __make(k);
		return n->__v.second;
	}
	T &at(const K &k) { return this->__find(k)->__v.second; }
	const T &at(const K &k) const { return this->__find(k)->__v.second; }
	pair<iterator, bool> insert(const value_type &v) {
		__node *n = this->__find(v.first);
		if (n)
			return pair<iterator, bool>(iterator(n), false);
		n = this->__alloc();
		new ((void *)&n->__v) value_type(v);
		n->__h = H()(v.first);
		this->__link(n);
		return pair<iterator, bool>(iterator(n), true);
	}
	template <class I> void insert(I first, I last) {
		for (; first != last; ++first)
			insert(*first);
	}
	template <class KK, class... Args> pair<iterator, bool> emplace(KK &&k, Args &&...args) {
		K key(static_cast<KK &&>(k));
		__node *n = this->__find(key);
		if (n)
			return pair<iterator, bool>(iterator(n), false);
		n = this->__alloc();
		new ((void *)&n->__v.first) K(static_cast<K &&>(key));
		new ((void *)&n->__v.second) T(static_cast<Args &&>(args)...);
		n->__h = H()(n->__v.first);
		this->__link(n);
		return pair<iterator, bool>(iterator(n), true);
	}
	template <class... Args> pair<iterator, bool> try_emplace(const K &k, Args &&...args) { return emplace(k, static_cast<Args &&>(args)...); }
	template <class V> pair<iterator, bool> insert_or_assign(const K &k, V &&v) {
		__node *n = this->__find(k);
		if (n) {
			n->__v.second = static_cast<V &&>(v);
			return pair<iterator, bool>(iterator(n), false);
		}
		return emplace(k, static_cast<V &&>(v));
	}
	using __base::erase;
	iterator erase(const_iterator it) {
		__node *next = it.__p->__next;
		this->__erase(it.__p);
		return iterator(next);
	}
};
}
#endif
`
	sysHeaders["unordered_set"] = `#ifndef __CC_UNORDERED_SET
#define __CC_UNORDERED_SET
#include <__go_map>
namespace std {
template <class K, class H = hash<K>, class E = __cc_equal<K>, class A = allocator<K>>
class unordered_set : public __hashtab<K, K, __key_self, H, E> {
	typedef __hashtab<K, K, __key_self, H, E> __base;
	typedef typename __base::__node __node;

public:
	typedef K value_type;
	typedef typename __base::template __iter<const K> iterator;
	typedef typename __base::template __iter<const K> const_iterator;
	unordered_set() {}
	explicit unordered_set(size_t) {}
	unordered_set(const unordered_set &o) { this->__copy(o); }
	unordered_set(unordered_set &&o) noexcept { this->__take(o); }
	unordered_set(initializer_list<K> l) {
		for (const K *p = l.begin(); p != l.end(); ++p)
			insert(*p);
	}
	template <class I> unordered_set(I first, I last) {
		for (; first != last; ++first)
			insert(*first);
	}
	unordered_set &operator=(const unordered_set &o) {
		if (this != &o) {
			this->clear();
			this->__copy(o);
		}
		return *this;
	}
	unordered_set &operator=(unordered_set &&o) noexcept {
		if (this != &o) {
			this->clear();
			this->__take(o);
		}
		return *this;
	}
	iterator begin() const noexcept { return iterator(this->__head); }
	iterator end() const noexcept { return iterator(nullptr); }
	iterator cbegin() const noexcept { return iterator(this->__head); }
	iterator cend() const noexcept { return iterator(nullptr); }
	iterator find(const K &k) const { return iterator(this->__find(k)); }
	pair<iterator, bool> insert(const K &k) {
		__node *n = this->__find(k);
		if (n)
			return pair<iterator, bool>(iterator(n), false);
		n = this->__alloc();
		new ((void *)&n->__v) K(k);
		n->__h = H()(k);
		this->__link(n);
		return pair<iterator, bool>(iterator(n), true);
	}
	template <class I> void insert(I first, I last) {
		for (; first != last; ++first)
			insert(*first);
	}
	template <class... Args> pair<iterator, bool> emplace(Args &&...args) { return insert(K(static_cast<Args &&>(args)...)); }
	using __base::erase;
	iterator erase(const_iterator it) {
		__node *next = it.__p->__next;
		this->__erase(it.__p);
		return iterator(next);
	}
};
}
#endif
`
	sysHeaders["map"] = `#ifndef __CC_MAP
#define __CC_MAP
#include <__go_map>
#include <functional>
namespace std {
template <class K, class T, class L = less<K>, class A = allocator<pair<const K, T>>>
class map : public __ordtab<K, pair<const K, T>, __key_first, L> {
	typedef __ordtab<K, pair<const K, T>, __key_first, L> __base;
	typedef typename __base::__node __node;

public:
	typedef T mapped_type;
	typedef pair<const K, T> value_type;
	typedef typename __base::template __iter<value_type> iterator;
	typedef typename __base::template __iter<const value_type> const_iterator;
	typedef std::reverse_iterator<iterator> reverse_iterator;
	typedef std::reverse_iterator<const_iterator> const_reverse_iterator;
	map() {}
	map(const map &o) { this->__copy(o); }
	map(map &&o) noexcept { this->swap(o); }
	map(initializer_list<value_type> l) {
		for (const value_type *p = l.begin(); p != l.end(); ++p)
			insert(*p);
	}
	map &operator=(const map &o) {
		if (this != &o) {
			this->clear();
			this->__copy(o);
		}
		return *this;
	}
	map &operator=(map &&o) noexcept {
		if (this != &o) {
			this->clear();
			this->swap(o);
		}
		return *this;
	}
	iterator begin() noexcept { return iterator(this->__head, this); }
	const_iterator begin() const noexcept { return const_iterator(this->__head, this); }
	iterator end() noexcept { return iterator(nullptr, this); }
	const_iterator end() const noexcept { return const_iterator(nullptr, this); }
	const_iterator cbegin() const noexcept { return const_iterator(this->__head, this); }
	const_iterator cend() const noexcept { return const_iterator(nullptr, this); }
	reverse_iterator rbegin() noexcept { return reverse_iterator(end()); }
	reverse_iterator rend() noexcept { return reverse_iterator(begin()); }
	iterator find(const K &k) { return iterator(this->__find(k), this); }
	const_iterator find(const K &k) const { return const_iterator(this->__find(k), this); }
	iterator lower_bound(const K &k) { return iterator(this->__at(this->__lower(k)), this); }
	const_iterator lower_bound(const K &k) const { return const_iterator(this->__at(this->__lower(k)), this); }
	iterator upper_bound(const K &k) { return iterator(this->__at(this->__upper(k)), this); }
	const_iterator upper_bound(const K &k) const { return const_iterator(this->__at(this->__upper(k)), this); }
	T &operator[](const K &k) {
		__node *n = this->__find(k);
		if (!n) {
			n = this->__alloc();
			new ((void *)&n->__v.first) K(k);
			new ((void *)&n->__v.second) T();
			this->__put(this->__lower(k), n);
		}
		return n->__v.second;
	}
	T &at(const K &k) { return this->__find(k)->__v.second; }
	const T &at(const K &k) const { return this->__find(k)->__v.second; }
	pair<iterator, bool> insert(const value_type &v) {
		__node *n = this->__find(v.first);
		if (n)
			return pair<iterator, bool>(iterator(n, this), false);
		n = this->__alloc();
		new ((void *)&n->__v) value_type(v);
		this->__put(this->__lower(v.first), n);
		return pair<iterator, bool>(iterator(n, this), true);
	}
	template <class I> void insert(I first, I last) {
		for (; first != last; ++first)
			insert(*first);
	}
	template <class KK, class... Args> pair<iterator, bool> emplace(KK &&k, Args &&...args) {
		K key(static_cast<KK &&>(k));
		__node *n = this->__find(key);
		if (n)
			return pair<iterator, bool>(iterator(n, this), false);
		n = this->__alloc();
		new ((void *)&n->__v.first) K(static_cast<K &&>(key));
		new ((void *)&n->__v.second) T(static_cast<Args &&>(args)...);
		this->__put(this->__lower(n->__v.first), n);
		return pair<iterator, bool>(iterator(n, this), true);
	}
	template <class... Args> pair<iterator, bool> try_emplace(const K &k, Args &&...args) { return emplace(k, static_cast<Args &&>(args)...); }
	using __base::erase;
	iterator erase(const_iterator it) {
		__node *next = it.__p->__next;
		this->__erase(it.__p);
		return iterator(next, this);
	}
};
}
#endif
`
	sysHeaders["set"] = `#ifndef __CC_SET
#define __CC_SET
#include <__go_map>
#include <functional>
namespace std {
template <class K, class L, bool Multi>
class __set : public __ordtab<K, K, __key_self, L> {
	typedef __ordtab<K, K, __key_self, L> __base;
	typedef typename __base::__node __node;

public:
	typedef K value_type;
	typedef typename __base::template __iter<const K> iterator;
	typedef typename __base::template __iter<const K> const_iterator;
	typedef std::reverse_iterator<iterator> reverse_iterator;
	typedef std::reverse_iterator<iterator> const_reverse_iterator;
	__set() {}
	__set(const __set &o) { this->__copy(o); }
	__set(__set &&o) noexcept { this->swap(o); }
	__set(initializer_list<K> l) {
		for (const K *p = l.begin(); p != l.end(); ++p)
			insert(*p);
	}
	template <class I> __set(I first, I last) {
		for (; first != last; ++first)
			insert(*first);
	}
	__set &operator=(const __set &o) {
		if (this != &o) {
			this->clear();
			this->__copy(o);
		}
		return *this;
	}
	__set &operator=(__set &&o) noexcept {
		if (this != &o) {
			this->clear();
			this->swap(o);
		}
		return *this;
	}
	iterator begin() const noexcept { return iterator(this->__head, this); }
	iterator end() const noexcept { return iterator(nullptr, this); }
	iterator cbegin() const noexcept { return iterator(this->__head, this); }
	iterator cend() const noexcept { return iterator(nullptr, this); }
	reverse_iterator rbegin() const noexcept { return reverse_iterator(end()); }
	reverse_iterator rend() const noexcept { return reverse_iterator(begin()); }
	iterator find(const K &k) const { return iterator(this->__find(k), this); }
	iterator lower_bound(const K &k) const { return iterator(this->__at(this->__lower(k)), this); }
	iterator upper_bound(const K &k) const { return iterator(this->__at(this->__upper(k)), this); }
	pair<iterator, bool> insert(const K &k) {
		if (!Multi) {
			__node *n = this->__find(k);
			if (n)
				return pair<iterator, bool>(iterator(n, this), false);
		}
		__node *n = this->__alloc();
		new ((void *)&n->__v) K(k);
		this->__put(this->__upper(k), n);
		return pair<iterator, bool>(iterator(n, this), true);
	}
	iterator insert(const_iterator, const K &k) { return insert(k).first; }
	template <class I> void insert(I first, I last) {
		for (; first != last; ++first)
			insert(*first);
	}
	template <class... Args> pair<iterator, bool> emplace(Args &&...args) { return insert(K(static_cast<Args &&>(args)...)); }
	using __base::erase;
	iterator erase(const_iterator it) {
		__node *next = it.__p->__next;
		this->__erase(it.__p);
		return iterator(next, this);
	}
};
template <class K, class L = less<K>, class A = allocator<K>> using set = __set<K, L, false>;
template <class K, class L = less<K>, class A = allocator<K>> using multiset = __set<K, L, true>;
}
#endif
`
	sysHeaders["list"] = `#ifndef __CC_LIST
#define __CC_LIST
#include <stddef.h>
#include <new>
#include <utility>
#include <iterator>
#include <initializer_list>
#include <__go_slice>
namespace std {
template <class T, class A = allocator<T>>
class list {
	struct __node {
		__node *__prev, *__next;
		T __v;
	};
	__node *__head, *__tail;
	size_t __n;
	static __node *__alloc() { return (__node *)::operator new(sizeof(__node)); }
	// n before at, which is nullptr for the end.
	void __link(__node *n, __node *at) {
		n->__next = at;
		n->__prev = at ? at->__prev : __tail;
		if (n->__prev)
			n->__prev->__next = n;
		else
			__head = n;
		if (at)
			at->__prev = n;
		else
			__tail = n;
		__n++;
	}
	void __unlink(__node *n) {
		if (n->__prev)
			n->__prev->__next = n->__next;
		else
			__head = n->__next;
		if (n->__next)
			n->__next->__prev = n->__prev;
		else
			__tail = n->__prev;
		__n--;
	}

public:
	template <class R>
	class __iter {
	public:
		__node *__p;
		const list *__l;
		typedef bidirectional_iterator_tag iterator_category;
		typedef T value_type;
		typedef ptrdiff_t difference_type;
		typedef R *pointer;
		typedef R &reference;
		__iter() : __p(nullptr), __l(nullptr) {}
		__iter(__node *p, const list *l) : __p(p), __l(l) {}
		template <class R2> __iter(const __iter<R2> &o) : __p(o.__p), __l(o.__l) {}
		R &operator*() const { return __p->__v; }
		R *operator->() const { return &__p->__v; }
		__iter &operator++() {
			__p = __p->__next;
			return *this;
		}
		__iter operator++(int) {
			__iter t = *this;
			__p = __p->__next;
			return t;
		}
		__iter &operator--() {
			__p = __p ? __p->__prev : __l->__tail;
			return *this;
		}
		__iter operator--(int) {
			__iter t = *this;
			__p = __p ? __p->__prev : __l->__tail;
			return t;
		}
		bool operator==(const __iter &o) const { return __p == o.__p; }
		bool operator!=(const __iter &o) const { return __p != o.__p; }
	};
	typedef T value_type;
	typedef size_t size_type;
	typedef ptrdiff_t difference_type;
	typedef T &reference;
	typedef const T &const_reference;
	typedef __iter<T> iterator;
	typedef __iter<const T> const_iterator;
	typedef std::reverse_iterator<iterator> reverse_iterator;
	typedef std::reverse_iterator<const_iterator> const_reverse_iterator;

	list() : __head(nullptr), __tail(nullptr), __n(0) {}
	list(const list &o) : __head(nullptr), __tail(nullptr), __n(0) {
		for (__node *n = o.__head; n; n = n->__next)
			push_back(n->__v);
	}
	list(list &&o) noexcept : __head(o.__head), __tail(o.__tail), __n(o.__n) {
		o.__head = o.__tail = nullptr;
		o.__n = 0;
	}
	list(initializer_list<T> l) : __head(nullptr), __tail(nullptr), __n(0) {
		for (const T *p = l.begin(); p != l.end(); ++p)
			push_back(*p);
	}
	~list() { clear(); }
	list &operator=(const list &o) {
		if (this != &o) {
			clear();
			for (__node *n = o.__head; n; n = n->__next)
				push_back(n->__v);
		}
		return *this;
	}
	list &operator=(list &&o) noexcept {
		if (this != &o) {
			clear();
			swap(o);
		}
		return *this;
	}
	size_t size() const noexcept { return __n; }
	bool empty() const noexcept { return __n == 0; }
	T &front() { return __head->__v; }
	const T &front() const { return __head->__v; }
	T &back() { return __tail->__v; }
	const T &back() const { return __tail->__v; }
	iterator begin() noexcept { return iterator(__head, this); }
	const_iterator begin() const noexcept { return const_iterator(__head, this); }
	iterator end() noexcept { return iterator(nullptr, this); }
	const_iterator end() const noexcept { return const_iterator(nullptr, this); }
	const_iterator cbegin() const noexcept { return const_iterator(__head, this); }
	const_iterator cend() const noexcept { return const_iterator(nullptr, this); }
	reverse_iterator rbegin() noexcept { return reverse_iterator(end()); }
	reverse_iterator rend() noexcept { return reverse_iterator(begin()); }
	template <class... Args> iterator emplace(const_iterator pos, Args &&...args) {
		__node *n = __alloc();
		new ((void *)&n->__v) T(static_cast<Args &&>(args)...);
		__link(n, pos.__p);
		return iterator(n, this);
	}
	iterator insert(const_iterator pos, const T &v) { return emplace(pos, v); }
	iterator insert(const_iterator pos, T &&v) { return emplace(pos, static_cast<T &&>(v)); }
	void push_back(const T &v) { emplace(end(), v); }
	void push_back(T &&v) { emplace(end(), static_cast<T &&>(v)); }
	void push_front(const T &v) { emplace(begin(), v); }
	void push_front(T &&v) { emplace(begin(), static_cast<T &&>(v)); }
	template <class... Args> T &emplace_back(Args &&...args) { return *emplace(end(), static_cast<Args &&>(args)...); }
	template <class... Args> T &emplace_front(Args &&...args) { return *emplace(begin(), static_cast<Args &&>(args)...); }
	iterator erase(const_iterator pos) {
		__node *n = pos.__p, *next = n->__next;
		__unlink(n);
		n->__v.~T();
		::operator delete(n);
		return iterator(next, this);
	}
	iterator erase(const_iterator first, const_iterator last) {
		while (first.__p != last.__p)
			first = erase(first);
		return iterator(last.__p, this);
	}
	void pop_back() { erase(const_iterator(__tail, this)); }
	void pop_front() { erase(const_iterator(__head, this)); }
	void clear() noexcept {
		while (__head)
			pop_front();
	}
	void remove(const T &v) {
		for (__node *n = __head; n;) {
			__node *next = n->__next;
			if (n->__v == v)
				erase(const_iterator(n, this));
			n = next;
		}
	}
	template <class P> void remove_if(P pred) {
		for (__node *n = __head; n;) {
			__node *next = n->__next;
			if (pred(n->__v))
				erase(const_iterator(n, this));
			n = next;
		}
	}
	void swap(list &o) noexcept {
		__node *h = __head, *t = __tail;
		size_t n = __n;
		__head = o.__head;
		__tail = o.__tail;
		__n = o.__n;
		o.__head = h;
		o.__tail = t;
		o.__n = n;
	}
	void splice(const_iterator pos, list &o) {
		while (o.__head) {
			__node *n = o.__head;
			o.__unlink(n);
			__link(n, pos.__p);
		}
	}
	void splice(const_iterator pos, list &o, const_iterator it) {
		__node *n = it.__p;
		o.__unlink(n);
		__link(n, pos.__p);
	}
	void reverse() noexcept {
		for (__node *n = __head; n;) {
			__node *next = n->__next;
			n->__next = n->__prev;
			n->__prev = next;
			n = next;
		}
		__node *h = __head;
		__head = __tail;
		__tail = h;
	}
	// An insertion sort of the nodes, which keeps the order of equals.
	template <class C> void sort(C less) {
		__node *sorted = nullptr, *last = nullptr;
		for (__node *n = __head; n;) {
			__node *next = n->__next;
			__node *at = last;
			while (at && less(n->__v, at->__v))
				at = at->__prev;
			n->__prev = at;
			n->__next = at ? at->__next : sorted;
			if (n->__next)
				n->__next->__prev = n;
			else
				last = n;
			if (at)
				at->__next = n;
			else
				sorted = n;
			n = next;
		}
		__head = sorted;
		__tail = last;
	}
	void sort() {
		sort([](const T &a, const T &b) { return a < b; });
	}
};
}
#endif
`
	sysHeaders["stack"] = `#ifndef __CC_STACK
#define __CC_STACK
#include <deque>
namespace std {
template <class T, class C = deque<T>>
class stack {
protected:
	C c;

public:
	typedef T value_type;
	typedef C container_type;
	stack() {}
	bool empty() const { return c.empty(); }
	size_t size() const { return c.size(); }
	T &top() { return c.back(); }
	const T &top() const { return c.back(); }
	void push(const T &v) { c.push_back(v); }
	void push(T &&v) { c.push_back(static_cast<T &&>(v)); }
	template <class... Args> void emplace(Args &&...args) { c.emplace_back(static_cast<Args &&>(args)...); }
	void pop() { c.pop_back(); }
	void swap(stack &o) { c.swap(o.c); }
};
}
#endif
`
	sysHeaders["string_view"] = `#ifndef __CC_STRING_VIEW
#define __CC_STRING_VIEW
#include <string>
#endif
`
	// <string> has the view, which a string converts to, and <functional>
	// the hash that the unordered containers use.
	const view = `
template <class C, class Tr = char_traits<C>>
class basic_string_view {
	const C *__p;
	size_t __n;

public:
	typedef C value_type;
	typedef size_t size_type;
	typedef const C *iterator;
	typedef const C *const_iterator;
	static const size_t npos = (size_t)-1;
	constexpr basic_string_view() noexcept : __p(nullptr), __n(0) {}
	constexpr basic_string_view(const C *s, size_t n) : __p(s), __n(n) {}
	basic_string_view(const C *s) : __p(s), __n(Tr::length(s)) {}
	constexpr const C *data() const noexcept { return __p; }
	constexpr size_t size() const noexcept { return __n; }
	constexpr size_t length() const noexcept { return __n; }
	constexpr bool empty() const noexcept { return __n == 0; }
	constexpr const C &operator[](size_t i) const { return __p[i]; }
	constexpr const C &front() const { return __p[0]; }
	constexpr const C &back() const { return __p[__n - 1]; }
	constexpr const C *begin() const noexcept { return __p; }
	constexpr const C *end() const noexcept { return __p + __n; }
	void remove_prefix(size_t n) {
		__p += n;
		__n -= n;
	}
	void remove_suffix(size_t n) { __n -= n; }
	basic_string_view substr(size_t pos = 0, size_t n = npos) const {
		if (pos > __n)
			pos = __n;
		if (n > __n - pos)
			n = __n - pos;
		return basic_string_view(__p + pos, n);
	}
	int compare(basic_string_view o) const {
		size_t m = __n < o.__n ? __n : o.__n;
		for (size_t i = 0; i < m; i++) {
			if (__p[i] < o.__p[i])
				return -1;
			if (o.__p[i] < __p[i])
				return 1;
		}
		return __n < o.__n ? -1 : __n > o.__n ? 1 : 0;
	}
	size_t find(basic_string_view o, size_t pos = 0) const {
		if (o.__n > __n)
			return npos;
		for (size_t i = pos; i + o.__n <= __n; i++) {
			size_t j = 0;
			while (j < o.__n && __p[i + j] == o.__p[j])
				j++;
			if (j == o.__n)
				return i;
		}
		return npos;
	}
	size_t find(C c, size_t pos = 0) const {
		for (size_t i = pos; i < __n; i++)
			if (__p[i] == c)
				return i;
		return npos;
	}
	size_t rfind(C c) const {
		for (size_t i = __n; i > 0; i--)
			if (__p[i - 1] == c)
				return i - 1;
		return npos;
	}
};
template <class C, class Tr> bool operator==(basic_string_view<C, Tr> a, basic_string_view<C, Tr> b) { return a.compare(b) == 0; }
template <class C, class Tr> bool operator!=(basic_string_view<C, Tr> a, basic_string_view<C, Tr> b) { return a.compare(b) != 0; }
template <class C, class Tr> bool operator<(basic_string_view<C, Tr> a, basic_string_view<C, Tr> b) { return a.compare(b) < 0; }
typedef basic_string_view<char> string_view;
typedef basic_string_view<char16_t> u16string_view;
typedef basic_string_view<char32_t> u32string_view;
`
	s := sysHeaders["string"]
	const mark = "// The characters are in a slice of Go's"
	i := strings.Index(s, mark)
	if i < 0 {
		panic("cc: <string> has no string")
	}
	s = s[:i] + view[1:] + s[i:]
	const conv = "	~basic_string() {}\n"
	s = strings.Replace(s, conv, "	explicit basic_string(basic_string_view<C, Tr> v) { __add(v.data(), v.size()); }\n	operator basic_string_view<C, Tr>() const noexcept { return basic_string_view<C, Tr>(data(), size()); }\n"+conv, 1)
	s = strings.Replace(s, "template <class T> struct hash;\n", "", 1)
	s = strings.Replace(s, "#include <__go_slice>\n", "#include <__go_slice>\n#include <__hash>\n", 1)
	sysHeaders["string"] = s
	f := sysHeaders["functional"]
	sysHeaders["functional"] = strings.Replace(f, "#include <utility>\n", "#include <utility>\n#include <__hash>\n#include <memory>\n", 1)
}
