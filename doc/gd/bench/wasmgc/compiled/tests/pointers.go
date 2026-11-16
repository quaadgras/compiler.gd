package main

type Vec3 struct{ X, Y, Z float32 }

func (v *Vec3) Add(o *Vec3) {
	v.X += o.X
	v.Y += o.Y
	v.Z += o.Z
}

//go:noinline
func scale(v *Vec3, f float32) {
	v.X *= f
	v.Y *= f
	v.Z *= f
}

type Vertex struct {
	Pos   Vec3
	Color [4]uint8
	ID    int32
}

type Body struct {
	Pos, Vel Vec3
	Name     string
	Next     *Body
	M        [4]float32
}

//go:noinline
func bump(p *int32) { *p += 7 }

//go:noinline
func sum(a []float32) (s float32) {
	for _, x := range a {
		s += x
	}
	return
}

//go:noinline
func first(m *[4]float32) *float32 { return &m[1] }

func main() {
	// Pointers into a struct with pointers.
	b := &Body{Name: "a body with a long name", Vel: Vec3{1, 2, 3}}
	b.Pos.Add(&b.Vel)
	scale(&b.Pos, 2)
	b.M[2] = 5
	*first(&b.M) = 9
	println("body", b.Pos.X, b.Pos.Y, b.Pos.Z, b.M[1], b.M[2], sum(b.M[:]), b.Name)

	// A slice of structs without pointers: linear memory.
	vs := make([]Vertex, 3)
	for i := range vs {
		vs[i].Pos = Vec3{float32(i), 1, 2}
		vs[i].ID = int32(i * 10)
		vs[i].Color[i] = uint8(200 + i)
	}
	vs = append(vs, Vertex{Pos: Vec3{9, 9, 9}, ID: 99})
	scale(&vs[1].Pos, 10)
	bump(&vs[2].ID)
	vs[3].Pos.Add(&vs[1].Pos)
	var ids int32
	var cols int
	for i := range vs {
		ids += vs[i].ID
		for _, c := range vs[i].Color {
			cols += int(c)
		}
	}
	println("verts", len(vs), cap(vs), vs[1].Pos.X, vs[3].Pos.X, vs[3].Pos.Z, ids, cols)
	tail := vs[2:]
	tail[0].ID = -5
	println("tail", len(tail), vs[2].ID, tail[1].ID)

	// Bytes.
	buf := make([]byte, 0, 4)
	for i := 0; i < 40; i++ {
		buf = append(buf, byte('a'+i%26))
	}
	n := 0
	for _, c := range buf[10:30] {
		n += int(c)
	}
	println("bytes", len(buf), n, buf[39])

	// Equality of structs, a pointer to a local, an array of structs.
	x, y := Vertex{ID: 3}, Vertex{ID: 3}
	println("eq", x == y, x.Pos == Vec3{}, b.Pos == Vec3{2, 4, 6})
	var k int32 = 35
	bump(&k)
	var arr [3]Vec3
	scale(&arr[1], 2)
	arr[2].Y = 4
	arr[1].Add(&arr[2])
	println("locals", k, arr[1].Y, arr[2].Y)
}
