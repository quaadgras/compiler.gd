(module
  (memory (export "mem") 8192)
  (global $hp (mut i32) (i32.const 65536))

  (type $node (struct (field $val (mut i64)) (field $next (mut (ref null $node)))))
  (type $words (array (mut i64)))
  (type $bytes (array (mut i8)))
  (type $s4 (struct (field (mut i64)) (field (mut i64)) (field (mut i64)) (field (mut i64))))
  (type $s4arr (array (mut (ref null $s4))))

  (global $glist (mut (ref null $node)) (ref.null $node))
  (global $gwords (mut (ref null $words)) (ref.null $words))
  (global $gbytes (mut (ref null $bytes)) (ref.null $bytes))
  (global $gs4 (mut (ref null $s4arr)) (ref.null $s4arr))

  ;; ---------- A. linked list: build then traverse ----------
  ;; linear memory: node = {val i64 @0, next i64 @8}, bump allocated
  (func (export "lin_list_build") (param $n i32) (result i32)
    (local $head i32) (local $p i32)
    (loop $l
      (local.set $p (global.get $hp))
      (global.set $hp (i32.add (global.get $hp) (i32.const 16)))
      (i64.store (local.get $p) (i64.extend_i32_u (local.get $n)))
      (i64.store offset=8 (local.get $p) (i64.extend_i32_u (local.get $head)))
      (local.set $head (local.get $p))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (local.get $head))
  (func (export "lin_list_sum") (param $head i32) (param $reps i32) (result i64)
    (local $p i32) (local $sum i64)
    (loop $r
      (local.set $p (local.get $head))
      (loop $l
        (local.set $sum (i64.add (local.get $sum) (i64.load (local.get $p))))
        (local.set $p (i32.wrap_i64 (i64.load offset=8 (local.get $p))))
        (br_if $l (local.get $p)))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))
  (func (export "lin_reset") (global.set $hp (i32.const 65536)))

  (func (export "gc_list_build") (param $n i32)
    (local $head (ref null $node))
    (loop $l
      (local.set $head (struct.new $node (i64.extend_i32_u (local.get $n)) (local.get $head)))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (global.set $glist (local.get $head)))
  (func (export "gc_list_sum") (param $reps i32) (result i64)
    (local $p (ref null $node)) (local $sum i64)
    (loop $r
      (local.set $p (global.get $glist))
      (loop $l
        (local.set $sum (i64.add (local.get $sum) (struct.get $node $val (local.get $p))))
        (local.set $p (struct.get $node $next (local.get $p)))
        (br_if $l (i32.eqz (ref.is_null (local.get $p)))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))

  ;; ---------- B. []int64: fill and sum, with Go's bounds check ----------
  (func (export "lin_words_sum") (param $base i32) (param $len i32) (param $reps i32) (result i64)
    (local $i i32) (local $sum i64)
    (loop $r
      (local.set $i (i32.const 0))
      (loop $l
        (if (i32.ge_u (local.get $i) (local.get $len)) (then (unreachable)))
        (i64.store (i32.add (local.get $base) (i32.shl (local.get $i) (i32.const 3)))
          (i64.add (i64.load (i32.add (local.get $base) (i32.shl (local.get $i) (i32.const 3)))) (i64.const 1)))
        (local.set $sum (i64.add (local.get $sum) (i64.load (i32.add (local.get $base) (i32.shl (local.get $i) (i32.const 3))))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br_if $l (i32.lt_u (local.get $i) (local.get $len))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))
  (func (export "gc_words_new") (param $len i32) (global.set $gwords (array.new_default $words (local.get $len))))
  (func (export "gc_words_sum") (param $len i32) (param $reps i32) (result i64)
    (local $i i32) (local $sum i64) (local $a (ref null $words))
    (local.set $a (global.get $gwords))
    (loop $r
      (local.set $i (i32.const 0))
      (loop $l
        (array.set $words (local.get $a) (local.get $i)
          (i64.add (array.get $words (local.get $a) (local.get $i)) (i64.const 1)))
        (local.set $sum (i64.add (local.get $sum) (array.get $words (local.get $a) (local.get $i))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br_if $l (i32.lt_u (local.get $i) (local.get $len))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))

  ;; ---------- C. []struct{a,b,c,d int64}: p := &s[i]; p.b = p.a + p.c ----------
  ;; linear memory: 32-byte elements
  (func (export "lin_structs") (param $base i32) (param $len i32) (param $reps i32) (result i64)
    (local $i i32) (local $p i32) (local $sum i64)
    (loop $r
      (local.set $i (i32.const 0))
      (loop $l
        (local.set $p (i32.add (local.get $base) (i32.shl (local.get $i) (i32.const 5))))
        (i64.store offset=8 (local.get $p)
          (i64.add (i64.load (local.get $p)) (i64.add (i64.load offset=16 (local.get $p)) (i64.const 1))))
        (local.set $sum (i64.add (local.get $sum) (i64.load offset=8 (local.get $p))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br_if $l (i32.lt_u (local.get $i) (local.get $len))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))
  ;; wasm GC, flat: the slice is one i64 array and &s[i] is (array, index): what
  ;; interior pointers need
  (func (export "gc_flat_structs") (param $len i32) (param $reps i32) (result i64)
    (local $i i32) (local $pi i32) (local $sum i64) (local $a (ref null $words))
    (local.set $a (global.get $gwords))
    (loop $r
      (local.set $i (i32.const 0))
      (loop $l
        (local.set $pi (i32.shl (local.get $i) (i32.const 2)))
        (array.set $words (local.get $a) (i32.add (local.get $pi) (i32.const 1))
          (i64.add (array.get $words (local.get $a) (local.get $pi))
            (i64.add (array.get $words (local.get $a) (i32.add (local.get $pi) (i32.const 2))) (i64.const 1))))
        (local.set $sum (i64.add (local.get $sum) (array.get $words (local.get $a) (i32.add (local.get $pi) (i32.const 1)))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br_if $l (i32.lt_u (local.get $i) (local.get $len))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))
  ;; wasm GC, typed: an array of references to structs (each element boxed)
  (func (export "gc_boxed_new") (param $len i32)
    (local $i i32) (local $a (ref null $s4arr))
    (local.set $a (array.new_default $s4arr (local.get $len)))
    (loop $l
      (array.set $s4arr (local.get $a) (local.get $i) (struct.new_default $s4))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $l (i32.lt_u (local.get $i) (local.get $len))))
    (global.set $gs4 (local.get $a)))
  (func (export "gc_boxed_structs") (param $len i32) (param $reps i32) (result i64)
    (local $i i32) (local $p (ref null $s4)) (local $sum i64) (local $a (ref null $s4arr))
    (local.set $a (global.get $gs4))
    (loop $r
      (local.set $i (i32.const 0))
      (loop $l
        (local.set $p (array.get $s4arr (local.get $a) (local.get $i)))
        (struct.set $s4 1 (local.get $p)
          (i64.add (struct.get $s4 0 (local.get $p)) (i64.add (struct.get $s4 2 (local.get $p)) (i64.const 1))))
        (local.set $sum (i64.add (local.get $sum) (struct.get $s4 1 (local.get $p))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br_if $l (i32.lt_u (local.get $i) (local.get $len))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))

  ;; ---------- D. []byte: read a little-endian uint64 at every offset ----------
  (func (export "lin_bytes_u64") (param $base i32) (param $len i32) (param $reps i32) (result i64)
    (local $i i32) (local $sum i64)
    (loop $r
      (local.set $i (i32.const 0))
      (loop $l
        (local.set $sum (i64.add (local.get $sum) (i64.load align=1 (i32.add (local.get $base) (local.get $i)))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br_if $l (i32.lt_u (local.get $i) (i32.sub (local.get $len) (i32.const 8)))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))
  (func (export "gc_bytes_new") (param $len i32) (global.set $gbytes (array.new_default $bytes (local.get $len))))
  (func $rd (param $a (ref null $bytes)) (param $i i32) (result i64)
    (i64.extend_i32_u (array.get_u $bytes (local.get $a) (local.get $i))))
  (func (export "gc_bytes_u64") (param $len i32) (param $reps i32) (result i64)
    (local $i i32) (local $sum i64) (local $a (ref null $bytes))
    (local.set $a (global.get $gbytes))
    (loop $r
      (local.set $i (i32.const 0))
      (loop $l
        (local.set $sum (i64.add (local.get $sum)
          (i64.or
            (i64.or
              (i64.or (call $rd (local.get $a) (local.get $i))
                (i64.shl (call $rd (local.get $a) (i32.add (local.get $i) (i32.const 1))) (i64.const 8)))
              (i64.or (i64.shl (call $rd (local.get $a) (i32.add (local.get $i) (i32.const 2))) (i64.const 16))
                (i64.shl (call $rd (local.get $a) (i32.add (local.get $i) (i32.const 3))) (i64.const 24))))
            (i64.or
              (i64.or (i64.shl (call $rd (local.get $a) (i32.add (local.get $i) (i32.const 4))) (i64.const 32))
                (i64.shl (call $rd (local.get $a) (i32.add (local.get $i) (i32.const 5))) (i64.const 40)))
              (i64.or (i64.shl (call $rd (local.get $a) (i32.add (local.get $i) (i32.const 6))) (i64.const 48))
                (i64.shl (call $rd (local.get $a) (i32.add (local.get $i) (i32.const 7))) (i64.const 56)))))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br_if $l (i32.lt_u (local.get $i) (i32.sub (local.get $len) (i32.const 8)))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))

  ;; ---------- E. allocation of short-lived 16-byte objects ----------
  (func (export "gc_alloc_garbage") (param $n i32) (result i64)
    (local $p (ref null $node)) (local $sum i64)
    (loop $l
      (local.set $p (struct.new $node (i64.extend_i32_u (local.get $n)) (local.get $p)))
      (struct.set $node $next (local.get $p) (ref.null $node))
      (local.set $sum (i64.add (local.get $sum) (struct.get $node $val (local.get $p))))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (local.get $sum))

  ;; ---------- F. universal objects: what keeps interior pointers working ----------
  ;; Every Go object is {words: array i64, refs: array (ref null $obj)}; a pointer is
  ;; (object, word index). A pointer stored in memory has its object in refs[i] and
  ;; its index in words[i]. Node = {val, next}: 2 words.
  (type $refs (array (mut (ref null struct))))
  (type $obj (struct (field $w (ref $words)) (field $r (ref $refs))))
  (global $gobj (mut (ref null $obj)) (ref.null $obj))

  (func (export "uni_list_build") (param $n i32)
    (local $head (ref null $obj)) (local $o (ref null $obj))
    (loop $l
      (local.set $o (struct.new $obj (array.new_default $words (i32.const 2)) (array.new_default $refs (i32.const 2))))
      (array.set $words (struct.get $obj $w (local.get $o)) (i32.const 0) (i64.extend_i32_u (local.get $n)))
      ;; next = (head, 0)
      (array.set $refs (struct.get $obj $r (local.get $o)) (i32.const 1) (local.get $head))
      (array.set $words (struct.get $obj $w (local.get $o)) (i32.const 1) (i64.const 0))
      (local.set $head (local.get $o))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (global.set $gobj (local.get $head)))
  (func (export "uni_list_sum") (param $reps i32) (result i64)
    (local $o (ref null $obj)) (local $i i32) (local $sum i64) (local $next (ref null struct))
    (loop $r
      (local.set $o (global.get $gobj))
      (local.set $i (i32.const 0))
      (loop $l
        ;; sum += p.val
        (local.set $sum (i64.add (local.get $sum) (array.get $words (struct.get $obj $w (local.get $o)) (local.get $i))))
        ;; p = p.next: load the (object, index) pair stored at word i+1
        (local.set $next (array.get $refs (struct.get $obj $r (local.get $o)) (i32.add (local.get $i) (i32.const 1))))
        (local.set $i (i32.wrap_i64 (array.get $words (struct.get $obj $w (local.get $o)) (i32.add (local.get $i) (i32.const 1)))))
        (local.set $o (ref.cast (ref null $obj) (local.get $next)))
        (br_if $l (i32.eqz (ref.is_null (local.get $o)))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))
  (func (export "uni_alloc_garbage") (param $n i32) (result i64)
    (local $o (ref null $obj)) (local $sum i64)
    (loop $l
      (local.set $o (struct.new $obj (array.new_default $words (i32.const 2)) (array.new_default $refs (i32.const 2))))
      (array.set $words (struct.get $obj $w (local.get $o)) (i32.const 0) (i64.extend_i32_u (local.get $n)))
      (local.set $sum (i64.add (local.get $sum) (array.get $words (struct.get $obj $w (local.get $o)) (i32.const 0))))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (local.get $sum))

  ;; ---------- G. bytes: a sieve over []bool ----------
  (func (export "lin_sieve") (param $base i32) (param $len i32) (result i32)
    (local $i i32) (local $j i32) (local $n i32)
    (local.set $i (i32.const 2))
    (loop $outer
      (if (i32.eqz (i32.load8_u (i32.add (local.get $base) (local.get $i))))
        (then
          (local.set $n (i32.add (local.get $n) (i32.const 1)))
          (local.set $j (i32.shl (local.get $i) (i32.const 1)))
          (block $jd
            (loop $jl
              (br_if $jd (i32.ge_u (local.get $j) (local.get $len)))
              (i32.store8 (i32.add (local.get $base) (local.get $j)) (i32.const 1))
              (local.set $j (i32.add (local.get $j) (local.get $i)))
              (br $jl)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $outer (i32.lt_u (local.get $i) (local.get $len))))
    (local.get $n))
  ;; a wasm GC i8 array
  (func (export "gc_sieve") (param $len i32) (result i32)
    (local $i i32) (local $j i32) (local $n i32) (local $a (ref null $bytes))
    (local.set $a (array.new_default $bytes (local.get $len)))
    (local.set $i (i32.const 2))
    (loop $outer
      (if (i32.eqz (array.get_u $bytes (local.get $a) (local.get $i)))
        (then
          (local.set $n (i32.add (local.get $n) (i32.const 1)))
          (local.set $j (i32.shl (local.get $i) (i32.const 1)))
          (block $jd
            (loop $jl
              (br_if $jd (i32.ge_u (local.get $j) (local.get $len)))
              (array.set $bytes (local.get $a) (local.get $j) (i32.const 1))
              (local.set $j (i32.add (local.get $j) (local.get $i)))
              (br $jl)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $outer (i32.lt_u (local.get $i) (local.get $len))))
    (local.get $n))
  ;; bytes packed 8 to a word of an i64 array (the universal object's words)
  (func (export "packed_sieve") (param $len i32) (result i32)
    (local $i i32) (local $j i32) (local $n i32) (local $a (ref null $words)) (local $w i32)
    (local.set $a (array.new_default $words (i32.add (i32.shr_u (local.get $len) (i32.const 3)) (i32.const 1))))
    (local.set $i (i32.const 2))
    (loop $outer
      (if (i64.eqz (i64.and (i64.const 255)
            (i64.shr_u (array.get $words (local.get $a) (i32.shr_u (local.get $i) (i32.const 3)))
              (i64.extend_i32_u (i32.shl (i32.and (local.get $i) (i32.const 7)) (i32.const 3))))))
        (then
          (local.set $n (i32.add (local.get $n) (i32.const 1)))
          (local.set $j (i32.shl (local.get $i) (i32.const 1)))
          (block $jd
            (loop $jl
              (br_if $jd (i32.ge_u (local.get $j) (local.get $len)))
              (local.set $w (i32.shr_u (local.get $j) (i32.const 3)))
              (array.set $words (local.get $a) (local.get $w)
                (i64.or
                  (i64.and (array.get $words (local.get $a) (local.get $w))
                    (i64.xor (i64.const -1) (i64.shl (i64.const 255) (i64.extend_i32_u (i32.shl (i32.and (local.get $j) (i32.const 7)) (i32.const 3))))))
                  (i64.shl (i64.const 1) (i64.extend_i32_u (i32.shl (i32.and (local.get $j) (i32.const 7)) (i32.const 3))))))
              (local.set $j (i32.add (local.get $j) (local.get $i)))
              (br $jl)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $outer (i32.lt_u (local.get $i) (local.get $len))))
    (local.get $n))

  ;; ---------- H. universal objects, leaner ----------
  ;; As F, but the reference array is typed (a recursive type), so loading a
  ;; pointer needs no cast, and offsets are byte offsets as a tagged pointer
  ;; would carry: word index = offset >> 3.
  (rec
    (type $refs2 (array (mut (ref null $obj2))))
    (type $obj2 (struct (field $w2 (ref $words)) (field $r2 (ref $refs2)))))
  (global $gobj2 (mut (ref null $obj2)) (ref.null $obj2))

  (func (export "uni2_list_build") (param $n i32)
    (local $head (ref null $obj2)) (local $o (ref null $obj2))
    (loop $l
      (local.set $o (struct.new $obj2 (array.new_default $words (i32.const 2)) (array.new_default $refs2 (i32.const 2))))
      (array.set $words (struct.get $obj2 $w2 (local.get $o)) (i32.const 0) (i64.extend_i32_u (local.get $n)))
      (array.set $refs2 (struct.get $obj2 $r2 (local.get $o)) (i32.const 1) (local.get $head))
      (array.set $words (struct.get $obj2 $w2 (local.get $o)) (i32.const 1) (i64.const 0))
      (local.set $head (local.get $o))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (global.set $gobj2 (local.get $head)))
  (func (export "uni2_list_sum") (param $reps i32) (result i64)
    (local $o (ref null $obj2)) (local $off i32) (local $sum i64) (local $w (ref null $words)) (local $i i32)
    (loop $r
      (local.set $o (global.get $gobj2))
      (local.set $off (i32.const 0))
      (loop $l
        (local.set $w (struct.get $obj2 $w2 (local.get $o)))
        (local.set $i (i32.shr_u (local.get $off) (i32.const 3)))
        ;; sum += p.val
        (local.set $sum (i64.add (local.get $sum) (array.get $words (local.get $w) (local.get $i))))
        ;; p = p.next
        (local.set $off (i32.wrap_i64 (array.get $words (local.get $w) (i32.add (local.get $i) (i32.const 1)))))
        (local.set $o (array.get $refs2 (struct.get $obj2 $r2 (local.get $o)) (i32.add (local.get $i) (i32.const 1))))
        (br_if $l (i32.eqz (ref.is_null (local.get $o)))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))

  ;; I. one array per object: scalars and references interleaved in an array of
  ;; anyref is not possible for i64, so the other lean option is a pointer that
  ;; carries both arrays: (words, refs, offset). A stored pointer then takes two
  ;; reference slots.
  (type $anyrefs (array (mut eqref)))
  (global $gw3 (mut (ref null $words)) (ref.null $words))
  (global $gr3 (mut (ref null $anyrefs)) (ref.null $anyrefs))
  (func (export "uni3_list_build") (param $n i32)
    (local $hw (ref null $words)) (local $hr (ref null $anyrefs)) (local $w (ref null $words)) (local $r (ref null $anyrefs))
    (loop $l
      (local.set $w (array.new_default $words (i32.const 2)))
      (local.set $r (array.new_default $anyrefs (i32.const 2)))
      (array.set $words (local.get $w) (i32.const 0) (i64.extend_i32_u (local.get $n)))
      (array.set $anyrefs (local.get $r) (i32.const 0) (local.get $hw))
      (array.set $anyrefs (local.get $r) (i32.const 1) (local.get $hr))
      (local.set $hw (local.get $w))
      (local.set $hr (local.get $r))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (global.set $gw3 (local.get $hw))
    (global.set $gr3 (local.get $hr)))
  (func (export "uni3_list_sum") (param $reps i32) (result i64)
    (local $w (ref null $words)) (local $r (ref null $anyrefs)) (local $sum i64) (local $nw eqref)
    (loop $rr
      (local.set $w (global.get $gw3))
      (local.set $r (global.get $gr3))
      (loop $l
        (local.set $sum (i64.add (local.get $sum) (array.get $words (local.get $w) (i32.const 0))))
        (local.set $nw (array.get $anyrefs (local.get $r) (i32.const 0)))
        (local.set $r (ref.cast (ref null $anyrefs) (array.get $anyrefs (local.get $r) (i32.const 1))))
        (local.set $w (ref.cast (ref null $words) (local.get $nw)))
        (br_if $l (i32.eqz (ref.is_null (local.get $w)))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $rr (local.get $reps)))
    (local.get $sum))

  ;; ---------- J. typed objects with tagged pointers ----------
  ;; One wasm struct per Go object. A pointer is (reference, tag): tag 0 means it
  ;; points at a whole object of its static type, so a dereference is a tag
  ;; check, a cast and a field access; any other tag is an interior pointer
  ;; (field and byte) and takes a slow path, a call through a per-field accessor.
  (type $tnode (struct (field $tval (mut i64)) (field $tnext (mut (ref null struct))) (field $tnexttag (mut i32))))
  (type $getter (func (param (ref null struct)) (param i32) (result i64)))
  (table $accessors 4 funcref)
  (func $slow_i64 (type $getter)
    ;; field 0 of a $tnode, reached through an interior pointer
    (struct.get $tnode $tval (ref.cast (ref null $tnode) (local.get 0))))
  (elem (i32.const 1) $slow_i64)
  (global $gt (mut (ref null $tnode)) (ref.null $tnode))

  (func (export "tag_list_build") (param $n i32)
    (local $head (ref null $tnode))
    (loop $l
      (local.set $head (struct.new $tnode (i64.extend_i32_u (local.get $n)) (local.get $head) (i32.const 0)))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (global.set $gt (local.get $head)))
  ;; every pointer is a whole-object pointer: the fast path
  (func (export "tag_list_sum") (param $reps i32) (result i64)
    (local $p (ref null struct)) (local $tag i32) (local $sum i64) (local $n (ref null $tnode))
    (loop $r
      (local.set $p (global.get $gt))
      (local.set $tag (i32.const 0))
      (loop $l
        (if (local.get $tag) (then (unreachable)))
        (local.set $n (ref.cast (ref null $tnode) (local.get $p)))
        (local.set $sum (i64.add (local.get $sum) (struct.get $tnode $tval (local.get $n))))
        (local.set $tag (struct.get $tnode $tnexttag (local.get $n)))
        (local.set $p (struct.get $tnode $tnext (local.get $n)))
        (br_if $l (i32.eqz (ref.is_null (local.get $p)))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))
  ;; the value is read through an interior pointer (&node.val): the slow path
  (func (export "tag_list_sum_interior") (param $reps i32) (result i64)
    (local $p (ref null struct)) (local $sum i64) (local $n (ref null $tnode))
    (loop $r
      (local.set $p (global.get $gt))
      (loop $l
        (local.set $sum (i64.add (local.get $sum)
          (call_indirect $accessors (type $getter) (local.get $p) (i32.const 0) (i32.const 1))))
        (local.set $n (ref.cast (ref null $tnode) (local.get $p)))
        (local.set $p (struct.get $tnode $tnext (local.get $n)))
        (br_if $l (i32.eqz (ref.is_null (local.get $p)))))
      (local.set $reps (i32.sub (local.get $reps) (i32.const 1)))
      (br_if $r (local.get $reps)))
    (local.get $sum))
)
