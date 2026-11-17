// layout.h: the structs of layout.go, as C. See check.sh.
#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>

struct Ptrs { int *a; void *b; struct Ptrs *next; };
struct Mixed { void *p; long long i64; int i; bool b; double f; char c; };
struct Ints { char c; int i; short s; long long l; unsigned u; uintptr_t up; };
struct Floats { float f32; double f64; float f32b; };
struct Inner { char c; long long l; };
struct Nested { bool b; struct Inner in; int *p; struct Inner arr[2]; };
struct Arrays { char c[3]; int i[5]; void *p[2]; long long l[1]; bool z; };
struct PtrThenI64 { void *p; long long l; };
struct I64ThenPtr { long long l; void *p; char c; };
struct Bools { bool a; bool b; void *p; bool c; };
struct Deep { struct Nested n; struct Mixed m; struct Ptrs ps[3]; double d; };
struct Small { short a; char b; };
struct WithSmall { char c; struct Small s; int i; struct Small t[3]; };
// A Go slice is { ptr, len, cap }, a Go string { ptr, hash, len }:
// three words in both.
struct GoSliceLike { void *data; int len; int cap; };
struct HasSlice { char c; struct GoSliceLike s; double d; };
