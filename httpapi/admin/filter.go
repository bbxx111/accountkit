package admin

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/user"
)

// filter 语法是 AIP-160 的一个刻意收窄的子集：term (AND term)*，term := field op value。
// 只支持 spec §4.3 列出的字段；不支持 OR / NOT / 括号 / 函数。错误只点名字段与位置，不回显值。

type token struct {
	text   string
	quoted bool
	pos    int // 起始位置（rune 下标），只用于错误信息
}

func invalidFilter(format string, args ...any) *apierror.Error {
	return apierror.New(apierror.StatusInvalidArgument, "INVALID_FILTER", "invalid filter: "+fmt.Sprintf(format, args...))
}

// tokenize 把 filter 切成裸词 / 引号串 / 运算符（= >= <= < >）。
func tokenize(s string) ([]token, *apierror.Error) {
	rs := []rune(s)
	var out []token
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '"':
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			if j >= len(rs) {
				return nil, invalidFilter("unterminated quoted value at position %d", i)
			}
			out = append(out, token{text: string(rs[i+1 : j]), quoted: true, pos: i})
			i = j + 1
		case r == '=' || r == '<' || r == '>':
			if (r == '<' || r == '>') && i+1 < len(rs) && rs[i+1] == '=' {
				out = append(out, token{text: string(rs[i : i+2]), pos: i})
				i += 2
			} else {
				out = append(out, token{text: string(r), pos: i})
				i++
			}
		case r == '!' || r == '(' || r == ')' || r == ':':
			return nil, invalidFilter("unsupported operator or grouping at position %d", i)
		default:
			j := i
			for j < len(rs) && !unicode.IsSpace(rs[j]) && !strings.ContainsRune(`"=<>!():`, rs[j]) {
				j++
			}
			out = append(out, token{text: string(rs[i:j]), pos: i})
			i = j
		}
	}
	return out, nil
}

// parseFilter 解析为 user.UserFilter（IncludeDeleted 由 show_deleted 单独设置，这里始终为 false）。
func parseFilter(s string) (user.UserFilter, *apierror.Error) {
	var f user.UserFilter
	if strings.TrimSpace(s) == "" {
		return f, nil
	}
	toks, e := tokenize(s)
	if e != nil {
		return f, e
	}
	seen := map[string]bool{}
	setKind := func(k enum.IdentityKind, field string, pos int) *apierror.Error {
		if f.IdentityKind != enum.IdentityKindUnspecified && f.IdentityKind != k {
			return invalidFilter("identity conditions must all refer to the same identity kind (field %s at position %d)", field, pos)
		}
		f.IdentityKind = k
		return nil
	}
	for i := 0; ; {
		if i+2 >= len(toks) {
			return f, invalidFilter("expected `field op value` at position %d", posOrEnd(toks, i, s))
		}
		field, op, val := toks[i], toks[i+1], toks[i+2]
		if field.quoted || op.quoted {
			return f, invalidFilter("field name and operator must not be quoted (position %d)", field.pos)
		}
		if val.text == "" {
			return f, invalidFilter("empty value for field %s", field.text)
		}
		key := field.text + op.text
		if seen[key] {
			return f, invalidFilter("field %s with operator %s appears more than once", field.text, op.text)
		}
		seen[key] = true
		switch field.text {
		case "state":
			if op.text != "=" {
				return f, invalidFilter("field state supports only =")
			}
			if seen["state<"] || seen["state>"] || seen["state>="] || seen["state<="] {
				return f, invalidFilter("field state appears more than once")
			}
			st, err := enum.ParseUserState(val.text)
			if err != nil {
				return f, invalidFilter("field state has an invalid value (use UPPER_SNAKE_CASE state names)")
			}
			f.State = &st
		case "create_time":
			t, err := time.Parse(time.RFC3339Nano, val.text)
			if err != nil {
				return f, invalidFilter("field create_time requires an RFC 3339 timestamp")
			}
			switch op.text {
			case ">=":
				f.CreateTimeMin = &t
			case "<":
				f.CreateTimeMax = &t
			default:
				return f, invalidFilter("field create_time supports only >= and <")
			}
		case "identity.phone", "identity.phone_prefix", "identity.phone_suffix", "identity.email", "identity.email_prefix", "identity.email_domain":
			if op.text != "=" {
				return f, invalidFilter("field %s supports only =", field.text)
			}
			kind := enum.IdentityPhone
			if strings.HasPrefix(field.text, "identity.email") {
				kind = enum.IdentityEmail
			}
			if e := setKind(kind, field.text, field.pos); e != nil {
				return f, e
			}
			v := val.text
			switch field.text {
			case "identity.phone", "identity.email":
				if f.Subject != nil {
					return f, invalidFilter("only one of identity.phone / identity.email may be given")
				}
				f.Subject = &v
			case "identity.phone_prefix", "identity.email_prefix":
				if kind == enum.IdentityEmail {
					v = strings.ToLower(v)
				}
				f.HintPrefix = &v
			case "identity.phone_suffix":
				f.HintSuffix = &v
			case "identity.email_domain":
				lower := strings.ToLower(v)
				f.HintSuffix = &lower
			}
		default:
			return f, invalidFilter("unknown field %s at position %d", field.text, field.pos)
		}
		i += 3
		if i == len(toks) {
			return f, nil
		}
		if toks[i].quoted || toks[i].text != "AND" {
			return f, invalidFilter("expected AND at position %d", toks[i].pos)
		}
		i++
		if i == len(toks) {
			return f, invalidFilter("dangling AND at end of filter")
		}
	}
}

func posOrEnd(toks []token, i int, s string) int {
	if i < len(toks) {
		return toks[i].pos
	}
	return len([]rune(s))
}

// parseShowDeleted 解析 AIP-164 show_deleted（缺省 false）。
func parseShowDeleted(raw string) (bool, *apierror.Error) {
	switch raw {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, apierror.New(apierror.StatusInvalidArgument, "INVALID_SHOW_DELETED", "show_deleted must be true or false")
	}
}
