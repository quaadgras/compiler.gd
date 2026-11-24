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
}
