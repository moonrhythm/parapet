package waf

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

// requestVar is the single top-level variable exposed to every WAF rule.
const requestVar = "request"

// listType is the CEL type used by helpers that take a list of strings.
var listType = cel.ListType(cel.StringType)

// regexCache caches compiled regexes used by the regexMatch CEL function.
// CEL programs are shared and concurrent, so the cache must be safe for
// concurrent access. sync.Map fits the read-mostly access pattern.
var regexCache sync.Map // pattern -> *regexp.Regexp or compileError

type regexCompileError struct{ err error }

func compileRegex(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		switch t := v.(type) {
		case *regexp.Regexp:
			return t, nil
		case regexCompileError:
			return nil, t.err
		}
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		regexCache.Store(pattern, regexCompileError{err: err})
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// cidrCache caches parsed CIDR blocks for ipInCidr.
var cidrCache sync.Map // cidr -> *net.IPNet or parseError

type cidrParseError struct{ err error }

func parseCIDR(cidr string) (*net.IPNet, error) {
	if v, ok := cidrCache.Load(cidr); ok {
		switch t := v.(type) {
		case *net.IPNet:
			return t, nil
		case cidrParseError:
			return nil, t.err
		}
	}
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		cidrCache.Store(cidr, cidrParseError{err: err})
		return nil, err
	}
	cidrCache.Store(cidr, n)
	return n, nil
}

// newCELEnv builds the CEL environment shared by every Rule.
//
// Keep this small and explicit: every variable and function that is exposed
// to user-supplied rules needs a deliberate decision. The fewer affordances
// we hand out, the smaller the attack surface for both abuse (resource
// exhaustion via expensive expressions) and confusion (rules that compile
// but never match because of a typo in a built-in name).
func newCELEnv(opts ...cel.EnvOption) (*cel.Env, error) {
	base := []cel.EnvOption{
		cel.Variable(requestVar, cel.MapType(cel.StringType, cel.DynType)),

		cel.Function("ipInCidr",
			cel.Overload("ipInCidr_string_string",
				[]*cel.Type{cel.StringType, cel.StringType},
				cel.BoolType,
				cel.BinaryBinding(celIpInCidr),
			),
		),

		cel.Function("regexMatch",
			cel.Overload("regexMatch_string_string",
				[]*cel.Type{cel.StringType, cel.StringType},
				cel.BoolType,
				cel.BinaryBinding(celRegexMatch),
			),
		),

		cel.Function("containsAny",
			cel.Overload("containsAny_string_list",
				[]*cel.Type{cel.StringType, listType},
				cel.BoolType,
				cel.BinaryBinding(celContainsAny),
			),
		),

		cel.Function("hasPrefixAny",
			cel.Overload("hasPrefixAny_string_list",
				[]*cel.Type{cel.StringType, listType},
				cel.BoolType,
				cel.BinaryBinding(celHasPrefixAny),
			),
		),

		cel.Function("lower",
			cel.Overload("lower_string",
				[]*cel.Type{cel.StringType},
				cel.StringType,
				cel.UnaryBinding(celLower),
			),
		),

		cel.Function("upper",
			cel.Overload("upper_string",
				[]*cel.Type{cel.StringType},
				cel.StringType,
				cel.UnaryBinding(celUpper),
			),
		),

		cel.Function("urlDecode",
			cel.Overload("urlDecode_string",
				[]*cel.Type{cel.StringType},
				cel.StringType,
				cel.UnaryBinding(celURLDecode),
			),
		),
	}
	base = append(base, opts...)
	return cel.NewEnv(base...)
}

func asString(v ref.Val) (string, bool) {
	s, ok := v.(types.String)
	if !ok {
		return "", false
	}
	return string(s), true
}

func celIpInCidr(ipv, cidrv ref.Val) ref.Val {
	ip, ok := asString(ipv)
	if !ok {
		return types.NewErr("ipInCidr: ip must be string")
	}
	cidr, ok := asString(cidrv)
	if !ok {
		return types.NewErr("ipInCidr: cidr must be string")
	}
	n, err := parseCIDR(cidr)
	if err != nil {
		return types.NewErr("ipInCidr: %v", err)
	}
	addr := net.ParseIP(ip)
	if addr == nil {
		return types.Bool(false)
	}
	return types.Bool(n.Contains(addr))
}

func celRegexMatch(sv, pv ref.Val) ref.Val {
	s, ok := asString(sv)
	if !ok {
		return types.NewErr("regexMatch: input must be string")
	}
	pattern, ok := asString(pv)
	if !ok {
		return types.NewErr("regexMatch: pattern must be string")
	}
	re, err := compileRegex(pattern)
	if err != nil {
		return types.NewErr("regexMatch: %v", err)
	}
	return types.Bool(re.MatchString(s))
}

// listFromRefVal flattens a CEL list ref.Val into a Go []string. Unknown
// element types yield an error rather than being silently coerced — strict
// typing catches rule mistakes early.
func listFromRefVal(v ref.Val) ([]string, error) {
	lister, ok := v.(traits.Lister)
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", v)
	}
	size, ok := lister.Size().(types.Int)
	if !ok {
		return nil, fmt.Errorf("list size not int")
	}
	out := make([]string, 0, int(size))
	it := lister.Iterator()
	for it.HasNext() == types.True {
		el := it.Next()
		s, ok := asString(el)
		if !ok {
			return nil, fmt.Errorf("list element not string: %T", el)
		}
		out = append(out, s)
	}
	return out, nil
}

func celContainsAny(sv, lv ref.Val) ref.Val {
	s, ok := asString(sv)
	if !ok {
		return types.NewErr("containsAny: input must be string")
	}
	list, err := listFromRefVal(lv)
	if err != nil {
		return types.NewErr("containsAny: %v", err)
	}
	for _, sub := range list {
		if sub != "" && strings.Contains(s, sub) {
			return types.Bool(true)
		}
	}
	return types.Bool(false)
}

func celHasPrefixAny(sv, lv ref.Val) ref.Val {
	s, ok := asString(sv)
	if !ok {
		return types.NewErr("hasPrefixAny: input must be string")
	}
	list, err := listFromRefVal(lv)
	if err != nil {
		return types.NewErr("hasPrefixAny: %v", err)
	}
	for _, p := range list {
		if p != "" && strings.HasPrefix(s, p) {
			return types.Bool(true)
		}
	}
	return types.Bool(false)
}

func celLower(v ref.Val) ref.Val {
	s, ok := asString(v)
	if !ok {
		return types.NewErr("lower: input must be string")
	}
	return types.String(strings.ToLower(s))
}

func celUpper(v ref.Val) ref.Val {
	s, ok := asString(v)
	if !ok {
		return types.NewErr("upper: input must be string")
	}
	return types.String(strings.ToUpper(s))
}

func celURLDecode(v ref.Val) ref.Val {
	s, ok := asString(v)
	if !ok {
		return types.NewErr("urlDecode: input must be string")
	}
	dec, err := url.QueryUnescape(s)
	if err != nil {
		return types.String("")
	}
	return types.String(dec)
}
