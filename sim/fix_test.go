package sim

import "testing"

// Readable fractions without floats: half = One/2, etc.
const (
	half    = One / 2
	quarter = One / 4
	third   = One / 3 // 21845 — inexact, and deliberately so
)

func TestFromIntToInt(t *testing.T) {
	for _, i := range []int{0, 1, -1, 7, -7, 32767, -32768} {
		if got := FromInt(i).ToInt(); got != i {
			t.Errorf("FromInt(%d).ToInt() = %d, want %d", i, got, i)
		}
	}
}

func TestToIntFloorsNegatives(t *testing.T) {
	cases := []struct {
		in   Fix
		want int
	}{
		{One, 1},
		{One + 1, 1},
		{half, 0},
		{0, 0},
		{-1, -1},          // smallest negative fraction floors to -1, not 0
		{-half, -1},       // -0.5 -> -1
		{-One, -1},        //
		{-One - half, -2}, // -1.5 -> -2
	}
	for _, c := range cases {
		if got := c.in.ToInt(); got != c.want {
			t.Errorf("Fix(%d).ToInt() = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestMul(t *testing.T) {
	cases := []struct {
		name string
		a, b Fix
		want Fix
	}{
		{"identity", FromInt(7), One, FromInt(7)},
		{"zero", FromInt(7), 0, 0},
		{"1.5x2", One + half, FromInt(2), FromInt(3)},
		{"0.5x0.5", half, half, quarter},
		{"neg x pos", -(One + half), FromInt(2), FromInt(-3)},
		{"neg x neg", FromInt(-3), FromInt(-4), FromInt(12)},
		{"pos x neg", FromInt(3), FromInt(-4), FromInt(-12)},
		{"1/3 x 3 truncates", third, FromInt(3), One - 1},
		{"range bound", FromInt(32767), One, FromInt(32767)},
		{"walk speed x 2", Fix(98304), FromInt(2), FromInt(3)}, // 1.5 units/frame
	}
	for _, c := range cases {
		if got := c.a.Mul(c.b); got != c.want {
			t.Errorf("%s: Fix(%d).Mul(%d) = %d, want %d", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestMulCommutes(t *testing.T) {
	vals := []Fix{0, 1, -1, half, -half, One, -One, third, FromInt(123), FromInt(-4567)}
	for _, a := range vals {
		for _, b := range vals {
			if a.Mul(b) != b.Mul(a) {
				t.Errorf("Fix(%d).Mul(%d) = %d != %d", a, b, a.Mul(b), b.Mul(a))
			}
		}
	}
}

func TestDiv(t *testing.T) {
	cases := []struct {
		name string
		a, b Fix
		want Fix
	}{
		{"identity", FromInt(7), One, FromInt(7)},
		{"3/2", FromInt(3), FromInt(2), One + half},
		{"-3/2", FromInt(-3), FromInt(2), -(One + half)},
		{"3/-2", FromInt(3), FromInt(-2), -(One + half)},
		{"1/4", One, FromInt(4), quarter},
		{"1/3 truncates toward zero", One, FromInt(3), third},
		{"-1/3 truncates toward zero", -One, FromInt(3), -third},
	}
	for _, c := range cases {
		if got := c.a.Div(c.b); got != c.want {
			t.Errorf("%s: Fix(%d).Div(%d) = %d, want %d", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestDivThenToIntFloors(t *testing.T) {
	if got := FromInt(-3).Div(FromInt(2)).ToInt(); got != -2 {
		t.Errorf("(-3/2).ToInt() = %d, want -2", got)
	}
}

func TestMulDivRoundTrip(t *testing.T) {
	// Exact only for powers of two; that is the point of picking them here.
	for _, a := range []Fix{FromInt(1), FromInt(-9), FromInt(1000), half, -quarter} {
		for _, b := range []Fix{FromInt(2), FromInt(-4), FromInt(8)} {
			if got := a.Div(b).Mul(b); got != a {
				t.Errorf("Fix(%d).Div(%d).Mul(%d) = %d, want %d", a, b, b, got, a)
			}
		}
	}
}

func TestAbs(t *testing.T) {
	cases := [][2]Fix{{0, 0}, {1, 1}, {-1, 1}, {One, One}, {-One, One}, {FromInt(-32767), FromInt(32767)}}
	for _, c := range cases {
		if got := c[0].Abs(); got != c[1] {
			t.Errorf("Fix(%d).Abs() = %d, want %d", c[0], got, c[1])
		}
	}
}

// Mul's int64 intermediate is exact; the int32 result is where the format's
// ±32768 limit bites. Pin the boundary so a future refactor to int32 math
// (which would silently overflow here) fails loudly.
func TestMulHoldsFullInt32Range(t *testing.T) {
	cases := []struct {
		a, b, want Fix
	}{
		{FromInt(32767), One, FromInt(32767)},
		{FromInt(-32768), One, FromInt(-32768)},
		{FromInt(181), FromInt(181), FromInt(32761)}, // largest exact square in range
		{FromInt(30000), half, FromInt(15000)},
	}
	for _, c := range cases {
		if got := c.a.Mul(c.b); got != c.want {
			t.Errorf("Fix(%d).Mul(%d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
