// Package cond evaluates the small boolean expression language used by step
// `if:` / `elif:` conditions. It is deliberately tiny and shell-free: the
// engine evaluates conditions on the host against a step's merged env, before
// the step runs.
//
// Grammar (precedence low→high: || , && , unary !, comparison):
//
//	or      := and ( '||' and )*
//	and     := unary ( '&&' unary )*
//	unary   := '!' unary | primary
//	primary := '(' or ')' | value ( ('=='|'!=') value )?
//	value   := '$' NAME | '${' NAME '}' | "'" … "'" | '"' … '"' | BAREWORD
//
// A `$VAR`/`${VAR}` resolves from the env (missing → ""); quoted and bareword
// values are literals. A lone value (no comparison) is truthy when its resolved
// text is non-empty and not "false" (any case) and not "0".
//
//	$LATCHET_LOCATION == server
//	$LATCHET_GIT_BRANCH == main && $LATCHET_LOCATION != local
//	!($CI == true)
package cond

import (
	"fmt"
	"strings"
)

// Check parses expr and reports a syntax error, without evaluating it. Used for
// validating workflow conditions at load time.
func Check(expr string) error {
	_, err := parse(expr)
	return err
}

// Eval parses and evaluates expr against env. A syntax error is returned (it
// should have been caught by Check at validation time).
func Eval(expr string, env map[string]string) (bool, error) {
	n, err := parse(expr)
	if err != nil {
		return false, err
	}
	return n.eval(env), nil
}

// --- AST ---

type node interface {
	eval(env map[string]string) bool
}

type orNode struct{ l, r node }

func (n orNode) eval(env map[string]string) bool { return n.l.eval(env) || n.r.eval(env) }

type andNode struct{ l, r node }

func (n andNode) eval(env map[string]string) bool { return n.l.eval(env) && n.r.eval(env) }

type notNode struct{ x node }

func (n notNode) eval(env map[string]string) bool { return !n.x.eval(env) }

type cmpNode struct {
	l, r val
	eq   bool // true for ==, false for !=
}

func (n cmpNode) eval(env map[string]string) bool {
	equal := n.l.resolve(env) == n.r.resolve(env)
	if n.eq {
		return equal
	}
	return !equal
}

type truthyNode struct{ v val }

func (n truthyNode) eval(env map[string]string) bool { return truthy(n.v.resolve(env)) }

type val interface {
	resolve(env map[string]string) string
}

type varVal struct{ name string }

func (v varVal) resolve(env map[string]string) string { return env[v.name] }

type litVal struct{ s string }

func (l litVal) resolve(map[string]string) string { return l.s }

func truthy(s string) bool {
	return s != "" && !strings.EqualFold(s, "false") && s != "0"
}

// --- tokens ---

type tokKind int

const (
	tEOF tokKind = iota
	tOr
	tAnd
	tNot
	tEq
	tNe
	tLParen
	tRParen
	tVar // val = variable name
	tStr // val = literal string
)

type token struct {
	kind tokKind
	val  string
}

func lex(s string) ([]token, error) {
	var toks []token
	i, n := 0, len(s)
	isOp := func(c byte) bool {
		switch c {
		case ' ', '\t', '\n', '|', '&', '=', '!', '(', ')', '$', '\'', '"':
			return true
		}
		return false
	}
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '|':
			if i+1 < n && s[i+1] == '|' {
				toks = append(toks, token{kind: tOr})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected %q (did you mean ||?)", "|")
			}
		case c == '&':
			if i+1 < n && s[i+1] == '&' {
				toks = append(toks, token{kind: tAnd})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected %q (did you mean &&?)", "&")
			}
		case c == '=':
			if i+1 < n && s[i+1] == '=' {
				toks = append(toks, token{kind: tEq})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected %q (use == for equality)", "=")
			}
		case c == '!':
			if i+1 < n && s[i+1] == '=' {
				toks = append(toks, token{kind: tNe})
				i += 2
			} else {
				toks = append(toks, token{kind: tNot})
				i++
			}
		case c == '(':
			toks = append(toks, token{kind: tLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen})
			i++
		case c == '$':
			i++
			braced := i < n && s[i] == '{'
			if braced {
				i++
			}
			start := i
			for i < n && !isOp(s[i]) && s[i] != '}' {
				i++
			}
			name := s[start:i]
			if braced {
				if i >= n || s[i] != '}' {
					return nil, fmt.Errorf("unterminated ${...}")
				}
				i++
			}
			if name == "" {
				return nil, fmt.Errorf("empty variable reference after $")
			}
			toks = append(toks, token{kind: tVar, val: name})
		case c == '\'' || c == '"':
			quote := c
			i++
			start := i
			for i < n && s[i] != quote {
				i++
			}
			if i >= n {
				return nil, fmt.Errorf("unterminated %c-quoted string", quote)
			}
			toks = append(toks, token{kind: tStr, val: s[start:i]})
			i++ // closing quote
		default:
			start := i
			for i < n && !isOp(s[i]) {
				i++
			}
			toks = append(toks, token{kind: tStr, val: s[start:i]})
		}
	}
	return append(toks, token{kind: tEOF}), nil
}

// --- parser ---

type parser struct {
	toks []token
	pos  int
}

func parse(expr string) (node, error) {
	toks, err := lex(expr)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing input in condition")
	}
	return n, nil
}

func (p *parser) cur() token  { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) parseOr() (node, error) {
	n, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tOr {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		n = orNode{n, r}
	}
	return n, nil
}

func (p *parser) parseAnd() (node, error) {
	n, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tAnd {
		p.next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		n = andNode{n, r}
	}
	return n, nil
}

func (p *parser) parseUnary() (node, error) {
	if p.cur().kind == tNot {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notNode{x}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (node, error) {
	if p.cur().kind == tLParen {
		p.next()
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tRParen {
			return nil, fmt.Errorf("missing ')'")
		}
		p.next()
		return n, nil
	}
	l, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	if k := p.cur().kind; k == tEq || k == tNe {
		p.next()
		r, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return cmpNode{l: l, r: r, eq: k == tEq}, nil
	}
	return truthyNode{l}, nil
}

func (p *parser) parseValue() (val, error) {
	t := p.next()
	switch t.kind {
	case tVar:
		return varVal{t.val}, nil
	case tStr:
		return litVal{t.val}, nil
	default:
		return nil, fmt.Errorf("expected a value in condition")
	}
}
