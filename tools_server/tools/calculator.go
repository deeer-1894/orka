package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// calculator evaluates an arithmetic expression with a safe hand-rolled
// recursive-descent parser (no eval): + - * / % ^, parentheses, unary minus,
// decimals. No function calls / variables, so it can't be abused.
func calculator() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		expr := strings.TrimSpace(req.GetString("expression", ""))
		if expr == "" {
			return mcp.NewToolResultError("expression is required"), nil
		}
		p := &calcParser{src: expr}
		v, err := p.parseExpr()
		if err != nil {
			return mcp.NewToolResultError("invalid expression: " + err.Error()), nil
		}
		p.skipSpace()
		if p.pos != len(p.src) {
			return mcp.NewToolResultError("unexpected trailing input"), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("%s = %s", expr, formatNum(v))), nil
	}
}

func formatNum(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

type calcParser struct {
	src string
	pos int
}

func (p *calcParser) skipSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

// expr := term (('+' | '-') term)*
func (p *calcParser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			break
		}
		op := p.src[p.pos]
		if op != '+' && op != '-' {
			break
		}
		p.pos++
		r, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			v += r
		} else {
			v -= r
		}
	}
	return v, nil
}

// term := unary (('*' | '/' | '%') unary)*
func (p *calcParser) parseTerm() (float64, error) {
	v, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			break
		}
		op := p.src[p.pos]
		if op != '*' && op != '/' && op != '%' {
			break
		}
		p.pos++
		r, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			v *= r
		case '/':
			if r == 0 {
				return 0, errors.New("division by zero")
			}
			v /= r
		case '%':
			if r == 0 {
				return 0, errors.New("modulo by zero")
			}
			v = float64(int64(v) % int64(r))
		}
	}
	return v, nil
}

// unary := ('-' | '+') unary | power   (unary binds looser than ^, so -2^2 = -(2^2))
func (p *calcParser) parseUnary() (float64, error) {
	p.skipSpace()
	if p.pos < len(p.src) && (p.src[p.pos] == '-' || p.src[p.pos] == '+') {
		neg := p.src[p.pos] == '-'
		p.pos++
		v, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		if neg {
			return -v, nil
		}
		return v, nil
	}
	return p.parsePower()
}

// power := atom ('^' unary)?   (right-associative; exponent may be unary, e.g. 2^-3)
func (p *calcParser) parsePower() (float64, error) {
	base, err := p.parseAtom()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '^' {
		p.pos++
		exp, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

func (p *calcParser) parseAtom() (float64, error) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return 0, errors.New("unexpected end")
	}
	if p.src[p.pos] == '(' {
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return 0, errors.New("missing ')'")
		}
		p.pos++
		return v, nil
	}
	start := p.pos
	for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '.') {
		p.pos++
	}
	if p.pos == start {
		return 0, fmt.Errorf("unexpected %q", p.src[p.pos])
	}
	return strconv.ParseFloat(p.src[start:p.pos], 64)
}

