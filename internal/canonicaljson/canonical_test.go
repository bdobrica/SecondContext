package canonicaljson

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestCanonicalJSON(t *testing.T) {
	cases := [][2]string{
		{`{"b":1.0,"a":1e0}`, `{"a":1,"b":1}`},
		{`{"numbers":[333333333.33333329,1E30,4.50,2e-3,0.000000000000000000000000001],"s":"\u20ac\u000f\n\/"}`, "{\"numbers\":[333333333.3333333,1e+30,4.5,0.002,1e-27],\"s\":\"€\\u000f\\n/\"}"},
		{`[-0,1e-6,1e-7,1e20,1e21]`, `[0,0.000001,1e-7,100000000000000000000,1e+21]`},
		{`{"\ufffd":1,"\ud83d\ude00":2,"a":3,"\u0080":4}`, "{\"a\":3,\"\u0080\":4,\"😀\":2,\"�\":1}"},
		{`["\\ud800","\ud800\udc00","\u2028","<>&"]`, "[\"\\\\ud800\",\"𐀀\",\"\u2028\",\"<>&\"]"},
	}
	for _, tc := range cases {
		got, err := Canonicalize([]byte(tc[0]))
		if err != nil || string(got) != tc[1] {
			t.Errorf("canonical %q: %q %v want %q", tc[0], got, err, tc[1])
		}
	}
	for _, bad := range []string{`{"a":1,"\u0061":2}`, `{"x":{"a":1,"a":2}}`, `"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, `1e999`, `{} {}`, "\"\xff\"", `[`, strings.Repeat("[", 102) + "0" + strings.Repeat("]", 102)} {
		if _, err := Canonicalize([]byte(bad)); err == nil {
			t.Errorf("accepted invalid %q", bad)
		}
	}
}

// RFC 8785 Appendix B boundaries exercise Ryu shortest-round-trip formatting.
func TestRFCNumberVectors(t *testing.T) {
	for _, tc := range []struct {
		bits uint64
		want string
	}{
		{0x0000000000000001, "5e-324"}, {0x8000000000000001, "-5e-324"}, {0x7fefffffffffffff, "1.7976931348623157e+308"},
		{0x4340000000000000, "9007199254740992"}, {0x4430000000000000, "295147905179352830000"}, {0x44b52d02c7e14af5, "9.999999999999997e+22"}, {0x44b52d02c7e14af6, "1e+23"}, {0x44b52d02c7e14af7, "1.0000000000000001e+23"},
		{0x444b1ae4d6e2ef4e, "999999999999999700000"}, {0x444b1ae4d6e2ef4f, "999999999999999900000"}, {0x444b1ae4d6e2ef50, "1e+21"},
		{0x3eb0c6f7a0b5ed8c, "9.999999999999997e-7"}, {0x3eb0c6f7a0b5ed8d, "0.000001"}, {0x43143ff3c1cb0959, "1424953923781206.2"},
	} {
		raw := strconv.FormatFloat(math.Float64frombits(tc.bits), 'g', -1, 64)
		got, e := Canonicalize([]byte(raw))
		if e != nil || string(got) != tc.want {
			t.Errorf("%x: %s %v want %s", tc.bits, got, e, tc.want)
		}
	}
}
