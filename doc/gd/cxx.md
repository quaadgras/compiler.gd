# C++ in cmd/cc: the plan, and what is there

`cmd/cc` compiles C++ as it compiles C (cc.md): to Go, on the memory
rules of internal/c23, with typed capabilities. C++ is added to the C
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

## The lowering

- **A class is a record.** Its data members are fields; its member
  functions are functions whose first parameter is `this`; its static
  members are objects and functions with the class's name in theirs. A
  class with no C++ in it is the C struct, byte for byte.
- **A reference is a pointer** in the tree (a pointer type marked as a
  reference). No expression has reference type: the parser dereferences
  a reference where it is named, and takes the address where one is
  bound. In memory and in registers it is C's pointer, so it has a
  capability as any pointer has.
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

## C++ on typed capabilities

What cc.md says of C holds: a class's object is a C object with its
type, checked the same way. What C++ adds:

- **`new T`** makes a C object of type `T`, as `malloc` does when its
  result is converted to `T *`: typed, so its pointer fields are pointer
  words that Go and the collector see, and no side table is used.
  `delete` runs the destructor; like `free` it frees nothing (the
  collector does) and faults for a pointer `new` did not return.
  `delete[]` needs no count kept in front of the array: the capability has
  the array's exact size.
- **A pointer to a base** is a pointer into the object, and a word that
  points into a C object gets the whole object's capability back. So a
  downcast is arithmetic on the offset, as in C++, and a wrong one cannot
  reach outside the object the pointer came from, nor read a pointer
  where the object has none: type confusion is confined to one object's
  scalars.
- **The table pointer** is a pointer field like another: only a pointer
  store writes it, and it points to a C global, so a call through a
  forged or stale one fails C's function pointer check.
- **Placement `new` and allocation functions of one's own are the open
  question.** Memory from `malloc` that is not converted to an object
  type is untyped (`c23.Raw`): bytes to Go, with split words, where every
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
with g++ and compares what they print.

There (step 1 of the order above):

- references (`&`, `&&`, binding to temporaries, lifetime extension),
  overloaded functions and default arguments, namespaces (nested,
  inline, unnamed), using declarations, directives and aliases,
  `extern "C"`, scoped enumerations, the named casts and `T(x)`, `auto`
  with declarators, argument-dependent lookup for calls and operators;
- classes: data members with default member initializers, member
  functions defined in or out of the class, `static` members, `mutable`,
  constructors with member initializers and delegation, destructors,
  conversion functions, `explicit`, `= default` and `= delete`, friends,
  the implicit default and copy constructors, copy assignment and
  destructor (member by member where a member needs it, the bytes
  otherwise);
- objects: temporaries destroyed at the end of their full expression
  (conditionally made ones too), classes passed and returned in the
  caller's memory, copy elision and the named return value
  optimization, overloaded operators (members and not), `new`,
  `new[]`, placement `new`, `delete`, and allocation functions of the
  program's own.

Not there yet, of step 1: static locals with constructors, `delete[]`
of objects with destructors, braces for aggregates whose members have
copy constructors, comparisons of type `bool` (they are `int`, as in C,
which only overload resolution and `auto` can tell), move construction
by implicit members (they copy), and access checking, which is not
planned. Then steps 2 to 5.
