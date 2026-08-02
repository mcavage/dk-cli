// Package money represents currency as exact integer micro-units.
//
// Why not float64: this tool reports totals a human compares against a real
// DigiKey cart. DigiKey quotes unit prices to five decimal places (a real
// observed value is 29.40000, and passives run to 0.00394), so cents are not
// enough precision to hold a unit price, and float64 accumulates error across
// the dozens of line items in a BOM. Micro-units (millionths of a dollar) hold
// DigiKey's five decimals exactly and keep every operation in int64.
//
// Overflow is not a practical concern: int64 micro-dollars covers roughly
// +/- 9.2 trillion dollars.
package money

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Micro is an amount in millionths of a currency unit. 1_000_000 == $1.00.
type Micro int64

const scale = 1_000_000

var ErrNotANumber = errors.New("money: not a valid amount")

// ParseMicro converts a decimal string to Micro without going through float64.
//
// It accepts an optional sign, an optional leading currency symbol, digit
// grouping commas, and up to six fractional digits. More than six fractional
// digits is an error rather than a silent rounding, because silently rounding
// money is how you end up explaining a total that does not match a cart.
func ParseMicro(s string) (Micro, error) {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "$")
	t = strings.ReplaceAll(t, ",", "")
	if t == "" {
		return 0, fmt.Errorf("%w: empty", ErrNotANumber)
	}

	neg := false
	switch t[0] {
	case '-':
		neg, t = true, t[1:]
	case '+':
		t = t[1:]
	}

	intPart, fracPart, hasFrac := strings.Cut(t, ".")
	if intPart == "" && !hasFrac {
		return 0, fmt.Errorf("%w: %q", ErrNotANumber, s)
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > 6 {
		return 0, fmt.Errorf("%w: %q has more than 6 decimal places", ErrNotANumber, s)
	}

	whole, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrNotANumber, s)
	}

	var frac int64
	if fracPart != "" {
		padded := fracPart + strings.Repeat("0", 6-len(fracPart))
		frac, err = strconv.ParseInt(padded, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %q", ErrNotANumber, s)
		}
	}

	m := Micro(whole*scale + frac)
	if neg {
		m = -m
	}
	return m, nil
}

// FromFloat converts a float64 to Micro, rounding half away from zero.
//
// This exists only for decoding JSON numbers, which is how DigiKey sends
// prices and fees. Prefer ParseMicro when the source is a string. Never use
// this for arithmetic.
func FromFloat(f float64) Micro {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return Micro(math.Round(f * scale))
}

// MulQty multiplies an amount by a whole quantity.
func (m Micro) MulQty(qty int) Micro { return m * Micro(qty) }

// String renders the amount for display with two decimal places, which is what
// a cart total looks like. It rounds half away from zero and is lossy on
// purpose: use Exact when the sub-cent digits matter.
func (m Micro) String() string {
	neg := m < 0
	if neg {
		m = -m
	}
	// Round to cents.
	cents := (int64(m) + 5000) / 10000
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

// Exact renders the amount with all six decimal places, trailing zeros
// trimmed. Used for unit prices, where DigiKey's own display keeps the extra
// digits and dropping them would make a per-unit price look wrong.
func (m Micro) Exact() string {
	neg := m < 0
	if neg {
		m = -m
	}
	whole := int64(m) / scale
	frac := int64(m) % scale
	s := fmt.Sprintf("%d", whole)
	if frac != 0 {
		f := strings.TrimRight(fmt.Sprintf("%06d", frac), "0")
		s += "." + f
	}
	if neg {
		return "-" + s
	}
	return s
}
