// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// The algorithms that <algorithm> has beyond headers_cxx.go's: those
// that need the iterators' types, or a buffer.
func init() {
	const more = `
template <class It, class P> It find_if_not(It first, It last, P pred) {
	for (; first != last; ++first)
		if (!pred(*first))
			break;
	return first;
}
template <class It, class P> ptrdiff_t count_if(It first, It last, P pred) {
	ptrdiff_t n = 0;
	for (; first != last; ++first)
		if (pred(*first))
			n++;
	return n;
}
template <class It, class P> bool all_of(It first, It last, P pred) {
	for (; first != last; ++first)
		if (!pred(*first))
			return false;
	return true;
}
template <class It, class P> bool any_of(It first, It last, P pred) {
	for (; first != last; ++first)
		if (pred(*first))
			return true;
	return false;
}
template <class It, class P> bool none_of(It first, It last, P pred) {
	for (; first != last; ++first)
		if (pred(*first))
			return false;
	return true;
}
template <class In, class Out, class F> Out transform(In first, In last, Out out, F f) {
	for (; first != last; ++first, ++out)
		*out = f(*first);
	return out;
}
template <class In1, class In2, class Out, class F> Out transform(In1 first, In1 last, In2 second, Out out, F f) {
	for (; first != last; ++first, ++second, ++out)
		*out = f(*first, *second);
	return out;
}
template <class In, class Out, class P> Out copy_if(In first, In last, Out out, P pred) {
	for (; first != last; ++first)
		if (pred(*first)) {
			*out = *first;
			++out;
		}
	return out;
}
template <class In, class N, class Out> Out copy_n(In first, N n, Out out) {
	for (N i = 0; i < n; ++i, ++first, ++out)
		*out = *first;
	return out;
}
template <class In, class Out> Out move(In first, In last, Out out) {
	for (; first != last; ++first, ++out)
		*out = static_cast<decltype(*first) &&>(*first);
	return out;
}
template <class It, class N, class T> It fill_n(It first, N n, const T &v) {
	for (N i = 0; i < n; ++i, ++first)
		*first = v;
	return first;
}
template <class It1, class It2> bool equal(It1 first, It1 last, It2 second) {
	for (; first != last; ++first, ++second)
		if (!(*first == *second))
			return false;
	return true;
}
template <class It1, class It2, class P> bool equal(It1 first, It1 last, It2 second, P pred) {
	for (; first != last; ++first, ++second)
		if (!pred(*first, *second))
			return false;
	return true;
}
template <class It, class T> It remove(It first, It last, const T &v) {
	It out = first;
	for (; first != last; ++first)
		if (!(*first == v)) {
			if (out != first)
				*out = static_cast<decltype(*first) &&>(*first);
			++out;
		}
	return out;
}
template <class It, class P> It remove_if(It first, It last, P pred) {
	It out = first;
	for (; first != last; ++first)
		if (!pred(*first)) {
			if (out != first)
				*out = static_cast<decltype(*first) &&>(*first);
			++out;
		}
	return out;
}
template <class It, class T> void replace(It first, It last, const T &from, const T &to) {
	for (; first != last; ++first)
		if (*first == from)
			*first = to;
}
template <class It> It unique(It first, It last) {
	if (first == last)
		return last;
	It out = first;
	for (++first; first != last; ++first)
		if (!(*out == *first)) {
			++out;
			if (out != first)
				*out = static_cast<decltype(*first) &&>(*first);
		}
	return ++out;
}
template <class It, class P> It unique(It first, It last, P pred) {
	if (first == last)
		return last;
	It out = first;
	for (++first; first != last; ++first)
		if (!pred(*out, *first)) {
			++out;
			if (out != first)
				*out = static_cast<decltype(*first) &&>(*first);
		}
	return ++out;
}
template <class It, class T> It lower_bound(It first, It last, const T &v) {
	ptrdiff_t n = last - first;
	while (n > 0) {
		ptrdiff_t h = n / 2;
		It m = first + h;
		if (*m < v) {
			first = m + 1;
			n -= h + 1;
		} else
			n = h;
	}
	return first;
}
template <class It, class T, class C> It lower_bound(It first, It last, const T &v, C less) {
	ptrdiff_t n = last - first;
	while (n > 0) {
		ptrdiff_t h = n / 2;
		It m = first + h;
		if (less(*m, v)) {
			first = m + 1;
			n -= h + 1;
		} else
			n = h;
	}
	return first;
}
template <class It, class T> It upper_bound(It first, It last, const T &v) {
	ptrdiff_t n = last - first;
	while (n > 0) {
		ptrdiff_t h = n / 2;
		It m = first + h;
		if (!(v < *m)) {
			first = m + 1;
			n -= h + 1;
		} else
			n = h;
	}
	return first;
}
template <class It, class T, class C> It upper_bound(It first, It last, const T &v, C less) {
	ptrdiff_t n = last - first;
	while (n > 0) {
		ptrdiff_t h = n / 2;
		It m = first + h;
		if (!less(v, *m)) {
			first = m + 1;
			n -= h + 1;
		} else
			n = h;
	}
	return first;
}
template <class It, class T> bool binary_search(It first, It last, const T &v) {
	first = lower_bound(first, last, v);
	return first != last && !(v < *first);
}
template <class It, class C> It min_element(It first, It last, C less) {
	It m = first;
	if (first != last)
		for (++first; first != last; ++first)
			if (less(*first, *m))
				m = first;
	return m;
}
template <class It, class C> It max_element(It first, It last, C less) {
	It m = first;
	if (first != last)
		for (++first; first != last; ++first)
			if (less(*m, *first))
				m = first;
	return m;
}
template <class It> It rotate(It first, It mid, It last) {
	reverse(first, mid);
	reverse(mid, last);
	reverse(first, last);
	return first + (last - mid);
}
template <class It1, class It2> It2 swap_ranges(It1 first, It1 last, It2 second) {
	for (; first != last; ++first, ++second) {
		auto t = static_cast<decltype(*first) &&>(*first);
		*first = static_cast<decltype(*second) &&>(*second);
		*second = static_cast<decltype(t) &&>(t);
	}
	return second;
}
// A merge sort: the halves are merged through a buffer of the
// elements.
template <class It, class C> void stable_sort(It first, It last, C less) {
	ptrdiff_t n = last - first;
	if (n < 2)
		return;
	if (n <= 8) {
		for (ptrdiff_t i = 1; i < n; i++)
			for (ptrdiff_t j = i; j > 0 && less(first[j], first[j - 1]); j--) {
				auto t = static_cast<decltype(*first) &&>(first[j]);
				first[j] = static_cast<decltype(*first) &&>(first[j - 1]);
				first[j - 1] = static_cast<decltype(t) &&>(t);
			}
		return;
	}
	It mid = first + n / 2;
	stable_sort(first, mid, less);
	stable_sort(mid, last, less);
	vector<typename iterator_traits<It>::value_type> buf;
	buf.reserve(n);
	It a = first, b = mid;
	while (a != mid && b != last) {
		if (less(*b, *a)) {
			buf.push_back(static_cast<decltype(*first) &&>(*b));
			++b;
		} else {
			buf.push_back(static_cast<decltype(*first) &&>(*a));
			++a;
		}
	}
	for (; a != mid; ++a)
		buf.push_back(static_cast<decltype(*first) &&>(*a));
	for (; b != last; ++b)
		buf.push_back(static_cast<decltype(*first) &&>(*b));
	for (ptrdiff_t i = 0; i < n; i++)
		first[i] = static_cast<decltype(*first) &&>(buf[i]);
}
template <class It> void stable_sort(It first, It last) { stable_sort(first, last, __cc_less<typename iterator_traits<It>::value_type>()); }
template <class It> struct __cc_not_ptr { typedef void type; };
template <class T> struct __cc_not_ptr<T *> {};
template <class It, class = typename __cc_not_ptr<It>::type> void sort(It first, It last) { sort(first, last, __cc_less<typename iterator_traits<It>::value_type>()); }
template <class T> struct __cc_minmax {
	T first, second;
};
`
	h := sysHeaders["algorithm"]
	i := strings.LastIndex(h, "\n}\n#endif")
	if i < 0 {
		panic("cc: <algorithm> has no end")
	}
	h = h[:i] + more + h[i:]
	// (After the guard: <vector> includes nothing of <algorithm>.)
	j := strings.Index(h, "namespace std {")
	sysHeaders["algorithm"] = h[:j] + "#include <iterator>\n#include <vector>\n" + h[j:]
}
