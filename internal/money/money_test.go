package money

import "testing"

func TestParseMicro(t *testing.T) {
	cases := []struct {
		in      string
		want    Micro
		wantErr bool
	}{
		// Real DigiKey values: five decimal places on unit prices.
		{in: "29.40000", want: 29_400_000},
		{in: "0.00243", want: 2_430},
		{in: "0.01351", want: 13_510},
		{in: "7.0", want: 7_000_000},
		{in: "0.10", want: 100_000},

		{in: "0", want: 0},
		{in: "1", want: 1_000_000},
		{in: ".5", want: 500_000},
		{in: "-1.25", want: -1_250_000},
		{in: "+2.5", want: 2_500_000},
		{in: "  3.75  ", want: 3_750_000},
		{in: "$4.20", want: 4_200_000},
		{in: "1,234.56", want: 1_234_560_000},
		{in: "0.000001", want: 1},

		// Seven decimals would have to round, and silently rounding money is
		// how you end up explaining a total that does not match the cart.
		{in: "0.0000001", wantErr: true},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "1.2.3", wantErr: true},
		{in: "1e5", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseMicro(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMicro(%q): want error, got %d", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMicro(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMicro(%q): want %d, got %d", tc.in, tc.want, got)
		}
	}
}

func TestString_RoundsToCents(t *testing.T) {
	cases := []struct {
		in   Micro
		want string
	}{
		{in: 0, want: "0.00"},
		{in: 100_000, want: "0.10"},
		{in: 320_000, want: "0.32"},
		{in: 7_000_000, want: "7.00"},
		{in: 7_320_000, want: "7.32"},
		{in: 29_400_000, want: "29.40"},
		// 12.1257 rounds to 12.13.
		{in: 12_125_700, want: "12.13"},
		// Half rounds away from zero.
		{in: 5_000, want: "0.01"},
		{in: 4_999, want: "0.00"},
		{in: -1_250_000, want: "-1.25"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("Micro(%d).String(): want %s, got %s", tc.in, tc.want, got)
		}
	}
}

// Unit prices must keep their sub-cent digits. Rendering 0.00243 as "0.00"
// makes a real per-unit price look wrong.
func TestExact_KeepsSubCentDigits(t *testing.T) {
	cases := []struct {
		in   Micro
		want string
	}{
		{in: 0, want: "0"},
		{in: 2_430, want: "0.00243"},
		{in: 13_510, want: "0.01351"},
		{in: 100_000, want: "0.1"},
		{in: 29_400_000, want: "29.4"},
		{in: 1, want: "0.000001"},
		{in: -2_430, want: "-0.00243"},
	}
	for _, tc := range cases {
		if got := tc.in.Exact(); got != tc.want {
			t.Errorf("Micro(%d).Exact(): want %s, got %s", tc.in, tc.want, got)
		}
	}
}

// The whole reason this type exists: summing a sub-cent unit price across a
// realistic quantity must be exact. In float64, 0.00243 * 5000 does not land
// on 12.15.
func TestMulQty_IsExactAcrossManyUnits(t *testing.T) {
	unit, err := ParseMicro("0.00243")
	if err != nil {
		t.Fatal(err)
	}
	if got := unit.MulQty(5000); got != 12_150_000 {
		t.Fatalf("want 12150000 micro (12.15), got %d", got)
	}
	if got := unit.MulQty(5000).String(); got != "12.15" {
		t.Fatalf("want 12.15, got %s", got)
	}
}

// Accumulating 60 BOM lines must not drift, which is the failure mode float64
// would introduce.
func TestSummation_DoesNotDrift(t *testing.T) {
	unit, err := ParseMicro("0.01351")
	if err != nil {
		t.Fatal(err)
	}
	var total Micro
	for i := 0; i < 60; i++ {
		total += unit.MulQty(100)
	}
	// 60 * 100 * 0.01351 == 81.06 exactly.
	if total != 81_060_000 {
		t.Fatalf("want 81060000 micro, got %d", total)
	}
	if got := total.String(); got != "81.06" {
		t.Fatalf("want 81.06, got %s", got)
	}
}

func TestFromFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want Micro
	}{
		{in: 7.0, want: 7_000_000},
		{in: 29.4, want: 29_400_000},
		{in: 0.00243, want: 2_430},
		{in: 0, want: 0},
	}
	for _, tc := range cases {
		if got := FromFloat(tc.in); got != tc.want {
			t.Errorf("FromFloat(%v): want %d, got %d", tc.in, tc.want, got)
		}
	}
}

func TestFromFloat_NonFiniteIsZeroNotGarbage(t *testing.T) {
	inf := 1.0
	for i := 0; i < 400; i++ {
		inf *= 10
	}
	if got := FromFloat(inf); got != 0 {
		t.Errorf("want infinity to become 0 rather than a garbage amount, got %d", got)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{"0.00243", "0.01351", "29.4", "7", "1234.56"} {
		m, err := ParseMicro(s)
		if err != nil {
			t.Fatalf("ParseMicro(%q): %v", s, err)
		}
		back, err := ParseMicro(m.Exact())
		if err != nil {
			t.Fatalf("ParseMicro(%q): %v", m.Exact(), err)
		}
		if back != m {
			t.Errorf("round trip of %q: %d != %d", s, m, back)
		}
	}
}

// A lone separator is not zero. ".", "-." and "$." previously parsed as $0.00,
// which turns a malformed price cell into a free part.
func TestParseMicro_LoneSeparatorIsNotZero(t *testing.T) {
	for _, s := range []string{".", "-.", "+.", "$."} {
		if got, err := ParseMicro(s); err == nil {
			t.Errorf("ParseMicro(%q) = %d, want an error", s, got)
		}
	}
}

// Overflow must be an error, never a wrapped negative. An unchecked whole*scale
// turned a deliberately huge spend limit into a NEGATIVE amount, which then
// skipped every "limit >= 0" guard and silently disabled the check the caller
// was trying to raise.
func TestParseMicro_OverflowIsAnErrorNotANegative(t *testing.T) {
	for _, s := range []string{"10000000000000", "9223372036854776", "-10000000000000"} {
		got, err := ParseMicro(s)
		if err == nil {
			t.Errorf("ParseMicro(%q) = %d, want an out-of-range error", s, got)
		}
		if got < 0 && err == nil {
			t.Errorf("ParseMicro(%q) wrapped to a negative amount: %d", s, got)
		}
	}
	// A large but representable amount still works.
	if _, err := ParseMicro("1000000.00"); err != nil {
		t.Errorf("a realistic large total must still parse: %v", err)
	}
}
