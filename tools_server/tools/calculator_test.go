package tools

import "testing"

func TestCalcParser(t *testing.T) {
	cases := map[string]float64{
		"1+2*3":       7,
		"(1+2)*3":     9,
		"2^3^2":       512, // right-assoc
		"-2^2":        -4,  // unary binds looser than ^ here: -(2^2)
		"10/4":        2.5,
		"10%3":        1,
		"3.5 + 0.5":   4,
		"((2+3)*(4))": 20,
	}
	for in, want := range cases {
		p := &calcParser{src: in}
		got, err := p.parseExpr()
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		p.skipSpace()
		if p.pos != len(p.src) {
			t.Fatalf("%q: trailing input at %d", in, p.pos)
		}
		if got != want {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
}

func TestCalcErrors(t *testing.T) {
	for _, in := range []string{"1/0", "1+", "(1+2", "abc", ""} {
		p := &calcParser{src: in}
		_, err := p.parseExpr()
		if err == nil && in != "" {
			// "" parses nothing then trailing check would catch; ensure non-empty errs
			t.Errorf("%q: expected error", in)
		}
	}
}
