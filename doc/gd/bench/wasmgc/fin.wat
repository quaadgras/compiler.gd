(module
  (type $handle (struct (field $addr (mut i32))))
  (import "env" "register" (func $register (param (ref $handle)) (param i32)))
  (import "env" "get" (func $get (param externref) (param i32) (result i32)))
  (memory (export "mem") 16)
  ;; a buffer handle: the wasm GC object that owns a block of linear memory
  (func (export "new_handle") (param $addr i32) (result (ref $handle))
    (struct.new $handle (local.get $addr)))
  ;; allocate n handles, registering each for finalization, keep none
  (func (export "alloc_registered") (param $n i32)
    (loop $l
      (call $register (struct.new $handle (local.get $n)) (local.get $n))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n))))
  (func (export "alloc_plain") (param $n i32) (result i32)
    (local $h (ref null $handle)) (local $s i32)
    (loop $l
      (local.set $h (struct.new $handle (local.get $n)))
      (local.set $s (i32.add (local.get $s) (struct.get $handle $addr (local.get $h))))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br_if $l (local.get $n)))
    (local.get $s))
  ;; sum a Uint8Array held as an externref, one JS call per element
  (func (export "sum_js_buffer") (param $buf externref) (param $len i32) (result i32)
    (local $i i32) (local $s i32)
    (loop $l
      (local.set $s (i32.add (local.get $s) (call $get (local.get $buf) (local.get $i))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $l (i32.lt_u (local.get $i) (local.get $len))))
    (local.get $s))
  ;; the same bytes in linear memory
  (func (export "sum_linear") (param $base i32) (param $len i32) (result i32)
    (local $i i32) (local $s i32)
    (loop $l
      (local.set $s (i32.add (local.get $s) (i32.load8_u (i32.add (local.get $base) (local.get $i)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $l (i32.lt_u (local.get $i) (local.get $len))))
    (local.get $s))
)
