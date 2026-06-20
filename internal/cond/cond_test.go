package cond

import "testing"

func TestEval(t *testing.T) {
	env := map[string]string{
		"LATCHET_LOCATION":   "server",
		"LATCHET_GIT_BRANCH": "main",
		"CI":                 "true",
		"EMPTY":              "",
		"FLAG_FALSE":         "false",
		"ZERO":               "0",
	}
	cases := []struct {
		expr string
		want bool
	}{
		{`$LATCHET_LOCATION == server`, true},
		{`$LATCHET_LOCATION == local`, false},
		{`$LATCHET_LOCATION != local`, true},
		{`${LATCHET_LOCATION} == "server"`, true},
		{`$LATCHET_LOCATION == server && $LATCHET_GIT_BRANCH == main`, true},
		{`$LATCHET_LOCATION == server && $LATCHET_GIT_BRANCH == dev`, false},
		{`$LATCHET_LOCATION == local || $LATCHET_GIT_BRANCH == main`, true},
		{`$CI`, true},             // truthy
		{`$EMPTY`, false},         // empty → false
		{`$MISSING`, false},       // missing var → "" → false
		{`!$EMPTY`, true},         // negation
		{`$FLAG_FALSE`, false},    // "false" → false
		{`$ZERO`, false},          // "0" → false
		{`!($CI == false)`, true}, // parens + negation
		{`$LATCHET_LOCATION == server || $LATCHET_LOCATION == local && false`, true}, // && binds tighter
		{`'a b' == "a b"`, true}, // quoted literals with spaces
	}
	for _, c := range cases {
		got, err := Eval(c.expr, env)
		if err != nil {
			t.Errorf("Eval(%q) error: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("Eval(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestCheckRejectsBadSyntax(t *testing.T) {
	bad := []string{
		`$LATCHET_LOCATION = server`, // single =
		`$ == x`,                     // empty var
		`(a == b`,                    // missing )
		`a ==`,                       // missing rhs
		`a && `,                      // missing operand
		`"unterminated`,              // bad quote
		`${unterminated`,             // bad ${
		`a b`,                        // trailing input
	}
	for _, e := range bad {
		if err := Check(e); err == nil {
			t.Errorf("Check(%q) = nil, want syntax error", e)
		}
	}
}

func TestCheckAcceptsValid(t *testing.T) {
	for _, e := range []string{
		`$X == y`,
		`!$X && ($Y == z || $W != q)`,
		`true`,
	} {
		if err := Check(e); err != nil {
			t.Errorf("Check(%q) unexpected error: %v", e, err)
		}
	}
}
