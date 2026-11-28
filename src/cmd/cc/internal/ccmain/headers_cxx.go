// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The C++ standard library's headers that cmd/cc has: written for it,
// with no library behind them (doc/gd/cxx.md). A <cname> header is C's
// <name.h>: std's names that are C's are found in the global namespace
// (scope.find).

func init() {
	for _, name := range []string{"assert", "ctype", "errno", "fenv", "float", "inttypes", "limits", "locale",
		"math", "setjmp", "signal", "stdarg", "stddef", "stdint", "stdio", "stdlib", "string", "time", "uchar",
		"wchar", "wctype"} {
		guard := "__CC_C" + name
		sysHeaders["c"+name] = "#ifndef " + guard + "\n#define " + guard + "\n#include <" + name + ".h>\nnamespace std {}\n#endif\n"
	}
	sysHeaders["new"] = `#ifndef __CC_NEW
#define __CC_NEW
#include <stddef.h>
namespace std {
struct nothrow_t {};
enum class align_val_t : size_t {};
}
inline void *operator new(size_t, void *__p) noexcept { return __p; }
inline void *operator new[](size_t, void *__p) noexcept { return __p; }
inline void operator delete(void *, void *) noexcept {}
inline void operator delete[](void *, void *) noexcept {}
#endif
`

	sysHeaders["type_traits"] = `#ifndef __CC_TYPE_TRAITS
#define __CC_TYPE_TRAITS
#include <stddef.h>
namespace std {
template <class T, T v>
struct integral_constant {
	static constexpr T value = v;
	typedef T value_type;
	typedef integral_constant type;
	constexpr operator value_type() const noexcept { return value; }
	constexpr value_type operator()() const noexcept { return value; }
};
template <bool B> using bool_constant = integral_constant<bool, B>;
typedef integral_constant<bool, true> true_type;
typedef integral_constant<bool, false> false_type;
typedef decltype(nullptr) nullptr_t;

template <bool B, class T = void> struct enable_if {};
template <class T> struct enable_if<true, T> { typedef T type; };
template <bool B, class T = void> using enable_if_t = typename enable_if<B, T>::type;
template <bool B, class T, class F> struct conditional { typedef T type; };
template <class T, class F> struct conditional<false, T, F> { typedef F type; };
template <bool B, class T, class F> using conditional_t = typename conditional<B, T, F>::type;
template <class...> using void_t = void;
template <class T> struct type_identity { typedef T type; };
template <class T> using type_identity_t = typename type_identity<T>::type;

template <class T> struct remove_const { typedef T type; };
template <class T> struct remove_const<const T> { typedef T type; };
template <class T> struct remove_volatile { typedef T type; };
template <class T> struct remove_volatile<volatile T> { typedef T type; };
template <class T> struct remove_cv { typedef T type; };
template <class T> struct remove_cv<const T> { typedef T type; };
template <class T> struct remove_cv<volatile T> { typedef T type; };
template <class T> struct remove_cv<const volatile T> { typedef T type; };
template <class T> struct remove_reference { typedef T type; };
template <class T> struct remove_reference<T &> { typedef T type; };
template <class T> struct remove_reference<T &&> { typedef T type; };
template <class T> struct remove_pointer { typedef T type; };
template <class T> struct remove_pointer<T *> { typedef T type; };
template <class T> struct remove_pointer<T *const> { typedef T type; };
template <class T> struct remove_extent { typedef T type; };
template <class T> struct remove_extent<T[]> { typedef T type; };
template <class T, size_t N> struct remove_extent<T[N]> { typedef T type; };
template <class T> struct add_const { typedef const T type; };
template <class T> struct add_volatile { typedef volatile T type; };
template <class T> struct add_cv { typedef const volatile T type; };
template <class T> struct add_lvalue_reference { typedef T &type; };
template <> struct add_lvalue_reference<void> { typedef void type; };
template <class T> struct add_rvalue_reference { typedef T &&type; };
template <> struct add_rvalue_reference<void> { typedef void type; };
template <class T> struct add_pointer { typedef typename remove_reference<T>::type *type; };
template <class T> using remove_const_t = typename remove_const<T>::type;
template <class T> using remove_volatile_t = typename remove_volatile<T>::type;
template <class T> using remove_cv_t = typename remove_cv<T>::type;
template <class T> using remove_reference_t = typename remove_reference<T>::type;
template <class T> using remove_pointer_t = typename remove_pointer<T>::type;
template <class T> using remove_extent_t = typename remove_extent<T>::type;
template <class T> using add_const_t = typename add_const<T>::type;
template <class T> using add_volatile_t = typename add_volatile<T>::type;
template <class T> using add_cv_t = typename add_cv<T>::type;
template <class T> using add_lvalue_reference_t = typename add_lvalue_reference<T>::type;
template <class T> using add_rvalue_reference_t = typename add_rvalue_reference<T>::type;
template <class T> using add_pointer_t = typename add_pointer<T>::type;
template <class T> struct remove_cvref { typedef remove_cv_t<remove_reference_t<T>> type; };
template <class T> using remove_cvref_t = typename remove_cvref<T>::type;
template <class T> struct underlying_type { typedef __underlying_type(T) type; };
template <class T> using underlying_type_t = typename underlying_type<T>::type;

template <class T> struct decay { typedef remove_cv_t<remove_reference_t<T>> type; };
template <class T> struct decay<T[]> { typedef T *type; };
template <class T, size_t N> struct decay<T[N]> { typedef T *type; };
template <class T, size_t N> struct decay<T (&)[N]> { typedef T *type; };
template <class T> using decay_t = typename decay<T>::type;

template <class T> struct make_unsigned { typedef __make_unsigned(T) type; };
template <class T> struct make_signed { typedef __make_signed(T) type; };
template <class T> using make_unsigned_t = typename make_unsigned<T>::type;
template <class T> using make_signed_t = typename make_signed<T>::type;

template <class T, class U> struct is_same : bool_constant<__is_same(T, U)> {};
template <class T, class U> inline constexpr bool is_same_v = __is_same(T, U);
template <class B, class D> struct is_base_of : bool_constant<__is_base_of(B, D)> {};
template <class B, class D> inline constexpr bool is_base_of_v = __is_base_of(B, D);
template <class F, class T> struct is_convertible : bool_constant<__is_convertible(F, T)> {};
template <class F, class T> inline constexpr bool is_convertible_v = __is_convertible(F, T);
template <class T, class... A> struct is_constructible : bool_constant<__is_constructible(T, A...)> {};
template <class T, class... A> inline constexpr bool is_constructible_v = __is_constructible(T, A...);
template <class T, class... A> struct is_trivially_constructible : bool_constant<__is_trivially_constructible(T, A...)> {};
template <class T, class... A> inline constexpr bool is_trivially_constructible_v = __is_trivially_constructible(T, A...);
template <class T, class... A> struct is_nothrow_constructible : bool_constant<__is_constructible(T, A...)> {};
template <class T, class... A> inline constexpr bool is_nothrow_constructible_v = __is_constructible(T, A...);
template <class T, class U> struct is_assignable : bool_constant<__is_assignable(T, U)> {};
template <class T, class U> inline constexpr bool is_assignable_v = __is_assignable(T, U);
template <class T, class U> struct is_trivially_assignable : bool_constant<__is_trivially_assignable(T, U)> {};
template <class T, class U> inline constexpr bool is_trivially_assignable_v = __is_trivially_assignable(T, U);

#define __CC_TRAIT1(name, builtin) \
	template <class T> struct name : bool_constant<builtin> {}; \
	template <class T> inline constexpr bool name##_v = builtin;
__CC_TRAIT1(is_void, __is_void(remove_cv_t<T>))
__CC_TRAIT1(is_null_pointer, __is_nullptr(remove_cv_t<T>))
__CC_TRAIT1(is_integral, __is_integral(T))
__CC_TRAIT1(is_floating_point, __is_floating_point(T))
__CC_TRAIT1(is_arithmetic, __is_arithmetic(T))
__CC_TRAIT1(is_fundamental, __is_fundamental(remove_cv_t<T>))
__CC_TRAIT1(is_scalar, __is_scalar(T))
__CC_TRAIT1(is_object, __is_object(T))
__CC_TRAIT1(is_compound, __is_compound(remove_cv_t<T>))
__CC_TRAIT1(is_signed, __is_signed(T))
__CC_TRAIT1(is_unsigned, __is_unsigned(T))
__CC_TRAIT1(is_const, __is_const(T))
__CC_TRAIT1(is_volatile, __is_volatile(T))
__CC_TRAIT1(is_pointer, __is_pointer(T))
__CC_TRAIT1(is_reference, __is_reference(T))
__CC_TRAIT1(is_lvalue_reference, __is_lvalue_reference(T))
__CC_TRAIT1(is_rvalue_reference, __is_rvalue_reference(T))
__CC_TRAIT1(is_array, __is_array(T))
__CC_TRAIT1(is_function, __is_function(T))
__CC_TRAIT1(is_member_pointer, __is_member_pointer(T))
__CC_TRAIT1(is_member_object_pointer, __is_member_object_pointer(T))
__CC_TRAIT1(is_member_function_pointer, __is_member_function_pointer(T))
__CC_TRAIT1(is_enum, __is_enum(T))
__CC_TRAIT1(is_class, __is_class(T))
__CC_TRAIT1(is_union, __is_union(T))
__CC_TRAIT1(is_empty, __is_empty(T))
__CC_TRAIT1(is_polymorphic, __is_polymorphic(T))
__CC_TRAIT1(is_abstract, __is_abstract(T))
__CC_TRAIT1(is_final, __is_final(T))
__CC_TRAIT1(is_aggregate, __is_aggregate(T))
__CC_TRAIT1(is_trivial, __is_trivial(T))
__CC_TRAIT1(is_pod, __is_pod(T))
__CC_TRAIT1(is_standard_layout, __is_standard_layout(T))
__CC_TRAIT1(is_trivially_copyable, __is_trivially_copyable(T))
__CC_TRAIT1(is_trivially_destructible, __is_trivially_destructible(T))
__CC_TRAIT1(is_destructible, __is_destructible(T))
__CC_TRAIT1(is_nothrow_destructible, __is_destructible(T))
__CC_TRAIT1(has_virtual_destructor, __has_virtual_destructor(T))
__CC_TRAIT1(is_default_constructible, __is_constructible(T))
__CC_TRAIT1(is_trivially_default_constructible, __is_trivially_constructible(T))
__CC_TRAIT1(is_nothrow_default_constructible, __is_constructible(T))
__CC_TRAIT1(is_copy_constructible, __is_constructible(T, const T &))
__CC_TRAIT1(is_trivially_copy_constructible, __is_trivially_constructible(T, const T &))
__CC_TRAIT1(is_nothrow_copy_constructible, __is_constructible(T, const T &))
__CC_TRAIT1(is_move_constructible, __is_constructible(T, T &&))
__CC_TRAIT1(is_trivially_move_constructible, __is_trivially_constructible(T, T &&))
__CC_TRAIT1(is_nothrow_move_constructible, __is_constructible(T, T &&))
__CC_TRAIT1(is_copy_assignable, __is_assignable(T &, const T &))
__CC_TRAIT1(is_trivially_copy_assignable, __is_trivially_assignable(T &, const T &))
__CC_TRAIT1(is_nothrow_copy_assignable, __is_assignable(T &, const T &))
__CC_TRAIT1(is_move_assignable, __is_assignable(T &, T &&))
__CC_TRAIT1(is_trivially_move_assignable, __is_trivially_assignable(T &, T &&))
__CC_TRAIT1(is_nothrow_move_assignable, __is_assignable(T &, T &&))
#undef __CC_TRAIT1

template <class... B> struct conjunction : true_type {};
template <class B1> struct conjunction<B1> : B1 {};
template <class B1, class... Bn> struct conjunction<B1, Bn...> : conditional_t<bool(B1::value), conjunction<Bn...>, B1> {};
template <class... B> struct disjunction : false_type {};
template <class B1> struct disjunction<B1> : B1 {};
template <class B1, class... Bn> struct disjunction<B1, Bn...> : conditional_t<bool(B1::value), B1, disjunction<Bn...>> {};
template <class B> struct negation : bool_constant<!bool(B::value)> {};
template <class... B> inline constexpr bool conjunction_v = conjunction<B...>::value;
template <class... B> inline constexpr bool disjunction_v = disjunction<B...>::value;
template <class B> inline constexpr bool negation_v = !bool(B::value);

template <class T> add_rvalue_reference_t<T> declval() noexcept;
}
#endif
`
	sysHeaders["utility"] = `#ifndef __CC_UTILITY
#define __CC_UTILITY
#include <type_traits>
#include <initializer_list>
namespace std {
template <class T> constexpr remove_reference_t<T> &&move(T &&t) noexcept { return static_cast<remove_reference_t<T> &&>(t); }
template <class T> constexpr T &&forward(remove_reference_t<T> &t) noexcept { return static_cast<T &&>(t); }
template <class T> constexpr T &&forward(remove_reference_t<T> &&t) noexcept { return static_cast<T &&>(t); }
template <class T> void swap(T &a, T &b) {
	T t = static_cast<T &&>(a);
	a = static_cast<T &&>(b);
	b = static_cast<T &&>(t);
}
template <class T, class U = T> T exchange(T &obj, U &&value) {
	T old = static_cast<T &&>(obj);
	obj = static_cast<U &&>(value);
	return old;
}
template <class T1, class T2>
struct pair {
	typedef T1 first_type;
	typedef T2 second_type;
	T1 first;
	T2 second;
	constexpr pair() : first(), second() {}
	constexpr pair(const T1 &a, const T2 &b) : first(a), second(b) {}
	template <class U1, class U2> constexpr pair(const pair<U1, U2> &o) : first(o.first), second(o.second) {}
};
template <class T1, class T2> constexpr pair<decay_t<T1>, decay_t<T2>> make_pair(T1 &&a, T2 &&b) {
	return pair<decay_t<T1>, decay_t<T2>>(static_cast<T1 &&>(a), static_cast<T2 &&>(b));
}
template <class T1, class T2> constexpr bool operator==(const pair<T1, T2> &a, const pair<T1, T2> &b) { return a.first == b.first && a.second == b.second; }
template <class T1, class T2> constexpr bool operator!=(const pair<T1, T2> &a, const pair<T1, T2> &b) { return !(a == b); }
template <class T1, class T2> constexpr bool operator<(const pair<T1, T2> &a, const pair<T1, T2> &b) { return a.first < b.first || !(b.first < a.first) && a.second < b.second; }
template <class T1, class T2> constexpr bool operator>(const pair<T1, T2> &a, const pair<T1, T2> &b) { return b < a; }
template <class T1, class T2> constexpr bool operator<=(const pair<T1, T2> &a, const pair<T1, T2> &b) { return !(b < a); }
template <class T1, class T2> constexpr bool operator>=(const pair<T1, T2> &a, const pair<T1, T2> &b) { return !(a < b); }
struct in_place_t { explicit in_place_t() = default; };
inline constexpr in_place_t in_place{};
}
#endif
`
	sysHeaders["initializer_list"] = `#ifndef __CC_INITIALIZER_LIST
#define __CC_INITIALIZER_LIST
#include <stddef.h>
namespace std {
template <class E>
class initializer_list {
	const E *__begin;
	size_t __size;

public:
	typedef E value_type;
	typedef const E &reference;
	typedef const E &const_reference;
	typedef size_t size_type;
	typedef const E *iterator;
	typedef const E *const_iterator;
	constexpr initializer_list() noexcept : __begin(nullptr), __size(0) {}
	constexpr initializer_list(const E *b, size_t n) noexcept : __begin(b), __size(n) {}
	constexpr size_t size() const noexcept { return __size; }
	constexpr const E *begin() const noexcept { return __begin; }
	constexpr const E *end() const noexcept { return __begin + __size; }
};
template <class E> constexpr const E *begin(initializer_list<E> il) noexcept { return il.begin(); }
template <class E> constexpr const E *end(initializer_list<E> il) noexcept { return il.end(); }
}
#endif
`

	// The algorithms that need no more than iterators that are
	// pointers or behave as them.
	sysHeaders["algorithm"] = `#ifndef __CC_ALGORITHM
#define __CC_ALGORITHM
#include <stddef.h>
#include <utility>
#include <initializer_list>
namespace std {
template <class T> constexpr const T &min(const T &a, const T &b) { return b < a ? b : a; }
template <class T> constexpr const T &max(const T &a, const T &b) { return a < b ? b : a; }
template <class T, class C> constexpr const T &min(const T &a, const T &b, C less) { return less(b, a) ? b : a; }
template <class T, class C> constexpr const T &max(const T &a, const T &b, C less) { return less(a, b) ? b : a; }
template <class T> constexpr T min(initializer_list<T> l) {
	const T *m = l.begin();
	for (const T *p = l.begin(); p != l.end(); ++p)
		if (*p < *m)
			m = p;
	return *m;
}
template <class T> constexpr T max(initializer_list<T> l) {
	const T *m = l.begin();
	for (const T *p = l.begin(); p != l.end(); ++p)
		if (*m < *p)
			m = p;
	return *m;
}
template <class T> constexpr const T &clamp(const T &v, const T &lo, const T &hi) { return v < lo ? lo : hi < v ? hi : v; }
template <class It, class T> It find(It first, It last, const T &v) {
	for (; first != last; ++first)
		if (*first == v)
			break;
	return first;
}
template <class It, class P> It find_if(It first, It last, P pred) {
	for (; first != last; ++first)
		if (pred(*first))
			break;
	return first;
}
template <class It, class T> ptrdiff_t count(It first, It last, const T &v) {
	ptrdiff_t n = 0;
	for (; first != last; ++first)
		if (*first == v)
			n++;
	return n;
}
template <class It, class F> F for_each(It first, It last, F f) {
	for (; first != last; ++first)
		f(*first);
	return f;
}
template <class It, class T> void fill(It first, It last, const T &v) {
	for (; first != last; ++first)
		*first = v;
}
template <class In, class Out> Out copy(In first, In last, Out out) {
	for (; first != last; ++first, ++out)
		*out = *first;
	return out;
}
template <class It> void reverse(It first, It last) {
	while (first != last && first != --last) {
		swap(*first, *last);
		++first;
	}
}
template <class It> It min_element(It first, It last) {
	It m = first;
	for (; first != last; ++first)
		if (*first < *m)
			m = first;
	return m;
}
template <class It> It max_element(It first, It last) {
	It m = first;
	for (; first != last; ++first)
		if (*m < *first)
			m = first;
	return m;
}
template <class It, class C> void __cc_sift(It first, ptrdiff_t i, ptrdiff_t n, C less) {
	for (;;) {
		ptrdiff_t k = 2 * i + 1;
		if (k >= n)
			return;
		if (k + 1 < n && less(first[k], first[k + 1]))
			k++;
		if (!less(first[i], first[k]))
			return;
		swap(first[i], first[k]);
		i = k;
	}
}
template <class It, class C> void sort(It first, It last, C less) {
	ptrdiff_t n = last - first;
	for (ptrdiff_t i = n / 2 - 1; i >= 0; i--)
		__cc_sift(first, i, n, less);
	for (ptrdiff_t i = n - 1; i > 0; i--) {
		swap(first[0], first[i]);
		__cc_sift(first, (ptrdiff_t)0, i, less);
	}
}
template <class T> struct __cc_less {
	constexpr bool operator()(const T &a, const T &b) const { return a < b; }
};
template <class T> void sort(T *first, T *last) { sort(first, last, __cc_less<T>()); }
}
#endif
`

	sysHeaders["array"] = `#ifndef __CC_ARRAY
#define __CC_ARRAY
#include <stddef.h>
#include <initializer_list>
namespace std {
template <class T, size_t N>
struct array {
	typedef T value_type;
	typedef size_t size_type;
	typedef ptrdiff_t difference_type;
	typedef T &reference;
	typedef const T &const_reference;
	typedef T *pointer;
	typedef const T *const_pointer;
	typedef T *iterator;
	typedef const T *const_iterator;
	T __elems[N];
	constexpr T *data() noexcept { return __elems; }
	constexpr const T *data() const noexcept { return __elems; }
	constexpr size_t size() const noexcept { return N; }
	constexpr size_t max_size() const noexcept { return N; }
	constexpr bool empty() const noexcept { return N == 0; }
	constexpr T &operator[](size_t i) { return __elems[i]; }
	constexpr const T &operator[](size_t i) const { return __elems[i]; }
	constexpr T &at(size_t i) { return __elems[i]; }
	constexpr const T &at(size_t i) const { return __elems[i]; }
	constexpr T &front() { return __elems[0]; }
	constexpr const T &front() const { return __elems[0]; }
	constexpr T &back() { return __elems[N - 1]; }
	constexpr const T &back() const { return __elems[N - 1]; }
	constexpr T *begin() noexcept { return __elems; }
	constexpr const T *begin() const noexcept { return __elems; }
	constexpr T *end() noexcept { return __elems + N; }
	constexpr const T *end() const noexcept { return __elems + N; }
	constexpr const T *cbegin() const noexcept { return __elems; }
	constexpr const T *cend() const noexcept { return __elems + N; }
	void fill(const T &v) {
		for (size_t i = 0; i < N; i++)
			__elems[i] = v;
	}
};
template <class T, size_t N>
bool operator==(const array<T, N> &a, const array<T, N> &b) {
	for (size_t i = 0; i < N; i++)
		if (!(a[i] == b[i]))
			return false;
	return true;
}
template <class T, size_t N>
bool operator!=(const array<T, N> &a, const array<T, N> &b) { return !(a == b); }
}
#endif
`

	sysHeaders["atomic"] = `#ifndef __CC_ATOMIC
#define __CC_ATOMIC
#include <stddef.h>
#include <stdint.h>
namespace std {
enum memory_order {
	memory_order_relaxed,
	memory_order_consume,
	memory_order_acquire,
	memory_order_release,
	memory_order_acq_rel,
	memory_order_seq_cst
};
template <class T>
struct atomic {
	T __v;
	atomic() noexcept = default;
	constexpr atomic(T v) noexcept : __v(v) {}
	atomic(const atomic &) = delete;
	atomic &operator=(const atomic &) = delete;
	static constexpr bool is_always_lock_free = true;
	bool is_lock_free() const noexcept { return true; }
	T load(memory_order = memory_order_seq_cst) const noexcept { return __cc_atomic_load(const_cast<T *>(&__v)); }
	void store(T v, memory_order = memory_order_seq_cst) noexcept { __cc_atomic_store(&__v, v); }
	T exchange(T v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '=', v); }
	bool compare_exchange_strong(T &e, T d, memory_order = memory_order_seq_cst, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_cmpxchg(&__v, &e, d); }
	bool compare_exchange_weak(T &e, T d, memory_order = memory_order_seq_cst, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_cmpxchg(&__v, &e, d); }
	T fetch_add(T v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '+', v); }
	T fetch_sub(T v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '-', v); }
	T fetch_and(T v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '&', v); }
	T fetch_or(T v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '|', v); }
	T fetch_xor(T v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '^', v); }
	operator T() const noexcept { return load(); }
	T operator=(T v) noexcept {
		store(v);
		return v;
	}
	T operator++() noexcept { return fetch_add(1) + 1; }
	T operator++(int) noexcept { return fetch_add(1); }
	T operator--() noexcept { return fetch_sub(1) - 1; }
	T operator--(int) noexcept { return fetch_sub(1); }
	T operator+=(T v) noexcept { return fetch_add(v) + v; }
	T operator-=(T v) noexcept { return fetch_sub(v) - v; }
	T operator&=(T v) noexcept { return fetch_and(v) & v; }
	T operator|=(T v) noexcept { return fetch_or(v) | v; }
	T operator^=(T v) noexcept { return fetch_xor(v) ^ v; }
};
template <class T>
struct atomic<T *> {
	T *__v;
	atomic() noexcept = default;
	constexpr atomic(T *v) noexcept : __v(v) {}
	atomic(const atomic &) = delete;
	atomic &operator=(const atomic &) = delete;
	static constexpr bool is_always_lock_free = true;
	bool is_lock_free() const noexcept { return true; }
	T *load(memory_order = memory_order_seq_cst) const noexcept { return __cc_atomic_load(const_cast<T **>(&__v)); }
	void store(T *v, memory_order = memory_order_seq_cst) noexcept { __cc_atomic_store(&__v, v); }
	T *exchange(T *v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '=', v); }
	bool compare_exchange_strong(T *&e, T *d, memory_order = memory_order_seq_cst, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_cmpxchg(&__v, &e, d); }
	bool compare_exchange_weak(T *&e, T *d, memory_order = memory_order_seq_cst, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_cmpxchg(&__v, &e, d); }
	T *fetch_add(ptrdiff_t v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '+', v); }
	T *fetch_sub(ptrdiff_t v, memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '-', v); }
	operator T *() const noexcept { return load(); }
	T *operator=(T *v) noexcept {
		store(v);
		return v;
	}
};
struct atomic_flag {
	bool __v = false;
	atomic_flag() noexcept = default;
	atomic_flag(const atomic_flag &) = delete;
	bool test_and_set(memory_order = memory_order_seq_cst) noexcept { return __cc_atomic_fetch(&__v, '=', true); }
	void clear(memory_order = memory_order_seq_cst) noexcept { __cc_atomic_store(&__v, false); }
	bool test(memory_order = memory_order_seq_cst) const noexcept { return __cc_atomic_load(const_cast<bool *>(&__v)); }
};
inline void atomic_thread_fence(memory_order) noexcept { __cc_atomic_fence(); }
inline void atomic_signal_fence(memory_order) noexcept { __cc_atomic_fence(); }
typedef atomic<bool> atomic_bool;
typedef atomic<char> atomic_char;
typedef atomic<signed char> atomic_schar;
typedef atomic<unsigned char> atomic_uchar;
typedef atomic<short> atomic_short;
typedef atomic<unsigned short> atomic_ushort;
typedef atomic<int> atomic_int;
typedef atomic<unsigned> atomic_uint;
typedef atomic<long> atomic_long;
typedef atomic<unsigned long> atomic_ulong;
typedef atomic<long long> atomic_llong;
typedef atomic<unsigned long long> atomic_ullong;
typedef atomic<int8_t> atomic_int8_t;
typedef atomic<uint8_t> atomic_uint8_t;
typedef atomic<int16_t> atomic_int16_t;
typedef atomic<uint16_t> atomic_uint16_t;
typedef atomic<int32_t> atomic_int32_t;
typedef atomic<uint32_t> atomic_uint32_t;
typedef atomic<int64_t> atomic_int64_t;
typedef atomic<uint64_t> atomic_uint64_t;
typedef atomic<size_t> atomic_size_t;
typedef atomic<intptr_t> atomic_intptr_t;
typedef atomic<uintptr_t> atomic_uintptr_t;
}
#define ATOMIC_FLAG_INIT {}
#endif
`
	sysHeaders["limits"] = `#ifndef __CC_LIMITS
#define __CC_LIMITS
#include <limits.h>
#include <float.h>
#include <math.h>
namespace std {
enum float_round_style { round_indeterminate = -1, round_toward_zero, round_to_nearest, round_toward_infinity, round_toward_neg_infinity };
template <class T>
struct numeric_limits {
	static constexpr bool is_specialized = false;
	static constexpr bool is_signed = false, is_integer = false, is_exact = false;
	static constexpr bool has_infinity = false, has_quiet_NaN = false, has_signaling_NaN = false;
	static constexpr bool is_iec559 = false, is_bounded = false, is_modulo = false;
	static constexpr int digits = 0, digits10 = 0, max_digits10 = 0, radix = 0;
	static constexpr T min() noexcept { return T(); }
	static constexpr T max() noexcept { return T(); }
	static constexpr T lowest() noexcept { return T(); }
	static constexpr T epsilon() noexcept { return T(); }
	static constexpr T infinity() noexcept { return T(); }
	static constexpr T quiet_NaN() noexcept { return T(); }
};
#define __CC_LIMITS_INT(T, MIN, MAX, SIGNED) \
	template <> struct numeric_limits<T> { \
		static constexpr bool is_specialized = true; \
		static constexpr bool is_signed = SIGNED, is_integer = true, is_exact = true; \
		static constexpr bool has_infinity = false, has_quiet_NaN = false, has_signaling_NaN = false; \
		static constexpr bool is_iec559 = false, is_bounded = true, is_modulo = !SIGNED; \
		static constexpr int digits = int(sizeof(T) * 8 - SIGNED), digits10 = int((sizeof(T) * 8 - SIGNED) * 301 / 1000), max_digits10 = 0, radix = 2; \
		static constexpr T min() noexcept { return MIN; } \
		static constexpr T max() noexcept { return MAX; } \
		static constexpr T lowest() noexcept { return MIN; } \
		static constexpr T epsilon() noexcept { return 0; } \
		static constexpr T infinity() noexcept { return 0; } \
		static constexpr T quiet_NaN() noexcept { return 0; } \
		static constexpr T signaling_NaN() noexcept { return 0; } \
		static constexpr T denorm_min() noexcept { return 0; } \
		static constexpr T round_error() noexcept { return 0; } \
	};
__CC_LIMITS_INT(bool, false, true, 0)
__CC_LIMITS_INT(char, CHAR_MIN, CHAR_MAX, 1)
__CC_LIMITS_INT(signed char, SCHAR_MIN, SCHAR_MAX, 1)
__CC_LIMITS_INT(unsigned char, 0, UCHAR_MAX, 0)
__CC_LIMITS_INT(short, SHRT_MIN, SHRT_MAX, 1)
__CC_LIMITS_INT(unsigned short, 0, USHRT_MAX, 0)
__CC_LIMITS_INT(int, INT_MIN, INT_MAX, 1)
__CC_LIMITS_INT(unsigned int, 0, UINT_MAX, 0)
__CC_LIMITS_INT(long, LONG_MIN, LONG_MAX, 1)
__CC_LIMITS_INT(unsigned long, 0, ULONG_MAX, 0)
__CC_LIMITS_INT(long long, LLONG_MIN, LLONG_MAX, 1)
__CC_LIMITS_INT(unsigned long long, 0, ULLONG_MAX, 0)
#undef __CC_LIMITS_INT
#define __CC_LIMITS_FLT(T, P, DIG, MANT, DIG10) \
	template <> struct numeric_limits<T> { \
		static constexpr bool is_specialized = true; \
		static constexpr bool is_signed = true, is_integer = false, is_exact = false; \
		static constexpr bool has_infinity = true, has_quiet_NaN = true, has_signaling_NaN = true; \
		static constexpr bool is_iec559 = true, is_bounded = true, is_modulo = false; \
		static constexpr int digits = MANT, digits10 = DIG, max_digits10 = DIG10, radix = 2; \
		static constexpr int min_exponent = P##_MIN_EXP, max_exponent = P##_MAX_EXP; \
		static constexpr int min_exponent10 = P##_MIN_10_EXP, max_exponent10 = P##_MAX_10_EXP; \
		static constexpr float_round_style round_style = round_to_nearest; \
		static constexpr T min() noexcept { return P##_MIN; } \
		static constexpr T max() noexcept { return P##_MAX; } \
		static constexpr T lowest() noexcept { return -P##_MAX; } \
		static constexpr T epsilon() noexcept { return P##_EPSILON; } \
		static constexpr T infinity() noexcept { return T(INFINITY); } \
		static constexpr T quiet_NaN() noexcept { return T(NAN); } \
		static constexpr T signaling_NaN() noexcept { return T(NAN); } \
		static constexpr T denorm_min() noexcept { return P##_TRUE_MIN; } \
		static constexpr T round_error() noexcept { return T(0.5); } \
	};
__CC_LIMITS_FLT(float, FLT, FLT_DIG, FLT_MANT_DIG, 9)
__CC_LIMITS_FLT(double, DBL, DBL_DIG, DBL_MANT_DIG, 17)
__CC_LIMITS_FLT(long double, LDBL, LDBL_DIG, LDBL_MANT_DIG, 17)
#undef __CC_LIMITS_FLT
}
#endif
`

	sysHeaders["cstdlib"] = `#ifndef __CC_CSTDLIB
#define __CC_CSTDLIB
#include <stdlib.h>
namespace std {
#ifndef __CC_ABS_INT
#define __CC_ABS_INT
using ::abs;
inline long abs(long __x) { return ::labs(__x); }
inline long long abs(long long __x) { return ::llabs(__x); }
#endif
}
#endif
`
	sysHeaders["cmath"] = `#ifndef __CC_CMATH
#define __CC_CMATH
#include <math.h>
#include <stdlib.h>
#include <type_traits>
namespace std {
#ifndef __CC_ABS_INT
#define __CC_ABS_INT
using ::abs;
inline long abs(long __x) { return ::labs(__x); }
inline long long abs(long long __x) { return ::llabs(__x); }
#endif
inline float abs(float __x) { return ::fabsf(__x); }
inline double abs(double __x) { return ::fabs(__x); }
inline long double abs(long double __x) { return ::fabsl(__x); }

#define __CC_MATH1(fn) \
	using ::fn; \
	inline float fn(float __x) { return ::fn##f(__x); } \
	inline long double fn(long double __x) { return ::fn##l(__x); } \
	template <class __T, enable_if_t<is_integral_v<__T>, int> = 0> inline double fn(__T __x) { return ::fn(double(__x)); }
#define __CC_MATH2(fn) \
	using ::fn; \
	inline float fn(float __x, float __y) { return ::fn##f(__x, __y); } \
	inline long double fn(long double __x, long double __y) { return ::fn##l(__x, __y); } \
	template <class __A, class __B, enable_if_t<is_arithmetic_v<__A> && is_arithmetic_v<__B>, int> = 0> \
	inline double fn(__A __x, __B __y) { return ::fn(double(__x), double(__y)); }
__CC_MATH1(sqrt) __CC_MATH1(cbrt) __CC_MATH1(sin) __CC_MATH1(cos) __CC_MATH1(tan) __CC_MATH1(asin) __CC_MATH1(acos)
__CC_MATH1(atan) __CC_MATH1(sinh) __CC_MATH1(cosh) __CC_MATH1(tanh) __CC_MATH1(asinh) __CC_MATH1(acosh) __CC_MATH1(atanh)
__CC_MATH1(exp) __CC_MATH1(exp2) __CC_MATH1(expm1) __CC_MATH1(log) __CC_MATH1(log2) __CC_MATH1(log10) __CC_MATH1(log1p)
__CC_MATH1(floor) __CC_MATH1(ceil) __CC_MATH1(round) __CC_MATH1(trunc) __CC_MATH1(rint) __CC_MATH1(nearbyint)
__CC_MATH1(fabs) __CC_MATH1(erf) __CC_MATH1(erfc) __CC_MATH1(tgamma) __CC_MATH1(lgamma) __CC_MATH1(logb)
__CC_MATH2(pow) __CC_MATH2(atan2) __CC_MATH2(fmod) __CC_MATH2(hypot) __CC_MATH2(copysign) __CC_MATH2(fmin)
__CC_MATH2(fmax) __CC_MATH2(fdim) __CC_MATH2(remainder) __CC_MATH2(nextafter)
#undef __CC_MATH1
#undef __CC_MATH2
using ::ldexp;
inline float ldexp(float __x, int __e) { return ::ldexpf(__x, __e); }
inline long double ldexp(long double __x, int __e) { return ::ldexpl(__x, __e); }
using ::scalbn;
inline float scalbn(float __x, int __e) { return ::scalbnf(__x, __e); }
using ::frexp;
inline float frexp(float __x, int *__e) { return ::frexpf(__x, __e); }
inline long double frexp(long double __x, int *__e) { return ::frexpl(__x, __e); }
using ::modf;
inline float modf(float __x, float *__i) { return ::modff(__x, __i); }
inline long double modf(long double __x, long double *__i) { return ::modfl(__x, __i); }
using ::ilogb;
inline int ilogb(float __x) { return ::ilogbf(__x); }
using ::lround;
inline long lround(float __x) { return ::lroundf(__x); }
using ::llround;
inline long long llround(float __x) { return ::llroundf(__x); }
using ::lrint;
inline long lrint(float __x) { return ::lrintf(__x); }
using ::fma;
inline float fma(float __x, float __y, float __z) { return ::fmaf(__x, __y, __z); }

#undef fpclassify
#undef isnan
#undef isinf
#undef isfinite
#undef isnormal
#undef signbit
inline int fpclassify(float __x) { return ::__cc_fpclassifyf(__x); }
inline int fpclassify(double __x) { return ::__cc_fpclassify(__x); }
inline int fpclassify(long double __x) { return ::__cc_fpclassify(double(__x)); }
#define __CC_CLASSIFY(fn, cond) \
	inline bool fn(float __x) { int __c = ::__cc_fpclassifyf(__x); return cond; } \
	inline bool fn(double __x) { int __c = ::__cc_fpclassify(__x); return cond; } \
	inline bool fn(long double __x) { int __c = ::__cc_fpclassify(double(__x)); return cond; } \
	template <class __T, enable_if_t<is_integral_v<__T>, int> = 0> inline bool fn(__T __x) { int __c = ::__cc_fpclassify(double(__x)); return cond; }
__CC_CLASSIFY(isnan, __c == FP_NAN)
__CC_CLASSIFY(isinf, __c == FP_INFINITE)
__CC_CLASSIFY(isfinite, __c >= FP_ZERO)
__CC_CLASSIFY(isnormal, __c == FP_NORMAL)
#undef __CC_CLASSIFY
inline bool signbit(float __x) { return ::__cc_signbit(double(__x)) != 0; }
inline bool signbit(double __x) { return ::__cc_signbit(__x) != 0; }
inline bool signbit(long double __x) { return ::__cc_signbit(double(__x)) != 0; }
}
using std::fpclassify;
using std::isfinite;
using std::isinf;
using std::isnan;
using std::isnormal;
using std::signbit;
#endif
`

	sysHeaders["functional"] = `#ifndef __CC_FUNCTIONAL
#define __CC_FUNCTIONAL
#include <type_traits>
#include <utility>
namespace std {
template <class> class function;
template <class R, class... A>
class function<R(A...)> {
	struct __base {
		virtual ~__base() {}
		virtual R __call(A... a) = 0;
		virtual __base *__clone() const = 0;
	};
	template <class F>
	struct __impl : __base {
		F __f;
		__impl(const F &f) : __f(f) {}
		R __call(A... a) override { return __f(a...); }
		__base *__clone() const override { return new __impl(__f); }
	};
	__base *__p;

public:
	typedef R result_type;
	function() noexcept : __p(nullptr) {}
	function(nullptr_t) noexcept : __p(nullptr) {}
	function(const function &o) : __p(o.__p ? o.__p->__clone() : nullptr) {}
	function(function &&o) noexcept : __p(o.__p) { o.__p = nullptr; }
	template <class F, class = enable_if_t<!is_same_v<decay_t<F>, function>>>
	function(F f) : __p(new __impl<F>(f)) {}
	~function() { delete __p; }
	function &operator=(const function &o) {
		if (this != &o) {
			__base *n = o.__p ? o.__p->__clone() : nullptr;
			delete __p;
			__p = n;
		}
		return *this;
	}
	function &operator=(function &&o) noexcept {
		if (this != &o) {
			delete __p;
			__p = o.__p;
			o.__p = nullptr;
		}
		return *this;
	}
	function &operator=(nullptr_t) noexcept {
		delete __p;
		__p = nullptr;
		return *this;
	}
	explicit operator bool() const noexcept { return __p != nullptr; }
	R operator()(A... a) const { return __p->__call(a...); }
};
template <class R, class... A> bool operator==(const function<R(A...)> &f, nullptr_t) noexcept { return !f; }
template <class R, class... A> bool operator!=(const function<R(A...)> &f, nullptr_t) noexcept { return bool(f); }
template <class T = void> struct less { constexpr bool operator()(const T &a, const T &b) const { return a < b; } };
template <class T = void> struct greater { constexpr bool operator()(const T &a, const T &b) const { return a > b; } };
template <class T = void> struct equal_to { constexpr bool operator()(const T &a, const T &b) const { return a == b; } };
}
#endif
`
	sysHeaders["mutex"] = `#ifndef __CC_MUTEX
#define __CC_MUTEX
#include <errno.h> // as libstdc++'s does, with <system_error>
#include <pthread.h>
#include <utility>
namespace std {
class mutex {
	pthread_mutex_t __m;

public:
	typedef pthread_mutex_t *native_handle_type;
	mutex() noexcept { pthread_mutex_init(&__m, nullptr); }
	~mutex() { pthread_mutex_destroy(&__m); }
	mutex(const mutex &) = delete;
	mutex &operator=(const mutex &) = delete;
	void lock() { pthread_mutex_lock(&__m); }
	bool try_lock() { return pthread_mutex_trylock(&__m) == 0; }
	void unlock() { pthread_mutex_unlock(&__m); }
	native_handle_type native_handle() { return &__m; }
};
class recursive_mutex {
	pthread_mutex_t __m;

public:
	typedef pthread_mutex_t *native_handle_type;
	recursive_mutex() {
		pthread_mutexattr_t a;
		pthread_mutexattr_init(&a);
		pthread_mutexattr_settype(&a, PTHREAD_MUTEX_RECURSIVE);
		pthread_mutex_init(&__m, &a);
		pthread_mutexattr_destroy(&a);
	}
	~recursive_mutex() { pthread_mutex_destroy(&__m); }
	recursive_mutex(const recursive_mutex &) = delete;
	recursive_mutex &operator=(const recursive_mutex &) = delete;
	void lock() { pthread_mutex_lock(&__m); }
	bool try_lock() { return pthread_mutex_trylock(&__m) == 0; }
	void unlock() { pthread_mutex_unlock(&__m); }
	native_handle_type native_handle() { return &__m; }
};
struct defer_lock_t { explicit defer_lock_t() = default; };
struct try_to_lock_t { explicit try_to_lock_t() = default; };
struct adopt_lock_t { explicit adopt_lock_t() = default; };
inline constexpr defer_lock_t defer_lock{};
inline constexpr try_to_lock_t try_to_lock{};
inline constexpr adopt_lock_t adopt_lock{};
template <class M>
class lock_guard {
	M &__m;

public:
	typedef M mutex_type;
	explicit lock_guard(M &m) : __m(m) { __m.lock(); }
	lock_guard(M &m, adopt_lock_t) : __m(m) {}
	~lock_guard() { __m.unlock(); }
	lock_guard(const lock_guard &) = delete;
	lock_guard &operator=(const lock_guard &) = delete;
};
template <class M>
class unique_lock {
	M *__m;
	bool __owns;

public:
	typedef M mutex_type;
	unique_lock() noexcept : __m(nullptr), __owns(false) {}
	explicit unique_lock(M &m) : __m(&m), __owns(true) { m.lock(); }
	unique_lock(M &m, defer_lock_t) noexcept : __m(&m), __owns(false) {}
	unique_lock(M &m, try_to_lock_t) : __m(&m), __owns(m.try_lock()) {}
	unique_lock(M &m, adopt_lock_t) : __m(&m), __owns(true) {}
	unique_lock(const unique_lock &) = delete;
	unique_lock &operator=(const unique_lock &) = delete;
	unique_lock(unique_lock &&o) noexcept : __m(o.__m), __owns(o.__owns) {
		o.__m = nullptr;
		o.__owns = false;
	}
	unique_lock &operator=(unique_lock &&o) noexcept {
		if (__owns)
			__m->unlock();
		__m = o.__m;
		__owns = o.__owns;
		o.__m = nullptr;
		o.__owns = false;
		return *this;
	}
	~unique_lock() {
		if (__owns)
			__m->unlock();
	}
	void lock() {
		__m->lock();
		__owns = true;
	}
	bool try_lock() { return __owns = __m->try_lock(); }
	void unlock() {
		__m->unlock();
		__owns = false;
	}
	M *release() noexcept {
		M *m = __m;
		__m = nullptr;
		__owns = false;
		return m;
	}
	bool owns_lock() const noexcept { return __owns; }
	explicit operator bool() const noexcept { return __owns; }
	M *mutex() const noexcept { return __m; }
};
template <class M>
class scoped_lock {
	M &__m;

public:
	explicit scoped_lock(M &m) : __m(m) { __m.lock(); }
	~scoped_lock() { __m.unlock(); }
	scoped_lock(const scoped_lock &) = delete;
};
struct once_flag {
	pthread_once_t __o = PTHREAD_ONCE_INIT;
	bool __done = false;
	pthread_mutex_t __m = PTHREAD_MUTEX_INITIALIZER;
};
template <class F, class... A>
void call_once(once_flag &flag, F &&f, A &&...a) {
	pthread_mutex_lock(&flag.__m);
	if (!flag.__done) {
		f(static_cast<A &&>(a)...);
		flag.__done = true;
	}
	pthread_mutex_unlock(&flag.__m);
}
}
#endif
`
	sysHeaders["condition_variable"] = `#ifndef __CC_CONDITION_VARIABLE
#define __CC_CONDITION_VARIABLE
#include <mutex>
namespace std {
enum class cv_status { no_timeout, timeout };
class condition_variable {
	pthread_cond_t __c;

public:
	condition_variable() { pthread_cond_init(&__c, nullptr); }
	~condition_variable() { pthread_cond_destroy(&__c); }
	condition_variable(const condition_variable &) = delete;
	condition_variable &operator=(const condition_variable &) = delete;
	void notify_one() noexcept { pthread_cond_signal(&__c); }
	void notify_all() noexcept { pthread_cond_broadcast(&__c); }
	void wait(unique_lock<mutex> &lock) { pthread_cond_wait(&__c, lock.mutex()->native_handle()); }
	template <class P>
	void wait(unique_lock<mutex> &lock, P pred) {
		while (!pred())
			wait(lock);
	}
};
class condition_variable_any {
	pthread_cond_t __c;
	pthread_mutex_t __m;

public:
	condition_variable_any() {
		pthread_cond_init(&__c, nullptr);
		pthread_mutex_init(&__m, nullptr);
	}
	~condition_variable_any() {
		pthread_cond_destroy(&__c);
		pthread_mutex_destroy(&__m);
	}
	void notify_one() noexcept {
		pthread_mutex_lock(&__m);
		pthread_cond_signal(&__c);
		pthread_mutex_unlock(&__m);
	}
	void notify_all() noexcept {
		pthread_mutex_lock(&__m);
		pthread_cond_broadcast(&__c);
		pthread_mutex_unlock(&__m);
	}
	template <class L>
	void wait(L &lock) {
		pthread_mutex_lock(&__m);
		lock.unlock();
		pthread_cond_wait(&__c, &__m);
		pthread_mutex_unlock(&__m);
		lock.lock();
	}
	template <class L, class P>
	void wait(L &lock, P pred) {
		while (!pred())
			wait(lock);
	}
};
}
#endif
`
	sysHeaders["shared_mutex"] = `#ifndef __CC_SHARED_MUTEX
#define __CC_SHARED_MUTEX
#include <mutex>
namespace std {
class shared_mutex {
	pthread_rwlock_t __l;

public:
	shared_mutex() { pthread_rwlock_init(&__l, nullptr); }
	~shared_mutex() { pthread_rwlock_destroy(&__l); }
	shared_mutex(const shared_mutex &) = delete;
	shared_mutex &operator=(const shared_mutex &) = delete;
	void lock() { pthread_rwlock_wrlock(&__l); }
	bool try_lock() { return pthread_rwlock_trywrlock(&__l) == 0; }
	void unlock() { pthread_rwlock_unlock(&__l); }
	void lock_shared() { pthread_rwlock_rdlock(&__l); }
	bool try_lock_shared() { return pthread_rwlock_tryrdlock(&__l) == 0; }
	void unlock_shared() { pthread_rwlock_unlock(&__l); }
};
typedef shared_mutex shared_timed_mutex;
template <class M>
class shared_lock {
	M *__m;
	bool __owns;

public:
	shared_lock() noexcept : __m(nullptr), __owns(false) {}
	explicit shared_lock(M &m) : __m(&m), __owns(true) { m.lock_shared(); }
	shared_lock(M &m, defer_lock_t) noexcept : __m(&m), __owns(false) {}
	shared_lock(const shared_lock &) = delete;
	~shared_lock() {
		if (__owns)
			__m->unlock_shared();
	}
	void lock() {
		__m->lock_shared();
		__owns = true;
	}
	void unlock() {
		__m->unlock_shared();
		__owns = false;
	}
	bool owns_lock() const noexcept { return __owns; }
};
}
#endif
`
	sysHeaders["thread"] = `#ifndef __CC_THREAD
#define __CC_THREAD
#include <pthread.h>
#include <sched.h>
#include <unistd.h>
#include <utility>
namespace std {
class thread {
	pthread_t __t;
	bool __joinable;
	template <class L>
	static void *__run(void *p) {
		L *l = static_cast<L *>(p);
		(*l)();
		delete l;
		return nullptr;
	}
	template <class L>
	void __start(L l) {
		L *p = new L(l);
		pthread_create(&__t, nullptr, &__run<L>, p);
		__joinable = true;
	}

public:
	class id {
		pthread_t __id;
		bool __set;

	public:
		id() noexcept : __id(), __set(false) {}
		explicit id(pthread_t t) : __id(t), __set(true) {}
		bool operator==(const id &o) const noexcept { return __set == o.__set && (!__set || pthread_equal(__id, o.__id)); }
		bool operator!=(const id &o) const noexcept { return !(*this == o); }
	};
	typedef pthread_t native_handle_type;
	thread() noexcept : __t(), __joinable(false) {}
	template <class F, class... A>
	explicit thread(F f, A... a) : __t(), __joinable(false) {
		__start([=]() { f(a...); });
	}
	thread(const thread &) = delete;
	thread &operator=(const thread &) = delete;
	thread(thread &&o) noexcept : __t(o.__t), __joinable(o.__joinable) { o.__joinable = false; }
	thread &operator=(thread &&o) noexcept {
		__t = o.__t;
		__joinable = o.__joinable;
		o.__joinable = false;
		return *this;
	}
	~thread() {}
	bool joinable() const noexcept { return __joinable; }
	void join() {
		pthread_join(__t, nullptr);
		__joinable = false;
	}
	void detach() {
		pthread_detach(__t);
		__joinable = false;
	}
	id get_id() const noexcept { return __joinable ? id(__t) : id(); }
	native_handle_type native_handle() { return __t; }
	static unsigned hardware_concurrency() noexcept {
		long n = sysconf(_SC_NPROCESSORS_ONLN);
		return n > 0 ? unsigned(n) : 1;
	}
};
namespace this_thread {
inline void yield() noexcept { sched_yield(); }
inline thread::id get_id() noexcept { return thread::id(pthread_self()); }
}
}
#endif
`
	sysHeaders["typeinfo"] = `#ifndef __CC_TYPEINFO
#define __CC_TYPEINFO
#include <stddef.h>
namespace std {
class type_info {
	const char *__name;

public:
	explicit type_info(const char *n) : __name(n) {}
	type_info(const type_info &) = delete;
	const char *name() const noexcept { return __name; }
	size_t hash_code() const noexcept { return size_t(__name); }
	bool operator==(const type_info &o) const noexcept { return __name == o.__name; }
	bool operator!=(const type_info &o) const noexcept { return __name != o.__name; }
	bool before(const type_info &o) const noexcept { return __name < o.__name; }
};
}
#endif
`
}
