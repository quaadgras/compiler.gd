# Units: files of a package that are compiled by themselves

A Go package is compiled as one thing: a change to one of its files
compiles all of them again. That is the right cost for packages people
write. It is the wrong one for a package that `cmd/cc` writes from a
large C or C++ program, where the package is the whole program: Godot's
core alone is 60 MB of Go that took a minute and a half to compile, for
any change.

A *unit* is a file of a package that the compiler compiles by itself and
whose object the go command's cache keeps. `cmd/cc -units` writes one
for each C file ([cc.md](cc.md)). A build compiles the units that
changed, several at a time, and takes the objects of the rest from the
cache.

| Godot (files) | one package | units, from nothing | units, one file changed |
|---|---|---|---|
| core (178): translate | 28 s, 15 GB | 22 s | 1 s |
| core (178): compile and link | 86 s | 70 s | 4 to 7 s |
| core and servers (311): translate | 128 s | 75 s | |
| core and servers (311): compile and link | 203 s | 96 s | |
| core, servers and scene (649): translate | (out of memory) | 100 s | |
| core, servers and scene (649): compile and link | | 167 s | |

(On 32 cores, 2026-11-28. The one-package numbers are of the same day's
toolchain.)

## What a unit is

A file is a unit if the line `//go:unit` is the first of the file, or
the second after one line of comment (`// Code generated ...`). The rest
of the package's files, at least one, are compiled together as ever: the
*root*.

The compiler compiles a unit as a package of one file (`-unit`), with
these differences:

- **Its functions and variables are the package's, by name.** A function
  the unit declares without a body, or a variable it declares, may be
  defined by another unit, or by the root; the linker binds the name.
  A variable that several units declare is one variable: the linker
  keeps the declaration that has data, or one of those that have none.
  A function that two units define is an error, unless both say
  `//go:dupok`: then each unit may inline its own and the linker keeps
  one. That is C++'s rule for an inline function, and what the
  directive is for.
- **Its types are the package's, by name.** Every unit declares the
  types it uses; the units' declarations of a name must be the same,
  and are one type to the program. A struct declared as
  `struct{ _ c23.Incomplete }` is a type the unit has only the name of
  (a class that the C file only declares): the unit may have pointers
  to it, and the unit that declares the struct in full, or the root,
  gives the type.
- **It initializes nothing.** A unit may not have an `init` function or
  a variable whose value Go computes when the package starts. What a
  unit has to do first is in functions that the root calls.
- **Nothing is known of what it does not define**: a call to another
  unit's function is not inlined, and what is passed to it escapes.
  Only what a unit defines itself, which `//go:dupok` functions are, is
  inlined and analysed. This is what separate compilation costs: Godot's
  core benchmark runs 17% more instructions as units than as one
  package (11.1G for 9.5G).

The object of a unit depends on that file, the compiler and its flags,
and the build ids of the packages that may be imported: under a key of
those it is kept in `$GOCACHE/gd-units`, which `go clean -cache`
removes. The units' objects follow the root's in the package's archive.
`-gcflags=-d=units=1` prints how many were compiled.

Nothing in the go command changes: it sees a package whose compilation
is fast when little has changed.

## Why not packages

The same could be had by making each C file a Go package, with
`//go:linkname` for the references that packages may not have (C files
refer to each other in circles). Two hand-written packages were linked
that way first, and it works. It needs the same of `cmd/cc`, and of the
compiler only `//go:dupok`; but every type would be a different Go type
in each package, which the run-time checks of C's memory compare by
their identity before anything else (what the other comparison would
cost was not measured), and the Go would be full of directives that say
what the package clause already says here.

## What is not done

- A unit's dependencies are the build ids of everything the package may
  import, not of what the unit imports: a change to any of them compiles
  every unit.
- The cache is trimmed by size alone: when the objects take more than
  10 GB (`$GDUNITCACHE`, in megabytes), those used longest ago are
  removed.
- Escape analysis and inlining across units (summaries of what a unit's
  functions do with their arguments, kept with its object) would take
  back most of what separate compilation costs.
