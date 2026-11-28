// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// Generic opcodes typically specify a width. The inputs and outputs
// of that op are the given number of bits wide. There is no notion of
// "sign", so Add32 can be used both for signed and unsigned 32-bit
// addition.

// Signed/unsigned is explicit with the extension ops
// (SignExt*/ZeroExt*) and implicit as the arg to some opcodes
// (e.g. the second argument to shifts is unsigned). If not mentioned,
// all args take signed inputs, or don't care whether their inputs
// are signed or unsigned.

var genericOps = []opData{
	// Pseudo-op.
	{name: "Last", argLength: -1}, // return last element of tuple; for "let" bindings

	// 2-input arithmetic
	// Types must be consistent with Go typing. Add, for example, must take two values
	// of the same type and produces that same type.
	{name: "Add8", argLength: 2, commutative: true}, // arg0 + arg1
	{name: "Add16", argLength: 2, commutative: true},
	{name: "Add32", argLength: 2, commutative: true},
	{name: "Add64", argLength: 2, commutative: true},
	{name: "AddPtr", argLength: 2}, // For address calculations.  arg0 is a pointer and arg1 is an int.
	{name: "Add32F", argLength: 2, commutative: true},
	{name: "Add64F", argLength: 2, commutative: true},

	{name: "Sub8", argLength: 2}, // arg0 - arg1
	{name: "Sub16", argLength: 2},
	{name: "Sub32", argLength: 2},
	{name: "Sub64", argLength: 2},
	{name: "SubPtr", argLength: 2},
	{name: "Sub32F", argLength: 2},
	{name: "Sub64F", argLength: 2},

	{name: "Mul8", argLength: 2, commutative: true}, // arg0 * arg1
	{name: "Mul16", argLength: 2, commutative: true},
	{name: "Mul32", argLength: 2, commutative: true},
	{name: "Mul64", argLength: 2, commutative: true},
	{name: "Mul32F", argLength: 2, commutative: true},
	{name: "Mul64F", argLength: 2, commutative: true},

	{name: "Div32F", argLength: 2}, // arg0 / arg1
	{name: "Div64F", argLength: 2},

	{name: "Hmul32", argLength: 2, commutative: true},
	{name: "Hmul32u", argLength: 2, commutative: true},
	{name: "Hmul64", argLength: 2, commutative: true},
	{name: "Hmul64u", argLength: 2, commutative: true},

	{name: "Mul32uhilo", argLength: 2, typ: "(UInt32,UInt32)", commutative: true}, // arg0 * arg1, returns (hi, lo)
	{name: "Mul64uhilo", argLength: 2, typ: "(UInt64,UInt64)", commutative: true}, // arg0 * arg1, returns (hi, lo)

	{name: "Mul32uover", argLength: 2, typ: "(UInt32,Bool)", commutative: true}, // Let x = arg0*arg1 (full 32x32-> 64 unsigned multiply), returns (uint32(x), (uint32(x) != x))
	{name: "Mul64uover", argLength: 2, typ: "(UInt64,Bool)", commutative: true}, // Let x = arg0*arg1 (full 64x64->128 unsigned multiply), returns (uint64(x), (uint64(x) != x))

	// Weird special instructions for use in the strength reduction of divides.
	// These ops compute unsigned (arg0 + arg1) / 2, correct to all
	// 32/64 bits, even when the intermediate result of the add has 33/65 bits.
	// These ops can assume arg0 >= arg1.
	// Note: these ops aren't commutative!
	{name: "Avg32u", argLength: 2, typ: "UInt32"}, // 32-bit platforms only
	{name: "Avg64u", argLength: 2, typ: "UInt64"}, // 64-bit platforms only

	// For Div16, Div32 and Div64, AuxInt non-zero means that the divisor has been proved to be not -1
	// or that the dividend is not the most negative value.
	{name: "Div8", argLength: 2},  // arg0 / arg1, signed
	{name: "Div8u", argLength: 2}, // arg0 / arg1, unsigned
	{name: "Div16", argLength: 2, aux: "Bool"},
	{name: "Div16u", argLength: 2},
	{name: "Div32", argLength: 2, aux: "Bool"},
	{name: "Div32u", argLength: 2},
	{name: "Div64", argLength: 2, aux: "Bool"},
	{name: "Div64u", argLength: 2},
	{name: "Div128u", argLength: 3}, // arg0:arg1 / arg2 (128-bit divided by 64-bit), returns (q, r)

	// For Mod16, Mod32 and Mod64, AuxInt non-zero means that the divisor has been proved to be not -1.
	{name: "Mod8", argLength: 2},  // arg0 % arg1, signed
	{name: "Mod8u", argLength: 2}, // arg0 % arg1, unsigned
	{name: "Mod16", argLength: 2, aux: "Bool"},
	{name: "Mod16u", argLength: 2},
	{name: "Mod32", argLength: 2, aux: "Bool"},
	{name: "Mod32u", argLength: 2},
	{name: "Mod64", argLength: 2, aux: "Bool"},
	{name: "Mod64u", argLength: 2},

	{name: "And8", argLength: 2, commutative: true}, // arg0 & arg1
	{name: "And16", argLength: 2, commutative: true},
	{name: "And32", argLength: 2, commutative: true},
	{name: "And64", argLength: 2, commutative: true},

	{name: "Or8", argLength: 2, commutative: true}, // arg0 | arg1
	{name: "Or16", argLength: 2, commutative: true},
	{name: "Or32", argLength: 2, commutative: true},
	{name: "Or64", argLength: 2, commutative: true},

	{name: "Xor8", argLength: 2, commutative: true}, // arg0 ^ arg1
	{name: "Xor16", argLength: 2, commutative: true},
	{name: "Xor32", argLength: 2, commutative: true},
	{name: "Xor64", argLength: 2, commutative: true},

	// For shifts, AxB means the shifted value has A bits and the shift amount has B bits.
	// Shift amounts are considered unsigned.
	// If arg1 is known to be nonnegative and less than the number of bits in arg0,
	// then auxInt may be set to 1.
	// This enables better code generation on some platforms.
	{name: "Lsh8x8", argLength: 2, aux: "Bool"}, // arg0 << arg1
	{name: "Lsh8x16", argLength: 2, aux: "Bool"},
	{name: "Lsh8x32", argLength: 2, aux: "Bool"},
	{name: "Lsh8x64", argLength: 2, aux: "Bool"},
	{name: "Lsh16x8", argLength: 2, aux: "Bool"},
	{name: "Lsh16x16", argLength: 2, aux: "Bool"},
	{name: "Lsh16x32", argLength: 2, aux: "Bool"},
	{name: "Lsh16x64", argLength: 2, aux: "Bool"},
	{name: "Lsh32x8", argLength: 2, aux: "Bool"},
	{name: "Lsh32x16", argLength: 2, aux: "Bool"},
	{name: "Lsh32x32", argLength: 2, aux: "Bool"},
	{name: "Lsh32x64", argLength: 2, aux: "Bool"},
	{name: "Lsh64x8", argLength: 2, aux: "Bool"},
	{name: "Lsh64x16", argLength: 2, aux: "Bool"},
	{name: "Lsh64x32", argLength: 2, aux: "Bool"},
	{name: "Lsh64x64", argLength: 2, aux: "Bool"},

	{name: "Rsh8x8", argLength: 2, aux: "Bool"}, // arg0 >> arg1, signed
	{name: "Rsh8x16", argLength: 2, aux: "Bool"},
	{name: "Rsh8x32", argLength: 2, aux: "Bool"},
	{name: "Rsh8x64", argLength: 2, aux: "Bool"},
	{name: "Rsh16x8", argLength: 2, aux: "Bool"},
	{name: "Rsh16x16", argLength: 2, aux: "Bool"},
	{name: "Rsh16x32", argLength: 2, aux: "Bool"},
	{name: "Rsh16x64", argLength: 2, aux: "Bool"},
	{name: "Rsh32x8", argLength: 2, aux: "Bool"},
	{name: "Rsh32x16", argLength: 2, aux: "Bool"},
	{name: "Rsh32x32", argLength: 2, aux: "Bool"},
	{name: "Rsh32x64", argLength: 2, aux: "Bool"},
	{name: "Rsh64x8", argLength: 2, aux: "Bool"},
	{name: "Rsh64x16", argLength: 2, aux: "Bool"},
	{name: "Rsh64x32", argLength: 2, aux: "Bool"},
	{name: "Rsh64x64", argLength: 2, aux: "Bool"},

	{name: "Rsh8Ux8", argLength: 2, aux: "Bool"}, // arg0 >> arg1, unsigned
	{name: "Rsh8Ux16", argLength: 2, aux: "Bool"},
	{name: "Rsh8Ux32", argLength: 2, aux: "Bool"},
	{name: "Rsh8Ux64", argLength: 2, aux: "Bool"},
	{name: "Rsh16Ux8", argLength: 2, aux: "Bool"},
	{name: "Rsh16Ux16", argLength: 2, aux: "Bool"},
	{name: "Rsh16Ux32", argLength: 2, aux: "Bool"},
	{name: "Rsh16Ux64", argLength: 2, aux: "Bool"},
	{name: "Rsh32Ux8", argLength: 2, aux: "Bool"},
	{name: "Rsh32Ux16", argLength: 2, aux: "Bool"},
	{name: "Rsh32Ux32", argLength: 2, aux: "Bool"},
	{name: "Rsh32Ux64", argLength: 2, aux: "Bool"},
	{name: "Rsh64Ux8", argLength: 2, aux: "Bool"},
	{name: "Rsh64Ux16", argLength: 2, aux: "Bool"},
	{name: "Rsh64Ux32", argLength: 2, aux: "Bool"},
	{name: "Rsh64Ux64", argLength: 2, aux: "Bool"},

	// 2-input comparisons
	{name: "Eq8", argLength: 2, commutative: true, typ: "Bool"}, // arg0 == arg1
	{name: "Eq16", argLength: 2, commutative: true, typ: "Bool"},
	{name: "Eq32", argLength: 2, commutative: true, typ: "Bool"},
	{name: "Eq64", argLength: 2, commutative: true, typ: "Bool"},
	{name: "EqPtr", argLength: 2, commutative: true, typ: "Bool"},
	{name: "EqInter", argLength: 2, typ: "Bool"}, // arg0 or arg1 is nil; other cases handled by frontend
	{name: "EqSlice", argLength: 2, typ: "Bool"}, // arg0 or arg1 is nil; other cases handled by frontend
	{name: "Eq32F", argLength: 2, commutative: true, typ: "Bool"},
	{name: "Eq64F", argLength: 2, commutative: true, typ: "Bool"},

	{name: "Neq8", argLength: 2, commutative: true, typ: "Bool"}, // arg0 != arg1
	{name: "Neq16", argLength: 2, commutative: true, typ: "Bool"},
	{name: "Neq32", argLength: 2, commutative: true, typ: "Bool"},
	{name: "Neq64", argLength: 2, commutative: true, typ: "Bool"},
	{name: "NeqPtr", argLength: 2, commutative: true, typ: "Bool"},
	{name: "NeqInter", argLength: 2, typ: "Bool"}, // arg0 or arg1 is nil; other cases handled by frontend
	{name: "NeqSlice", argLength: 2, typ: "Bool"}, // arg0 or arg1 is nil; other cases handled by frontend
	{name: "Neq32F", argLength: 2, commutative: true, typ: "Bool"},
	{name: "Neq64F", argLength: 2, commutative: true, typ: "Bool"},

	{name: "Less8", argLength: 2, typ: "Bool"},  // arg0 < arg1, signed
	{name: "Less8U", argLength: 2, typ: "Bool"}, // arg0 < arg1, unsigned
	{name: "Less16", argLength: 2, typ: "Bool"},
	{name: "Less16U", argLength: 2, typ: "Bool"},
	{name: "Less32", argLength: 2, typ: "Bool"},
	{name: "Less32U", argLength: 2, typ: "Bool"},
	{name: "Less64", argLength: 2, typ: "Bool"},
	{name: "Less64U", argLength: 2, typ: "Bool"},
	{name: "Less32F", argLength: 2, typ: "Bool"},
	{name: "Less64F", argLength: 2, typ: "Bool"},

	{name: "Leq8", argLength: 2, typ: "Bool"},  // arg0 <= arg1, signed
	{name: "Leq8U", argLength: 2, typ: "Bool"}, // arg0 <= arg1, unsigned
	{name: "Leq16", argLength: 2, typ: "Bool"},
	{name: "Leq16U", argLength: 2, typ: "Bool"},
	{name: "Leq32", argLength: 2, typ: "Bool"},
	{name: "Leq32U", argLength: 2, typ: "Bool"},
	{name: "Leq64", argLength: 2, typ: "Bool"},
	{name: "Leq64U", argLength: 2, typ: "Bool"},
	{name: "Leq32F", argLength: 2, typ: "Bool"},
	{name: "Leq64F", argLength: 2, typ: "Bool"},

	// the type of a CondSelect is the same as the type of its first
	// two arguments, which should be register-width scalars; the third
	// argument should be a boolean
	{name: "CondSelect", argLength: 3}, // arg2 ? arg0 : arg1

	// boolean ops
	{name: "AndB", argLength: 2, commutative: true, typ: "Bool"}, // arg0 && arg1 (not shortcircuited)
	{name: "OrB", argLength: 2, commutative: true, typ: "Bool"},  // arg0 || arg1 (not shortcircuited)
	{name: "EqB", argLength: 2, commutative: true, typ: "Bool"},  // arg0 == arg1
	{name: "NeqB", argLength: 2, commutative: true, typ: "Bool"}, // arg0 != arg1
	{name: "Not", argLength: 1, typ: "Bool"},                     // !arg0, boolean

	// 1-input ops
	{name: "Neg8", argLength: 1}, // -arg0
	{name: "Neg16", argLength: 1},
	{name: "Neg32", argLength: 1},
	{name: "Neg64", argLength: 1},
	{name: "Neg32F", argLength: 1},
	{name: "Neg64F", argLength: 1},

	{name: "Com8", argLength: 1}, // ^arg0
	{name: "Com16", argLength: 1},
	{name: "Com32", argLength: 1},
	{name: "Com64", argLength: 1},

	{name: "Ctz8", argLength: 1},         // Count trailing (low order) zeroes (returns 0-8)
	{name: "Ctz16", argLength: 1},        // Count trailing (low order) zeroes (returns 0-16)
	{name: "Ctz32", argLength: 1},        // Count trailing (low order) zeroes (returns 0-32)
	{name: "Ctz64", argLength: 1},        // Count trailing (low order) zeroes (returns 0-64)
	{name: "Ctz64On32", argLength: 2},    // Count trailing (low order) zeroes (returns 0-64) in arg[1]<<32+arg[0]
	{name: "Ctz8NonZero", argLength: 1},  // same as above, but arg[0] known to be non-zero, returns 0-7
	{name: "Ctz16NonZero", argLength: 1}, // same as above, but arg[0] known to be non-zero, returns 0-15
	{name: "Ctz32NonZero", argLength: 1}, // same as above, but arg[0] known to be non-zero, returns 0-31
	{name: "Ctz64NonZero", argLength: 1}, // same as above, but arg[0] known to be non-zero, returns 0-63
	{name: "BitLen8", argLength: 1},      // Number of bits in arg[0] (returns 0-8)
	{name: "BitLen16", argLength: 1},     // Number of bits in arg[0] (returns 0-16)
	{name: "BitLen32", argLength: 1},     // Number of bits in arg[0] (returns 0-32)
	{name: "BitLen64", argLength: 1},     // Number of bits in arg[0] (returns 0-64)

	{name: "Bswap16", argLength: 1}, // Swap bytes
	{name: "Bswap32", argLength: 1}, // Swap bytes
	{name: "Bswap64", argLength: 1}, // Swap bytes

	{name: "BitRev8", argLength: 1},  // Reverse the bits in arg[0]
	{name: "BitRev16", argLength: 1}, // Reverse the bits in arg[0]
	{name: "BitRev32", argLength: 1}, // Reverse the bits in arg[0]
	{name: "BitRev64", argLength: 1}, // Reverse the bits in arg[0]

	{name: "PopCount8", argLength: 1},  // Count bits in arg[0]
	{name: "PopCount16", argLength: 1}, // Count bits in arg[0]
	{name: "PopCount32", argLength: 1}, // Count bits in arg[0]
	{name: "PopCount64", argLength: 1}, // Count bits in arg[0]

	// RotateLeftX instructions rotate the X bits of arg[0] to the left
	// by the low lg_2(X) bits of arg[1], interpreted as an unsigned value.
	// Note that this works out regardless of the bit width or signedness of
	// arg[1]. In particular, RotateLeft by x is the same as RotateRight by -x.
	{name: "RotateLeft64", argLength: 2},
	{name: "RotateLeft32", argLength: 2},
	{name: "RotateLeft16", argLength: 2},
	{name: "RotateLeft8", argLength: 2},

	// Square root.
	// Special cases:
	//   +∞  → +∞
	//   ±0  → ±0 (sign preserved)
	//   x<0 → NaN
	//   NaN → NaN
	{name: "Sqrt", argLength: 1},   // √arg0 (floating point, double precision)
	{name: "Sqrt32", argLength: 1}, // √arg0 (floating point, single precision)

	// Round to integer, float64 only.
	// Special cases:
	//   ±∞  → ±∞ (sign preserved)
	//   ±0  → ±0 (sign preserved)
	//   NaN → NaN
	{name: "Floor", argLength: 1},       // round arg0 toward -∞
	{name: "Ceil", argLength: 1},        // round arg0 toward +∞
	{name: "Trunc", argLength: 1},       // round arg0 toward 0
	{name: "Round", argLength: 1},       // round arg0 to nearest, ties away from 0
	{name: "RoundToEven", argLength: 1}, // round arg0 to nearest, ties to even

	// Modify the sign bit
	{name: "Abs", argLength: 1},      // absolute value arg0
	{name: "Copysign", argLength: 2}, // copy sign from arg0 to arg1

	// Integer min/max implementation, if hardware is available.
	{name: "Min64", argLength: 2},  // min(arg0,arg1), signed
	{name: "Max64", argLength: 2},  // max(arg0,arg1), signed
	{name: "Min64u", argLength: 2}, // min(arg0,arg1), unsigned
	{name: "Max64u", argLength: 2}, // max(arg0,arg1), unsigned

	// Float min/max implementation, if hardware is available.
	{name: "Min64F", argLength: 2}, // min(arg0,arg1)
	{name: "Min32F", argLength: 2}, // min(arg0,arg1)
	{name: "Max64F", argLength: 2}, // max(arg0,arg1)
	{name: "Max32F", argLength: 2}, // max(arg0,arg1)

	// 3-input opcode.
	// Fused-multiply-add, float64 only.
	// When a*b+c is exactly zero (before rounding), then the result is +0 or -0.
	// The 0's sign is determined according to the standard rules for the
	// addition (-0 if both a*b and c are -0, +0 otherwise).
	//
	// Otherwise, when a*b+c rounds to zero, then the resulting 0's sign is
	// determined by the sign of the exact result a*b+c.
	// See section 6.3 in ieee754.
	//
	// When the multiply is an infinity times a zero, the result is NaN.
	// See section 7.2 in ieee754.
	{name: "FMA", argLength: 3}, // compute (a*b)+c without intermediate rounding

	// Data movement. Max argument length for Phi is indefinite.
	{name: "Phi", argLength: -1, zeroWidth: true}, // select an argument based on which predecessor block we came from
	{name: "Copy", argLength: 1},                  // output = arg0
	// Convert converts between pointers and integers.
	// We have a special op for this so as to not confuse GC
	// (particularly stack maps).  It takes a memory arg so it
	// gets correctly ordered with respect to GC safepoints.
	// It gets compiled to nothing, so its result must in the same
	// register as its argument. regalloc knows it can use any
	// allocatable integer register for OpConvert.
	// arg0=ptr/int arg1=mem, output=int/ptr
	{name: "Convert", argLength: 2, zeroWidth: true, resultInArg0: true},

	// constants. Constant values are stored in the aux or
	// auxint fields.
	{name: "ConstBool", aux: "Bool"},     // auxint is 0 for false and 1 for true
	{name: "ConstString", aux: "String"}, // value is aux.(string)
	{name: "ConstNil", typ: "BytePtr"},   // nil pointer
	{name: "Const8", aux: "Int8"},        // auxint is sign-extended 8 bits
	{name: "Const16", aux: "Int16"},      // auxint is sign-extended 16 bits
	{name: "Const32", aux: "Int32"},      // auxint is sign-extended 32 bits
	// Note: ConstX are sign-extended even when the type of the value is unsigned.
	// For instance, uint8(0xaa) is stored as auxint=0xffffffffffffffaa.
	{name: "Const64", aux: "Int64"}, // value is auxint
	// Note: for both Const32F and Const64F, we disallow encoding NaNs.
	// Signaling NaNs are tricky because if you do anything with them, they become quiet.
	// Particularly, converting a 32 bit sNaN to 64 bit and back converts it to a qNaN.
	// See issue 36399 and 36400.
	// Encodings of +inf, -inf, and -0 are fine.
	{name: "Const32F", aux: "Float32"}, // value is math.Float64frombits(uint64(auxint)) and is exactly representable as float 32
	{name: "Const64F", aux: "Float64"}, // value is math.Float64frombits(uint64(auxint))
	{name: "ConstInterface"},           // nil interface
	{name: "ConstSlice"},               // nil slice

	// Constant-like things
	{name: "InitMem", zeroWidth: true},                               // memory input to the function.
	{name: "Arg", aux: "SymOff", symEffect: "Read", zeroWidth: true}, // argument to the function.  aux=GCNode of arg, off = offset in that arg.

	// Like Arg, these are generic ops that survive lowering. AuxInt is a register index, and the actual output register for each index is defined by the architecture.
	// AuxInt = integer argument index (not a register number). ABI-specified spill loc obtained from function
	{name: "ArgIntReg", aux: "NameOffsetInt8", zeroWidth: true},   // argument to the function in an int reg.
	{name: "ArgFloatReg", aux: "NameOffsetInt8", zeroWidth: true}, // argument to the function in a float reg.

	// The address of a variable.  arg0 is the base pointer.
	// If the variable is a global, the base pointer will be SB and
	// the Aux field will be a *obj.LSym.
	// If the variable is a local, the base pointer will be SP and
	// the Aux field will be a *ir.Name
	{name: "Addr", argLength: 1, aux: "Sym", symEffect: "Addr"},      // Address of a variable.  Arg0=SB.  Aux identifies the variable.
	{name: "LocalAddr", argLength: 2, aux: "Sym", symEffect: "Addr"}, // Address of a variable.  Arg0=SP. Arg1=mem. Aux identifies the variable.

	{name: "SP", zeroWidth: true, fixedReg: true},                 // stack pointer
	{name: "SB", typ: "Uintptr", zeroWidth: true, fixedReg: true}, // static base pointer (a.k.a. globals pointer)
	{name: "Invalid"}, // unused value
	{name: "SPanchored", typ: "Uintptr", argLength: 2, zeroWidth: true}, // arg0 = SP, arg1 = mem. Result is identical to arg0, but cannot be scheduled before memory state arg1.

	// Memory operations
	{name: "Load", argLength: 2},                              // Load from arg0.  arg1=memory
	{name: "Dereference", argLength: 2},                       // Load from arg0.  arg1=memory.  Helper op for arg/result passing, result is an otherwise not-SSA-able "value".
	{name: "Store", argLength: 3, typ: "Mem", aux: "TypSize"}, // Store arg1 to arg0.  arg2=memory, aux=type.  Returns memory. auxint=1 if what is stored needs no publishing (runtime/shared.go).

	// gd: a string's words, loaded and stored with one 16-byte access
	// (single-copy atomic where runtime.stringAtomic; doc/gd/sharing.md).
	// Generated for amd64 and arm64 only.
	{name: "StringLoad16", argLength: 2},                              // load the two words of the string at arg0, as a tuple (w0, w1). arg1=memory
	{name: "StringStore16", argLength: 4, typ: "Mem", aux: "TypSize"}, // store the string of words arg1 (w0, a pointer) and arg2 (w1) at arg0. arg3=memory, aux=type (string).  Returns memory.

	// masked memory operations.
	// TODO add 16 and 8
	{name: "LoadMasked8", argLength: 3},                           // Load from arg0, arg1 = mask of 8-bits, arg2 = memory
	{name: "LoadMasked16", argLength: 3},                          // Load from arg0, arg1 = mask of 16-bits, arg2 = memory
	{name: "LoadMasked32", argLength: 3},                          // Load from arg0, arg1 = mask of 32-bits, arg2 = memory
	{name: "LoadMasked64", argLength: 3},                          // Load from arg0, arg1 = mask of 64-bits, arg2 = memory
	{name: "StoreMasked8", argLength: 4, typ: "Mem", aux: "Typ"},  // Store arg2 to arg0, arg1=mask of 8-bits, arg3 = memory
	{name: "StoreMasked16", argLength: 4, typ: "Mem", aux: "Typ"}, // Store arg2 to arg0, arg1=mask of 16-bits, arg3 = memory
	{name: "StoreMasked32", argLength: 4, typ: "Mem", aux: "Typ"}, // Store arg2 to arg0, arg1=mask of 32-bits, arg3 = memory
	{name: "StoreMasked64", argLength: 4, typ: "Mem", aux: "Typ"}, // Store arg2 to arg0, arg1=mask of 64-bits, arg3 = memory

	// Normally we require that the source and destination of Move do not overlap.
	// There is an exception when we know all the loads will happen before all
	// the stores. In that case, overlap is ok. See
	// memmove inlining in generic.rules. When inlineablememmovesize (in ../rewrite.go)
	// returns true, we must do all loads before all stores, when lowering Move.
	// The type of Move is used for the write barrier pass to insert write barriers
	// and for alignment on some architectures.
	// For pointerless types, it is possible for the type to be inaccurate.
	// For type alignment and pointer information, use the type in Aux;
	// for type size, use the size in AuxInt.
	// The "inline runtime.memmove" rewrite rule generates Moves with inaccurate types,
	// such as type byte instead of the more accurate type [8]byte.
	{name: "Move", argLength: 3, typ: "Mem", aux: "TypSize"}, // arg0=destptr, arg1=srcptr, arg2=mem, auxint=size, aux=type.  Returns memory.
	{name: "Zero", argLength: 2, typ: "Mem", aux: "TypSize"}, // arg0=destptr, arg1=mem, auxint=size, aux=type. Returns memory.

	// Memory operations with write barriers.
	// Expand to runtime calls. Write barrier will be removed if write on stack.
	{name: "StoreWB", argLength: 3, typ: "Mem", aux: "TypSize"},         // Store arg1 to arg0. arg2=memory, aux=type.  Returns memory.
	{name: "StringStore16WB", argLength: 4, typ: "Mem", aux: "TypSize"}, // StringStore16 with a write barrier for arg1.
	{name: "MoveWB", argLength: 3, typ: "Mem", aux: "TypSize"},          // arg0=destptr, arg1=srcptr, arg2=mem, auxint=size, aux=type.  Returns memory.
	{name: "ZeroWB", argLength: 2, typ: "Mem", aux: "TypSize"},          // arg0=destptr, arg1=mem, auxint=size, aux=type. Returns memory.
	{name: "WBend", argLength: 1, typ: "Mem"},                           // Write barrier code is done, interrupting is now allowed.

	// A sequence lock is held from ShareLocked, whose memory is that of
	// the compare-and-swap that took it, to ShareUnlocked, after the
	// store that gave it back (ssagen's shareStoreIface): no interrupting.
	{name: "ShareLocked", argLength: 1, typ: "Mem"},
	{name: "ShareUnlocked", argLength: 1, typ: "Mem"},

	// WB invokes runtime.gcWriteBarrier.  This is not a normal
	// call: it takes arguments in registers, doesn't clobber
	// general-purpose registers (the exact clobber set is
	// arch-dependent), and is not a safe-point.
	{name: "WB", argLength: 1, typ: "(BytePtr,Mem)", aux: "Int64"}, // arg0=mem, auxint=# of buffer entries needed. Returns buffer pointer and memory.

	{name: "HasCPUFeature", argLength: 0, typ: "bool", aux: "Sym", symEffect: "None"}, // aux=place that this feature flag can be loaded from

	// PanicBounds and PanicExtend generate a runtime panic.
	// Their arguments provide index values to use in panic messages.
	// Both PanicBounds and PanicExtend have an AuxInt value from the BoundsKind type (in ../op.go).
	// PanicBounds' index is int sized.
	// PanicExtend's index is int64 sized. (PanicExtend is only used on 32-bit archs.)
	{name: "PanicBounds", argLength: 3, aux: "Int64", typ: "Mem", call: true}, // arg0=idx, arg1=len, arg2=mem, returns memory.
	{name: "PanicExtend", argLength: 4, aux: "Int64", typ: "Mem", call: true}, // arg0=idxHi, arg1=idxLo, arg2=len, arg3=mem, returns memory.

	// Function calls. Arguments to the call have already been written to the stack.
	// Return values appear on the stack. The method receiver, if any, is treated
	// as a phantom first argument.
	// TODO(josharian): ClosureCall and InterCall should have Int32 aux
	// to match StaticCall's 32 bit arg size limit.
	// TODO(drchase,josharian): could the arg size limit be bundled into the rules for CallOff?

	// Before lowering, LECalls receive their fixed inputs (first), memory (last),
	// and a variable number of input values in the middle.
	// They produce a variable number of result values.
	// These values are not necessarily "SSA-able"; they can be too large,
	// but in that case inputs are loaded immediately before with OpDereference,
	// and outputs are stored immediately with OpStore.
	//
	// After call expansion, Calls have the same fixed-middle-memory arrangement of inputs,
	// with the difference that the "middle" is only the register-resident inputs,
	// and the non-register inputs are instead stored at ABI-defined offsets from SP
	// (and the stores thread through the memory that is ultimately an input to the call).
	// Outputs follow a similar pattern; register-resident outputs are the leading elements
	// of a Result-typed output, with memory last, and any memory-resident outputs have been
	// stored to ABI-defined locations.  Each non-memory input or output fits in a register.
	//
	// Subsequent architecture-specific lowering only changes the opcode.

	{name: "ClosureCall", argLength: -1, aux: "CallOff", call: true},   // arg0=code pointer, arg1=context ptr, arg2..argN-1 are register inputs, argN=memory.  auxint=arg size.  Returns Result of register results, plus memory.
	{name: "StaticCall", argLength: -1, aux: "CallOff", call: true},    // call function aux.(*obj.LSym), arg0..argN-1 are register inputs, argN=memory.  auxint=arg size.  Returns Result of register results, plus memory.
	{name: "InterCall", argLength: -1, aux: "CallOff", call: true},     // interface call.  arg0=code pointer, arg1..argN-1 are register inputs, argN=memory, auxint=arg size.  Returns Result of register results, plus memory.
	{name: "TailCall", argLength: -1, aux: "CallOff", call: true},      // tail call function aux.(*obj.LSym), arg0..argN-1 are register inputs, argN=memory.  auxint=arg size.  Returns Result of register results, plus memory.
	{name: "TailCallInter", argLength: -1, aux: "CallOff", call: true}, // tail call function arg0=code pointer, arg1..argN-1 are register inputs, argN=memory.  auxint=arg size.  Returns Result of register results, plus memory.

	{name: "ClosureLECall", argLength: -1, aux: "CallOff", call: true},   // late-expanded closure call. arg0=code pointer, arg1=context ptr,  arg2..argN-1 are inputs, argN is mem. auxint = arg size. Result is tuple of result(s), plus mem.
	{name: "StaticLECall", argLength: -1, aux: "CallOff", call: true},    // late-expanded static call function aux.(*ssa.AuxCall.Fn). arg0..argN-1 are inputs, argN is mem. auxint = arg size. Result is tuple of result(s), plus mem.
	{name: "InterLECall", argLength: -1, aux: "CallOff", call: true},     // late-expanded interface call. arg0=code pointer, arg1..argN-1 are inputs, argN is mem. auxint = arg size. Result is tuple of result(s), plus mem.
	{name: "TailLECall", argLength: -1, aux: "CallOff", call: true},      // late-expanded static tail call function aux.(*ssa.AuxCall.Fn). arg0..argN-1 are inputs, argN is mem. auxint = arg size. Result is tuple of result(s), plus mem.
	{name: "TailLECallInter", argLength: -1, aux: "CallOff", call: true}, // late-expanded static tail call function arg0=code pointer, arg1..argN-1 are inputs, argN is mem. auxint = arg size. Result is tuple of result(s), plus mem.

	// Conversions: signed extensions, zero (unsigned) extensions, truncations
	{name: "SignExt8to16", argLength: 1, typ: "Int16"},
	{name: "SignExt8to32", argLength: 1, typ: "Int32"},
	{name: "SignExt8to64", argLength: 1, typ: "Int64"},
	{name: "SignExt16to32", argLength: 1, typ: "Int32"},
	{name: "SignExt16to64", argLength: 1, typ: "Int64"},
	{name: "SignExt32to64", argLength: 1, typ: "Int64"},
	{name: "ZeroExt8to16", argLength: 1, typ: "UInt16"},
	{name: "ZeroExt8to32", argLength: 1, typ: "UInt32"},
	{name: "ZeroExt8to64", argLength: 1, typ: "UInt64"},
	{name: "ZeroExt16to32", argLength: 1, typ: "UInt32"},
	{name: "ZeroExt16to64", argLength: 1, typ: "UInt64"},
	{name: "ZeroExt32to64", argLength: 1, typ: "UInt64"},
	{name: "Trunc16to8", argLength: 1},
	{name: "Trunc32to8", argLength: 1},
	{name: "Trunc32to16", argLength: 1},
	{name: "Trunc64to8", argLength: 1},
	{name: "Trunc64to16", argLength: 1},
	{name: "Trunc64to32", argLength: 1},

	{name: "Cvt32to32F", argLength: 1},
	{name: "Cvt32to64F", argLength: 1},
	{name: "Cvt64to32F", argLength: 1},
	{name: "Cvt64to64F", argLength: 1},
	{name: "Cvt32Fto32", argLength: 1},
	{name: "Cvt32Fto64", argLength: 1},
	{name: "Cvt64Fto32", argLength: 1},
	{name: "Cvt64Fto64", argLength: 1},
	{name: "Cvt32Fto64F", argLength: 1},
	{name: "Cvt64Fto32F", argLength: 1},
	{name: "CvtBoolToUint8", argLength: 1},

	// Force rounding to precision of type.
	{name: "Round32F", argLength: 1},
	{name: "Round64F", argLength: 1},

	// Automatically inserted safety checks
	{name: "IsNonNil", argLength: 1, typ: "Bool"},        // arg0 != nil
	{name: "IsInBounds", argLength: 2, typ: "Bool"},      // 0 <= arg0 < arg1. arg1 is guaranteed >= 0.
	{name: "IsSliceInBounds", argLength: 2, typ: "Bool"}, // 0 <= arg0 <= arg1. arg1 is guaranteed >= 0.
	{name: "NilCheck", argLength: 2, nilCheck: true},     // arg0=ptr, arg1=mem. Panics if arg0 is nil. Returns the ptr unmodified.

	// Pseudo-ops
	{name: "GetG", argLength: 1, zeroWidth: true}, // runtime.getg() (read g pointer). arg0=mem
	{name: "GetClosurePtr"},                       // get closure pointer from dedicated register
	{name: "GetCallerPC"},                         // for GetCallerPC intrinsic
	{name: "GetCallerSP", argLength: 1},           // for GetCallerSP intrinsic. arg0=mem.

	// Indexing operations
	{name: "PtrIndex", argLength: 2},             // arg0=ptr, arg1=index. Computes ptr+sizeof(*v.type)*index, where index is extended to ptrwidth type
	{name: "OffPtr", argLength: 1, aux: "Int64"}, // arg0 + auxint (arg0 and result are pointers)

	// Slices
	{name: "SliceMake", argLength: 3},                // arg0=ptr, arg1=len, arg2=cap
	{name: "SlicePtr", argLength: 1, typ: "BytePtr"}, // ptr(arg0)
	{name: "SliceLen", argLength: 1},                 // len(arg0)
	{name: "SliceCap", argLength: 1},                 // cap(arg0)
	// SlicePtrUnchecked, like SlicePtr, extracts the pointer from a slice.
	// SlicePtr values are assumed non-nil, because they are guarded by bounds checks.
	// SlicePtrUnchecked values can be nil.
	{name: "SlicePtrUnchecked", argLength: 1},

	// Packed slices (types.SlicePacked, doc/gd/slice16.md): in memory
	// and in registers a slice is w0, a pointer, and w1, a uint64 whose
	// bytes as stored are the length (bytes 0..3) and the capacity
	// (bytes 4..7); a huge slice's w0 is its header's address and w1 the
	// marker. The slicecodec pass expands these ops to their branches.
	{name: "SlicePack", argLength: 4},                  // w0 of the slice arg0=ptr, arg1=len, arg2=cap: ptr, or a new header if huge. arg3=memory. Returns (w0, memory).
	{name: "SlicePackW1", argLength: 3, typ: "UInt64"}, // w1 of the slice of length arg0 and capacity arg1: the two, or the marker if huge. arg2=its w0 (Select0 of its SlicePack), which it goes with.
	{name: "SliceUnpackPtr", argLength: 3},             // the pointer of the slice of words arg0 (w0) and arg1 (w1); arg2=memory (a huge slice's header is read)
	{name: "SliceUnpackLen", argLength: 3, typ: "Int"}, // its length
	{name: "SliceUnpackCap", argLength: 3, typ: "Int"}, // its capacity

	// Complex (part/whole)
	{name: "ComplexMake", argLength: 2}, // arg0=real, arg1=imag
	{name: "ComplexReal", argLength: 1}, // real(arg0)
	{name: "ComplexImag", argLength: 1}, // imag(arg0)

	// Strings
	// A string has two values in SSA where it is two words
	// (types.StringTwoWords): arg0=w0 (the address of the bytes, or
	// with 8-byte words an inline string's tag and bytes 8..14:
	// types.StringTagged) and arg1=w1 (the length, or an inline
	// string's bytes 0..7). With GOWASM=gc it has three: arg0=ptr (word
	// 0: the address of the bytes, nil if inline), arg1=word 1 (the
	// hash or bytes 0..7; wasm32: A) and arg2=word 2 (tag<<60 | len, or
	// tag<<60 | bytes 8..14; wasm32: B).
	{name: "StringMake", argLength: -1},                 // arg0=w0, arg1=w1[, arg2=word 2]
	{name: "StringPtr", argLength: 1, typ: "BytePtr"},   // word 0 of arg0, raw: the address of the bytes unless inline (tagged, or nil with GOWASM=gc)
	{name: "StringWord1", argLength: 1, typ: "Uintptr"}, // word 1 of arg0, raw (two words: the length or bytes 0..7)
	{name: "StringLen", argLength: 1, typ: "Int"},       // len(arg0): logical length (tag-decoded)
	{name: "StringWord2", argLength: 1, typ: "Int"},     // word 2 of arg0 raw (tag nibble + packed), GOWASM=gc only.

	// The string of wasm32 (types.StringWasm32), whose word 0 is text
	// in an inline string and the address of the bytes otherwise, and
	// in SSA both a pointer (nil if inline) and the low half of A.
	// Generated only for GOWASM=gc,split, whose backend compiles them,
	// but PtrBits, which is also word 0 of a tagged string as an integer
	// (generic.rules turns it into a copy elsewhere).
	{name: "PtrBits", argLength: 1, typ: "Uintptr"},       // the address of the pointer arg0, as an integer (no memory argument)
	{name: "StringLenAB", argLength: 2, typ: "Int"},       // the length of the string whose A and B are arg0 and arg1
	{name: "StringLoadPtr", argLength: 3, typ: "BytePtr"}, // word 0 of the string at arg0 as a pointer if arg1 (its B) has a tag of 0, else nil. arg2=mem
	{name: "StringStorePtr", argLength: 3, typ: "Mem"},    // store the pointer arg1 as word 0 of the string at arg0 (the word and its element of the array of pointers); A is stored after it. arg2=mem

	// Interfaces
	// gd fat-interface: IMake carries the full 32B iface header in SSA.
	// arg0=itab, arg1=data (boxed pointer, nil when inline), arg2=inline real
	// (float64 half of the 16B inline payload), arg3=inline imag.
	{name: "IMake", argLength: 4},
	{name: "ITab", argLength: 1, typ: "Uintptr"},        // arg0=interface, returns itable field
	{name: "IData", argLength: 1},                       // arg0=interface, returns data field (boxed; nil if inline)
	{name: "IInlineReal", argLength: 1, typ: "Float64"}, // arg0=interface, returns inline payload real half
	{name: "IInlineImag", argLength: 1, typ: "Float64"}, // arg0=interface, returns inline payload imag half

	// Structs
	{name: "StructMake", argLength: -1},                // args...=field0..n-1. Returns struct with n fields. Must have >0 size (use Empty otherwise).
	{name: "StructSelect", argLength: 1, aux: "Int64"}, // arg0=struct, auxint=field index.  Returns the auxint'th field.

	// Arrays
	{name: "ArrayMake1", argLength: 1},                // Returns array with 1 element. Use Empty if the element is zero-sized.
	{name: "ArraySelect", argLength: 1, aux: "Int64"}, // arg0=array, auxint=index. Returns a[i].

	// Spill&restore ops for the register allocator. These are
	// semantically identical to OpCopy; they do not take/return
	// stores like regular memory ops do. We can get away without memory
	// args because we know there is no aliasing of spill slots on the stack.
	{name: "StoreReg", argLength: 1},
	{name: "LoadReg", argLength: 1},

	// Used during ssa construction. Like Copy, but the arg has not been specified yet.
	{name: "FwdRef", aux: "Sym", symEffect: "None"},

	// Unknown value. Used for Values whose values don't matter because they are dead code.
	{name: "Unknown"},

	{name: "VarDef", argLength: 1, aux: "Sym", typ: "Mem", symEffect: "None", zeroWidth: true}, // aux is a *ir.Name of a variable that is about to be initialized.  arg0=mem, returns mem
	// TODO: what's the difference between VarLive and KeepAlive?
	{name: "VarLive", argLength: 1, aux: "Sym", symEffect: "Read", zeroWidth: true}, // aux is a *ir.Name of a variable that must be kept live.  arg0=mem, returns mem
	// gd: C23Slow reports whether a check of arg0, a pointer made of the
	// pointer arg1, is to be made by the runtime: before the store or
	// the call that it is for. The pass "c headers" makes of it a
	// comparison with what runtime.c23Header says of arg1, or true.
	// auxint is the check (the cSlow constants), then the mask of the
	// alignment that arg0 is to have <<8 and the size that it is
	// converted to <<16.
	{name: "C23Slow", argLength: 3, aux: "Int64", typ: "Bool"}, // arg2=mem
	// The check of a C23Slow that a stub of the runtime makes with the
	// shadow of C's memory (cheaders.go's cShadowSite): true if the
	// runtime is to see arg0, a pointer. auxint is the stub's number
	// (cshadowstubs.go); arg1=mem.
	{name: "C23ShadowCheck", argLength: 2, aux: "Int64", typ: "Bool"},

	{name: "KeepAlive", argLength: 2, typ: "Mem", zeroWidth: true},                  // arg[0] is a value that must be kept alive until this mark.  arg[1]=mem, returns mem

	// InlMark marks the start of an inlined function body. Its AuxInt field
	// distinguishes which entry in the local inline tree it is marking.
	{name: "InlMark", argLength: 1, aux: "Int32", typ: "Void"}, // arg[0]=mem, returns void.

	// Ops for breaking 64-bit operations on 32-bit architectures
	{name: "Int64Make", argLength: 2, typ: "UInt64"}, // arg0=hi, arg1=lo
	{name: "Int64Hi", argLength: 1, typ: "UInt32"},   // high 32-bit of arg0
	{name: "Int64Lo", argLength: 1, typ: "UInt32"},   // low 32-bit of arg0

	{name: "Add32carry", argLength: 2, commutative: true, typ: "(UInt32,Flags)"},          // arg0 + arg1, returns (value, carry)
	{name: "Add32withcarry", argLength: 3, commutative: true},                             // arg0 + arg1 + arg2, arg2=carry (0 or 1)
	{name: "Add32carrywithcarry", argLength: 3, commutative: true, typ: "(UInt32,Flags)"}, // arg0 + arg1 + arg2, arg2=carry, returns (value, carry)

	{name: "Sub32carry", argLength: 2, typ: "(UInt32,Flags)"}, // arg0 - arg1, returns (value, carry)
	{name: "Sub32withcarry", argLength: 3},                    // arg0 - arg1 - arg2, arg2=carry (0 or 1)

	{name: "Add64carry", argLength: 3, commutative: true, typ: "(UInt64,UInt64)"}, // arg0 + arg1 + arg2, arg2 must be 0 or 1. returns (value, value>>64)
	{name: "Sub64borrow", argLength: 3, typ: "(UInt64,UInt64)"},                   // arg0 - (arg1 + arg2), arg2 must be 0 or 1. returns (value, value>>64&1)

	{name: "Signmask", argLength: 1, typ: "Int32"},  // 0 if arg0 >= 0, -1 if arg0 < 0
	{name: "Zeromask", argLength: 1, typ: "UInt32"}, // 0 if arg0 == 0, 0xffffffff if arg0 != 0
	{name: "Slicemask", argLength: 1},               // 0 if arg0 == 0, -1 if arg0 > 0, undef if arg0<0. Type is native int size.

	{name: "SpectreIndex", argLength: 2},      // arg0 if 0 <= arg0 < arg1, 0 otherwise. Type is native int size.
	{name: "SpectreSliceIndex", argLength: 2}, // arg0 if 0 <= arg0 <= arg1, 0 otherwise. Type is native int size.

	{name: "Cvt32Uto32F", argLength: 1}, // uint32 -> float32, only used on 32-bit arch
	{name: "Cvt32Uto64F", argLength: 1}, // uint32 -> float64, only used on 32-bit arch
	{name: "Cvt32Fto32U", argLength: 1}, // float32 -> uint32, only used on 32-bit arch
	{name: "Cvt64Fto32U", argLength: 1}, // float64 -> uint32, only used on 32-bit arch
	{name: "Cvt64Uto32F", argLength: 1}, // uint64 -> float32, only used on archs that has the instruction
	{name: "Cvt64Uto64F", argLength: 1}, // uint64 -> float64, only used on archs that has the instruction
	{name: "Cvt32Fto64U", argLength: 1}, // float32 -> uint64, only used on archs that has the instruction
	{name: "Cvt64Fto64U", argLength: 1}, // float64 -> uint64, only used on archs that has the instruction

	// pseudo-ops for breaking Tuple
	{name: "Select0", argLength: 1, zeroWidth: true},  // the first component of a tuple
	{name: "Select1", argLength: 1, zeroWidth: true},  // the second component of a tuple
	{name: "MakeTuple", argLength: 2},                 // arg0 arg1 are components of a "Tuple" (like the result from a 128bits op).
	{name: "SelectN", argLength: 1, aux: "Int64"},     // arg0=result, auxint=field index.  Returns the auxint'th member.
	{name: "SelectNAddr", argLength: 1, aux: "Int64"}, // arg0=result, auxint=field index.  Returns the address of auxint'th member. Used for un-SSA-able result types.
	{name: "MakeResult", argLength: -1},               // arg0 .. are components of a "Result" (like the result from a Call). The last arg should be memory (like the result from a call).

	// Atomic operations used for semantically inlining sync/atomic and
	// internal/runtime/atomic. Atomic loads return a new memory so that
	// the loads are properly ordered with respect to other loads and
	// stores.
	{name: "AtomicLoad8", argLength: 2, typ: "(UInt8,Mem)"},                                    // Load from arg0.  arg1=memory.  Returns loaded value and new memory.
	{name: "AtomicLoad32", argLength: 2, typ: "(UInt32,Mem)"},                                  // Load from arg0.  arg1=memory.  Returns loaded value and new memory.
	{name: "AtomicLoad64", argLength: 2, typ: "(UInt64,Mem)"},                                  // Load from arg0.  arg1=memory.  Returns loaded value and new memory.
	{name: "AtomicLoadPtr", argLength: 2, typ: "(BytePtr,Mem)"},                                // Load from arg0.  arg1=memory.  Returns loaded value and new memory.
	{name: "AtomicLoadAcq32", argLength: 2, typ: "(UInt32,Mem)"},                               // Load from arg0.  arg1=memory.  Lock acquisition, returns loaded value and new memory.
	{name: "AtomicLoadAcq64", argLength: 2, typ: "(UInt64,Mem)"},                               // Load from arg0.  arg1=memory.  Lock acquisition, returns loaded value and new memory.
	{name: "AtomicStore8", argLength: 3, typ: "Mem", hasSideEffects: true},                     // Store arg1 to *arg0.  arg2=memory.  Returns memory.
	{name: "AtomicStore32", argLength: 3, typ: "Mem", hasSideEffects: true},                    // Store arg1 to *arg0.  arg2=memory.  Returns memory.
	{name: "AtomicStore64", argLength: 3, typ: "Mem", hasSideEffects: true},                    // Store arg1 to *arg0.  arg2=memory.  Returns memory.
	{name: "AtomicStorePtrNoWB", argLength: 3, typ: "Mem", hasSideEffects: true},               // Store arg1 to *arg0.  arg2=memory.  Returns memory.
	{name: "AtomicStoreRel32", argLength: 3, typ: "Mem", hasSideEffects: true},                 // Store arg1 to *arg0.  arg2=memory.  Lock release, returns memory.
	{name: "AtomicStoreRel64", argLength: 3, typ: "Mem", hasSideEffects: true},                 // Store arg1 to *arg0.  arg2=memory.  Lock release, returns memory.
	{name: "AtomicExchange8", argLength: 3, typ: "(UInt8,Mem)", hasSideEffects: true},          // Store arg1 to *arg0.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicExchange32", argLength: 3, typ: "(UInt32,Mem)", hasSideEffects: true},        // Store arg1 to *arg0.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicExchange64", argLength: 3, typ: "(UInt64,Mem)", hasSideEffects: true},        // Store arg1 to *arg0.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicAdd32", argLength: 3, typ: "(UInt32,Mem)", hasSideEffects: true},             // Do *arg0 += arg1.  arg2=memory.  Returns sum and new memory.
	{name: "AtomicAdd64", argLength: 3, typ: "(UInt64,Mem)", hasSideEffects: true},             // Do *arg0 += arg1.  arg2=memory.  Returns sum and new memory.
	{name: "AtomicCompareAndSwap32", argLength: 4, typ: "(Bool,Mem)", hasSideEffects: true},    // if *arg0==arg1, then set *arg0=arg2.  Returns true if store happens and new memory.
	{name: "AtomicCompareAndSwap64", argLength: 4, typ: "(Bool,Mem)", hasSideEffects: true},    // if *arg0==arg1, then set *arg0=arg2.  Returns true if store happens and new memory.
	{name: "AtomicCompareAndSwapRel32", argLength: 4, typ: "(Bool,Mem)", hasSideEffects: true}, // if *arg0==arg1, then set *arg0=arg2.  Lock release, reports whether store happens and new memory.

	// Older atomic logical operations which don't return the old value.
	{name: "AtomicAnd8", argLength: 3, typ: "Mem", hasSideEffects: true},  // *arg0 &= arg1.  arg2=memory.  Returns memory.
	{name: "AtomicOr8", argLength: 3, typ: "Mem", hasSideEffects: true},   // *arg0 |= arg1.  arg2=memory.  Returns memory.
	{name: "AtomicAnd32", argLength: 3, typ: "Mem", hasSideEffects: true}, // *arg0 &= arg1.  arg2=memory.  Returns memory.
	{name: "AtomicOr32", argLength: 3, typ: "Mem", hasSideEffects: true},  // *arg0 |= arg1.  arg2=memory.  Returns memory.

	// Newer atomic logical operations which return the old value.
	{name: "AtomicAnd64value", argLength: 3, typ: "(Uint64, Mem)", hasSideEffects: true}, // *arg0 &= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicAnd32value", argLength: 3, typ: "(Uint32, Mem)", hasSideEffects: true}, // *arg0 &= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicAnd8value", argLength: 3, typ: "(Uint8, Mem)", hasSideEffects: true},   // *arg0 &= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicOr64value", argLength: 3, typ: "(Uint64, Mem)", hasSideEffects: true},  // *arg0 |= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicOr32value", argLength: 3, typ: "(Uint32, Mem)", hasSideEffects: true},  // *arg0 |= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicOr8value", argLength: 3, typ: "(Uint8, Mem)", hasSideEffects: true},    // *arg0 |= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.

	// Atomic operation variants
	// These variants have the same semantics as above atomic operations.
	// But they are used for generating more efficient code on certain modern machines, with run-time CPU feature detection.
	// On ARM64, these are used when the LSE hardware feature is available (either known at compile time or detected at runtime). If LSE is not available,
	// then the basic atomic operations are used instead.
	{name: "AtomicStore8Variant", argLength: 3, typ: "Mem", hasSideEffects: true},  // Store arg1 to *arg0.  arg2=memory.  Returns memory.
	{name: "AtomicStore32Variant", argLength: 3, typ: "Mem", hasSideEffects: true}, // Store arg1 to *arg0.  arg2=memory.  Returns memory.
	{name: "AtomicStore64Variant", argLength: 3, typ: "Mem", hasSideEffects: true}, // Store arg1 to *arg0.  arg2=memory.  Returns memory.

	{name: "AtomicAdd32Variant", argLength: 3, typ: "(UInt32,Mem)", hasSideEffects: true},          // Do *arg0 += arg1.  arg2=memory.  Returns sum and new memory.
	{name: "AtomicAdd64Variant", argLength: 3, typ: "(UInt64,Mem)", hasSideEffects: true},          // Do *arg0 += arg1.  arg2=memory.  Returns sum and new memory.
	{name: "AtomicExchange8Variant", argLength: 3, typ: "(UInt8,Mem)", hasSideEffects: true},       // Store arg1 to *arg0.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicExchange32Variant", argLength: 3, typ: "(UInt32,Mem)", hasSideEffects: true},     // Store arg1 to *arg0.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicExchange64Variant", argLength: 3, typ: "(UInt64,Mem)", hasSideEffects: true},     // Store arg1 to *arg0.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicCompareAndSwap32Variant", argLength: 4, typ: "(Bool,Mem)", hasSideEffects: true}, // if *arg0==arg1, then set *arg0=arg2.  Returns true if store happens and new memory.
	{name: "AtomicCompareAndSwap64Variant", argLength: 4, typ: "(Bool,Mem)", hasSideEffects: true}, // if *arg0==arg1, then set *arg0=arg2.  Returns true if store happens and new memory.
	{name: "AtomicAnd64valueVariant", argLength: 3, typ: "(Uint64, Mem)", hasSideEffects: true},    // *arg0 &= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicOr64valueVariant", argLength: 3, typ: "(Uint64, Mem)", hasSideEffects: true},     // *arg0 |= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicAnd32valueVariant", argLength: 3, typ: "(Uint32, Mem)", hasSideEffects: true},    // *arg0 &= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicOr32valueVariant", argLength: 3, typ: "(Uint32, Mem)", hasSideEffects: true},     // *arg0 |= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicAnd8valueVariant", argLength: 3, typ: "(Uint8, Mem)", hasSideEffects: true},      // *arg0 &= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.
	{name: "AtomicOr8valueVariant", argLength: 3, typ: "(Uint8, Mem)", hasSideEffects: true},       // *arg0 |= arg1.  arg2=memory.  Returns old contents of *arg0 and new memory.

	// Publication barrier
	{name: "PubBarrier", argLength: 1, hasSideEffects: true}, // Do data barrier. arg0=memory.

	// Clobber experiment op
	{name: "Clobber", argLength: 0, typ: "Void", aux: "SymOff", symEffect: "None"}, // write an invalid pointer value to the given pointer slot of a stack variable
	{name: "ClobberReg", argLength: 0, typ: "Void"},                                // clobber a register

	// Prefetch instruction
	{name: "PrefetchCache", argLength: 2, hasSideEffects: true},         // Do prefetch arg0 to cache. arg0=addr, arg1=memory.
	{name: "PrefetchCacheStreamed", argLength: 2, hasSideEffects: true}, // Do non-temporal or streamed prefetch arg0 to cache. arg0=addr, arg1=memory.

	// Helper instruction which is semantically equivalent to calling runtime.memequal, but some targets may prefer to custom lower it later, e.g. for specific constant sizes.
	{name: "MemEq", argLength: 4, commutative: true, typ: "Bool"}, // arg0=ptr0, arg1=ptr1, arg2=size, arg3=memory.

	// gd: runtime.streqfast of two strings, given by their 2 words
	// (arg0..arg1 and arg2..arg3); arg4=memory. Like MemEq it writes no
	// memory (a string's bytes cannot change), so loads around it are
	// not done again. arm64 calls runtime.streqmixed, a leaf.
	{name: "StrEqFast", argLength: 5, typ: "Bool"},

	// Value of a zero-sized type.
	{name: "Empty", argLength: 0},

	// SIMD
	{name: "ZeroSIMD", argLength: 0}, // zero value of a vector

	// Convert integers to masks
	{name: "Cvt16toMask8x16", argLength: 1},  // arg0 = integer mask value
	{name: "Cvt32toMask8x32", argLength: 1},  // arg0 = integer mask value
	{name: "Cvt64toMask8x64", argLength: 1},  // arg0 = integer mask value
	{name: "Cvt8toMask16x8", argLength: 1},   // arg0 = integer mask value
	{name: "Cvt16toMask16x16", argLength: 1}, // arg0 = integer mask value
	{name: "Cvt32toMask16x32", argLength: 1}, // arg0 = integer mask value
	{name: "Cvt8toMask32x4", argLength: 1},   // arg0 = integer mask value
	{name: "Cvt8toMask32x8", argLength: 1},   // arg0 = integer mask value
	{name: "Cvt16toMask32x16", argLength: 1}, // arg0 = integer mask value
	{name: "Cvt8toMask64x2", argLength: 1},   // arg0 = integer mask value
	{name: "Cvt8toMask64x4", argLength: 1},   // arg0 = integer mask value
	{name: "Cvt8toMask64x8", argLength: 1},   // arg0 = integer mask value

	// Convert masks to integers
	{name: "CvtMask8x16to16", argLength: 1},  // arg0 = mask
	{name: "CvtMask8x32to32", argLength: 1},  // arg0 = mask
	{name: "CvtMask8x64to64", argLength: 1},  // arg0 = mask
	{name: "CvtMask16x8to8", argLength: 1},   // arg0 = mask
	{name: "CvtMask16x16to16", argLength: 1}, // arg0 = mask
	{name: "CvtMask16x32to32", argLength: 1}, // arg0 = mask
	{name: "CvtMask32x4to8", argLength: 1},   // arg0 = mask
	{name: "CvtMask32x8to8", argLength: 1},   // arg0 = mask
	{name: "CvtMask32x16to16", argLength: 1}, // arg0 = mask
	{name: "CvtMask64x2to8", argLength: 1},   // arg0 = mask
	{name: "CvtMask64x4to8", argLength: 1},   // arg0 = mask
	{name: "CvtMask64x8to8", argLength: 1},   // arg0 = mask

	// Returns true if arg0 is all zero.
	{name: "IsZeroVec", argLength: 1},

	// Returns a mask indicating whether arg0's elements are NaN.
	{name: "IsNaNFloat32x4", argLength: 1},
	{name: "IsNaNFloat32x8", argLength: 1},
	{name: "IsNaNFloat32x16", argLength: 1},
	{name: "IsNaNFloat64x2", argLength: 1},
	{name: "IsNaNFloat64x4", argLength: 1},
	{name: "IsNaNFloat64x8", argLength: 1},
}

//     kind          controls          successors   implicit exit
//   ------------------------------------------------------------
//     Exit      [return mem]                  []             yes
//      Ret      [return mem]                  []             yes
//   RetJmp      [return mem]                  []             yes
//    Plain                []              [next]
//       If   [boolean Value]        [then, else]
//    First                []     [always, never]
//    Defer             [mem] [nopanic, recovery]                  (control opcode should be OpStaticCall to runtime.defer*)
// JumpTable   [integer Value]   [succ1,succ2,..]

var genericBlocks = []blockData{
	{name: "Plain"},                  // a single successor
	{name: "If", controls: 1},        // if Controls[0] goto Succs[0] else goto Succs[1]
	{name: "Defer", controls: 1},     // Succs[0]=defer queued, Succs[1]=defer recovery branch (jmp performed by runtime). Controls[0] is call op (of memory type).
	{name: "Ret", controls: 1},       // no successors, Controls[0] value is memory result
	{name: "RetJmp", controls: 1},    // no successors, Controls[0] value is a tail call
	{name: "Exit", controls: 1},      // no successors, Controls[0] value generates a panic
	{name: "JumpTable", controls: 1}, // multiple successors, the integer Controls[0] selects which one

	// transient block state used for dead code removal
	{name: "First"}, // 2 successors, always takes the first one (second is dead)
}

func init() {
	genericOps = append(genericOps, simdGenericOps()...)
	// When adding SIMD for another architecture, it may be useful to temporarily
	// maintain a separate list of generic operations till that work stabilizes.
	// For example:
	// genericOps = append(genericOps, simdGenericOpsWasm()...)
	archs = append(archs, arch{
		name:    "generic",
		ops:     genericOps,
		blocks:  genericBlocks,
		generic: true,
	})
}
