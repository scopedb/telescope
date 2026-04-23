package scopeql

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Expr interface {
	ScopeQL() string
}

type rawExpr struct {
	value string
}

func Raw(value string) Expr {
	return rawExpr{value: value}
}

func (e rawExpr) ScopeQL() string {
	return e.value
}

type refExpr struct {
	name string
}

func Ref(name string) Expr {
	return refExpr{name: name}
}

func (e refExpr) ScopeQL() string {
	return e.name
}

type stringExpr struct {
	value string
}

func String(value string) Expr {
	return stringExpr{value: value}
}

func (e stringExpr) ScopeQL() string {
	return "'" + strings.ReplaceAll(e.value, "'", "''") + "'"
}

type intExpr struct {
	value int64
}

func Int(value int64) Expr {
	return intExpr{value: value}
}

func (e intExpr) ScopeQL() string {
	return strconv.FormatInt(e.value, 10)
}

type floatExpr struct {
	value float64
}

func Float(value float64) Expr {
	return floatExpr{value: value}
}

func (e floatExpr) ScopeQL() string {
	return strconv.FormatFloat(e.value, 'f', -1, 64)
}

type boolExpr struct {
	value bool
}

func Bool(value bool) Expr {
	return boolExpr{value: value}
}

func (e boolExpr) ScopeQL() string {
	if e.value {
		return "true"
	}
	return "false"
}

func Literal(value any) (Expr, error) {
	switch v := value.(type) {
	case Expr:
		return v, nil
	case string:
		return String(v), nil
	case int:
		return Int(int64(v)), nil
	case int8:
		return Int(int64(v)), nil
	case int16:
		return Int(int64(v)), nil
	case int32:
		return Int(int64(v)), nil
	case int64:
		return Int(v), nil
	case float32:
		return Float(float64(v)), nil
	case float64:
		return Float(v), nil
	case bool:
		return Bool(v), nil
	case time.Time:
		return Raw(String(v.UTC().Format(time.RFC3339Nano)).ScopeQL() + "::timestamp"), nil
	default:
		return nil, fmt.Errorf("unsupported literal type %T", value)
	}
}

type callExpr struct {
	name string
	args []Expr
}

func Call(name string, args ...Expr) Expr {
	return callExpr{name: name, args: args}
}

func (e callExpr) ScopeQL() string {
	args := make([]string, 0, len(e.args))
	for _, arg := range e.args {
		args = append(args, arg.ScopeQL())
	}
	return fmt.Sprintf("%s(%s)", e.name, strings.Join(args, ", "))
}

type binaryExpr struct {
	left  Expr
	op    string
	right Expr
}

func Binary(left Expr, op string, right Expr) Expr {
	return binaryExpr{left: left, op: op, right: right}
}

func Eq(left Expr, right Expr) Expr {
	return Binary(left, "=", right)
}

func Ne(left Expr, right Expr) Expr {
	return Binary(left, "!=", right)
}

func Gt(left Expr, right Expr) Expr {
	return Binary(left, ">", right)
}

func Gte(left Expr, right Expr) Expr {
	return Binary(left, ">=", right)
}

func Lt(left Expr, right Expr) Expr {
	return Binary(left, "<", right)
}

func Lte(left Expr, right Expr) Expr {
	return Binary(left, "<=", right)
}

func (e binaryExpr) ScopeQL() string {
	return fmt.Sprintf("%s %s %s", e.left.ScopeQL(), e.op, e.right.ScopeQL())
}

type logicalExpr struct {
	op    string
	exprs []Expr
}

type unaryExpr struct {
	op   string
	expr Expr
}

func Not(expr Expr) Expr {
	return unaryExpr{op: "NOT", expr: expr}
}

func (e unaryExpr) ScopeQL() string {
	return e.op + " (" + e.expr.ScopeQL() + ")"
}

func And(exprs ...Expr) Expr {
	return logicalExpr{op: "AND", exprs: exprs}
}

func Or(exprs ...Expr) Expr {
	return logicalExpr{op: "OR", exprs: exprs}
}

func (e logicalExpr) ScopeQL() string {
	if len(e.exprs) == 0 {
		return ""
	}
	if len(e.exprs) == 1 {
		return e.exprs[0].ScopeQL()
	}

	parts := make([]string, 0, len(e.exprs))
	for _, expr := range e.exprs {
		parts = append(parts, "("+expr.ScopeQL()+")")
	}
	return strings.Join(parts, " "+e.op+" ")
}

type inExpr struct {
	left  Expr
	right []Expr
}

func In(left Expr, right ...Expr) Expr {
	return inExpr{left: left, right: right}
}

func (e inExpr) ScopeQL() string {
	parts := make([]string, 0, len(e.right))
	for _, expr := range e.right {
		parts = append(parts, expr.ScopeQL())
	}
	return fmt.Sprintf("%s IN (%s)", e.left.ScopeQL(), strings.Join(parts, ", "))
}

func IsNotNull(expr Expr) Expr {
	return Raw(expr.ScopeQL() + " IS NOT NULL")
}
