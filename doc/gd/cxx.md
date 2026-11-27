# C++ in cmd/cc: the plan, and what is there

`cmd/cc` compiles C++ as it compiles C (cc.md): to Go, 1:1, a class's
object a Go variable and its pointers Go's. C++ is added to the C
front end, not beside it: the parser lowers each C++ construct to the
typed C tree it already builds (ast.go), as cfront did, so the generator
and the run-time support stay C's. A file is C++ by its name (`.cc`,
`.cpp`, `.cxx`, `.c++`, `.C`).

The work is driven by one program: Godot (C++17, built with
`-fno-exceptions`, its own containers, little of the standard library).
What Godot does not use waits.

## Why Godot sets the order

Godot's core (`core/`, 200,000 lines) uses of the standard library only
`<type_traits>` (54 `enable_if_t`, 51 `is_base_of_v`, the `is_trivially_*`
family), `<initializer_list>`, `<atomic>`, `<new>`, `<utility>`
(`move`, `forward`), `<functional>` (7 `std::function`), the threads'
headers, and C's headers by their `<c...>` names. None of it needs
libstdc++ or libc++, which need all of the language before `<vector>`
compiles: cmd/cc has headers of its own for those few, written for it,
with the traits as compiler built-ins.

Of the language it uses, by weight: templates (900 in core, 226 of them
variadic, 190 explicit specializations), `constexpr` (960, 152 `if
constexpr`), virtual functions (1700), operators (1700),
`reinterpret_cast` (1750, most of them Variant's storage), placement
`new` (every object: `memnew`), range `for` (560), lambdas, `thread_local`,
and no exceptions, 4 `dynamic_cast` and 2 `typeid`.

So the order is:

1. **C with classes** (this document's first part): references,
   overloading, namespaces, classes with member functions, constructors
   and destructors, `new` and `delete`, operators, default arguments.
2. **Inheritance and virtual functions**, static locals with dynamic
   initialization, member pointers.
3. **Templates**: classes and functions, specializations, deduction,
   packs, SFINAE, `if constexpr`, the traits' built-ins; then lambdas and
   `constexpr` evaluation.
4. Godot's `core/typedefs.h`, `core/math`, `core/templates`,
   `core/string`, ... file by file, each one that compiles kept compiling
   by a test.
5. Exceptions and RTTI, when something needs them.

Steps 1 to 3 are largely done, and step 4 has begun (Status, below).

## The lowering

- **A class is a record.** Its data members are fields; its member
  functions are functions whose first parameter is `this`; its static
  members are objects and functions with the class's name in theirs. A
  class with no C++ in it is the C struct, byte for byte.
- **A reference is a pointer** in the tree (a pointer type marked as a
  reference). No expression has reference type: the parser dereferences
  a reference where it is named, and takes the address where one is
  bound. In memory and in registers it is C's pointer, which is Go's.
- **Names with C++ linkage are mangled** as the Itanium ABI mangles them
  (`_ZN3Foo3barEi`), and the mangled name is the symbol's name from the
  parser on: units link by it as C's link by theirs. `extern "C"` names,
  and what cmd/cc's own C headers declare, are not mangled.
- **Overloads** are a list of symbols under one name in a scope; a call
  parses its arguments, then chooses (exact match, promotion, conversion,
  user-defined conversion, ellipsis; ties by reference binding and
  qualification).
- **Scopes** are C's, with a kind: a namespace's is kept and reopened, a
  class's is its members', searched after a member function's block
  scopes and before the namespace's. A member function defined in its
  class is parsed when the class is complete: its tokens are kept.
- **Constructors and destructors** are functions (`C1`, `D1`). A local of
  a class with a destructor uses the tree's `cleanup` (GNU's attribute,
  which is the same thing): the generator already runs it wherever the
  block is left. A constructor's member initializers, default member
  initializers and members' constructors are statements before its body;
  a destructor's members' destructors are a `defer` (C2y's, already
  there) around its body.
- **A class prvalue** (`T(1, 2)`, a call that returns a class with a
  constructor) is not a value in the tree but a recipe: given the object
  to initialize, it makes the expression that initializes it, as C++17
  defines one. `T a = f();` constructs `a` in place; where no object
  takes it, the parser makes a temporary, destroyed when its full
  expression ends (a statement expression around it).
- **Passing and returning classes.** The generator already passes a
  struct by the pointer to its memory and returns one into memory the
  caller gives. For a class with a copy constructor or a destructor that
  is not trivial the callee uses the caller's object instead of a copy of
  its bytes (the Itanium ABI's invisible reference), the caller destroys
  it, and a `return` constructs into the caller's memory.
- **Virtual functions**: a hidden first field, the pointer to the class's
  table, a C array of function pointers (so a call through it is C's
  checked call through a function pointer). Bases are leading fields,
  found as C's anonymous members are found.
- **Templates** are instantiated by parsing their tokens again with their
  parameters bound, in the scope they were declared in: cmd/cc's parser
  types as it parses, so a template's body means nothing to it until its
  arguments are known. An instance is kept by its arguments. (Names are
  therefore all looked up at instantiation: two-phase lookup's
  differences are not there.)

## C++ in the 1:1 translation

What cc.md says of C holds: a class's object is a Go variable of the
struct that mirrors it. What C++ adds:

- **`new T`** allocates as `malloc` does when its result is converted
  to `T *`: a Go allocation of T's type, whose pointer fields the
  collector sees. `delete` runs the destructor; like `free` it frees
  nothing (the collector does). `new T[n]` of a class with a destructor
  keeps n in the 8 bytes in front of the array, which `delete[]` reads.
- **A pointer to a base** is a pointer into the object, and a downcast
  is arithmetic on the offset, as in C++: `unsafe.Add`, which the
  compiler checks stays in the object (unsafe.md).
- **The table pointer** is a pointer field like another, to a Go
  variable of the class's table, whose entries are descriptors of
  functions (`c23.Func`).
- **Placement `new` and allocation functions of one's own are the open
  question.** Memory from `malloc` that is not converted to an object
  type is untyped (`c23.AllocRaw`): words that may each hold a pointer, where every
  pointer goes through a side table. An object placed there works, but
  slowly, and Go sees only bytes. Godot allocates every object so
  (`memnew` is placement `new` over `Memory::alloc_static`, which offsets
  `malloc`'s result by a header). Three ways out, to choose between when
  `core/os/memory.cpp` compiles: (a) leave it (correct, slow); (b) let an
  allocation function declared so (an attribute) take the type of the
  `new` expression that calls it, and allocate typed; (c) give Godot's
  `Memory` a cmd/cc implementation. The collector's bitmap for an object
  is fixed when it is allocated, so untyped memory cannot become typed
  later; the type has to arrive with the allocation.
- **Unions with class members and `reinterpret_cast`** (Variant's
  storage) are C's unions and casts: split words.
- **Go sees classes as it sees structs** (`C.struct_node`): through
  `extern "C"` functions for now; C++ names are not Go's.

## What is not planned

Two-phase lookup, access checking as an error (access specifiers are
parsed; nothing is rejected for them), modules, coroutines, concepts
beyond what parses, and ABI compatibility with objects GCC or clang
compiled: the mangled names are the Itanium ABI's, the calls are Go's.

## Status

`go test cmd/cc -run Cxx` compiles `testdata/cxx/*.cc` with cmd/cc and
with g++ and compares what they print (50 programs). Against Godot
(4.7.1, with its own defines and include directories, and `__GNUC__`
defined: Godot expects its compiler to be GCC, clang or MSVC), 176 of
the 177 `.cpp` files of `core` translate, each alone, and the 176
together, as one program of 1.2 million lines of Go (1.9 million before
the translation was 1:1): in 30 seconds on 32 threads (0.4 seconds from
`cc -cache` when nothing it read has changed), which the Go compiler
then builds in a minute and a half, into a program of 82 MB (65 MB
without what a debugger reads). The other,
`core/math/geometry_2d.cpp`, includes clipper2, which uses `<vector>`,
`<string>`, `<queue>` and `<iostream>`.

Translated together with `cc -undef` (a call of a function that no file
defines panics, so a part of a program can be built: core calls 1,300
functions of the scene and the servers), that program runs. Core's
objects of static duration are constructed, and a test prints the same
41 lines as it does linked with g++'s build of core: what the vectors, a
Basis, a Quaternion, a Transform3D and a Color compute; String
(concatenation, searching, splitting, numbers, UTF-8, `vformat`, paths,
hashes); Vector, LocalVector, HashMap, HashSet, RBMap and List; and
Variant, with Array and Dictionary, converted and printed. A second
test sets core up as the engine does (`register_core_types`, with an OS
that does nothing) and prints the same as with g++ too: the 106 classes
that core registers, an object made by its class's name and reached
through `Ref`, methods called by name through ClassDB with Variants,
metadata, and a signal that is declared, connected to a bound Callable
and emitted. The whole run, core's static initialization
included, takes 8 ms, where g++'s build takes 3 ms and the translation
with capabilities took 76 ms to start.

What is there, by step of the order above:

1. **C with classes**: references (binding to temporaries, lifetime
   extension), overloaded functions and default arguments, namespaces,
   using declarations, directives and aliases, `extern "C"`, scoped
   enumerations, the named casts and `T(x)`, `auto`, argument-dependent
   lookup; classes with default member initializers, static members,
   `mutable`, constructors with member initializers and delegation,
   destructors, conversion functions, `explicit`, `= default` and
   `= delete`, friends, the implicit special members; temporaries
   destroyed at the end of their full expression, classes passed and
   returned in the caller's memory, copy elision and the named return
   value optimization, overloaded operators, `new`, `delete` and
   allocation functions of the program's own; braced lists as
   initializers, arguments and results, with `std::initializer_list`;
   range-based `for`; lambdas (captures by value and reference, `this`,
   initialized captures, nested, converted to function pointers);
   `wchar_t`, `char16_t` and `char32_t` as types of their own; raw
   strings; a conditional of class type; aggregates with members that
   have constructors.
2. **Inheritance and virtual functions**: bases as leading fields with
   no name, empty bases that take no room, conversions to a base and
   back; tables of virtual functions as C arrays of function pointers,
   virtual and deleting destructors, pure virtual functions; static
   locals initialized the first time they are reached; `bool` as the
   type of comparisons; `new T[n]{...}` and `delete[]`; pointers to
   members (a data member's is its offset, a member function's two
   words, a virtual one's through a function that makes the virtual
   call); `typeid` of a static type.
3. **Templates**: class, function, alias and variable templates, full
   and partial specializations, members defined outside their class,
   member templates, value parameters, parameter packs with expansions
   and fold expressions, deduction (forwarding references, arrays'
   lengths, classes derived from a template's instance, pointers to
   members), class template argument deduction from constructors,
   SFINAE, `if constexpr`, the type traits as built-ins, functions as
   arguments of pointer-to-function parameters, partial ordering of
   function templates. Instances are made when needed: a class when it
   must be complete, a function's body (any inline function's, a
   template's or not) when it is used.
4. **The library**: `<type_traits>`, `<utility>`, `<initializer_list>`,
   `<new>`, `<atomic>`, `<limits>`, `<functional>`, `<mutex>`,
   `<condition_variable>`, `<shared_mutex>`, `<thread>`, `<typeinfo>`,
   `<array>`, `<algorithm>` (min, max, clamp, sort and the simple
   algorithms on iterators), `<cmath>` with its overloads, and the
   other `<c...>` headers.

Not there yet:

- **constexpr evaluation**: a call of a constexpr function with
  constants is a constant if the function is no member and computes
  with integers (a function template's instance too): the width of a
  bit-field, the length of an array, a template's argument. A member
  function's call, one that computes with floating point or with
  objects, is not: a constexpr object initialized with one is
  initialized when the program starts, and a `static_assert` that
  needs one is not checked.
- virtual base classes, `dynamic_cast`, run-time type information
  (a class may have several base classes with virtual functions: each
  after the first has its own table in the object, whose entries for
  the functions the class overrides are thunks);
- exceptions (Godot is built without them);
- implicit move constructors and move assignment (the copy is used),
  destructors of objects of static duration at exit, thread-safe
  initialization of static locals;
- generic lambdas, structured bindings, user-defined literals,
  deduction guides, member templates of class templates defined outside
  both, designated initializers and brace elision for aggregates with
  members that have constructors;
- the containers and streams of the standard library (`<vector>`,
  `<string>`, `<iostream>`): Godot's own code does not use them, some
  of its third-party libraries do;
- access checking, which is not planned.

`CCDEBUG=sfinae` prints the errors that are not reported because they
are substitution failures; `CCDEBUG=panic` makes a parse error a panic,
with the parser's stack.
