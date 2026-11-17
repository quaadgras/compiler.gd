// layout.h: C declarations whose sizes, alignments and member offsets
// cmd/cc and clang --target=wasm32 must agree on (check.sh), and Go too
// for those layout.go declares.
#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>
#include <stdarg.h>

enum Small { SA, SB = 1000 };
enum Big { BA, BB = 0x100000000 };

struct Scalars { char c; short s; int i; long l; long long ll; float f; double d; void *p; size_t z; ptrdiff_t pd; intptr_t ip; uintptr_t up; bool b; wchar_t w; };
struct LongFirst { long l; char c; };
struct LLAfterChar { char c; long long ll; };
struct DoubleAfterInt { int i; double d; };
struct Ptrs { int *a; void *b; struct Ptrs *next; void (*fn)(int); };
struct Longs { long a; unsigned long b; long c[3]; char d; };
struct Nested { char c; struct LLAfterChar in; long l; struct Ptrs p; };
struct Arrays { char c[3]; long l[5]; void *p[2]; double d[2]; short s; };
struct Fixed { int8_t a; int64_t b; int_fast16_t c; int_fast32_t d; intmax_t e; uint64_t f; int_least64_t g; };
union U { long l; double d; void *p; char c[5]; };
struct WithUnion { char c; union U u; int i; };
struct Bits { unsigned a : 3; unsigned b : 5; long c : 7; long long d : 40; char e; int f : 20; };
struct LDouble { char c; long double ld; };
struct Int128 { char c; __int128 x; };
struct Enums { char c; enum Small s; char d; enum Big b; };
struct VaList { char c; va_list ap; };
struct Complex { char c; _Complex double z; _Complex float w; };
struct Flex { int n; long data[]; };
struct Aligned { char c; _Alignas(16) int x; };
struct __attribute__((packed)) Packed { char c; long l; void *p; };
struct MaxAlign { char c; max_align_t m; };
