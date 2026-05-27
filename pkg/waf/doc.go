// Package waf provides a Web Application Firewall middleware for parapet.
//
// Rules are written in the Common Expression Language (CEL,
// https://github.com/google/cel-go) which allows safe, sandboxed expressions
// to be evaluated against incoming HTTP requests without restarting the
// process.
//
// # Quick start
//
//	w := waf.New()
//	_ = w.SetRules([]waf.Rule{{
//		ID:         "block-sqli",
//		Expression: `request.query.contains("' OR '1'='1") || request.path.matches("(?i).*union.*select.*")`,
//		Action:     waf.ActionBlock,
//		Status:     http.StatusForbidden,
//	}})
//
//	srv := parapet.NewFrontend()
//	srv.Use(w)
//
// # Hot reload
//
// Rules can be replaced atomically at runtime via WAF.SetRules. Existing
// in-flight requests continue to use the previous compiled ruleset; new
// requests use the new ruleset on the very next call. Compilation happens
// inside SetRules so the request path is never blocked by parsing or
// type-checking.
//
// # Variables exposed to expressions
//
// The top-level identifier `request` is a map with these fields:
//
//	request.method        string
//	request.host          string
//	request.path          string
//	request.query         string  // raw query string
//	request.uri           string  // request URI
//	request.proto         string  // "HTTP/1.1", "HTTP/2.0", ...
//	request.scheme        string  // "http" or "https"
//	request.remote_ip     string  // best-effort client IP (X-Real-IP -> X-Forwarded-For -> RemoteAddr)
//	request.country       string  // ISO 3166-1 alpha-2 from WAF.Country (e.g. "TH"); "" if unresolved/unset
//	request.asn           int     // autonomous system number from WAF.ASN (e.g. 13335); 0 if unresolved/unset
//	request.content_length int
//	request.headers       map<string, string>  // single value per name (canonicalised, lowercase keys)
//	request.cookies       map<string, string>
//	request.args          map<string, string>  // first value of each query parameter
//	request.user_agent    string
//	request.referer       string
//	request.body          string  // populated only when WAF.InspectBody > 0; truncated to that many bytes
//
// # Custom functions
//
//	ipInCidr(ip, cidr)        bool   // CIDR membership
//	regexMatch(s, pattern)    bool   // pre-compiled regex (cached)
//	containsAny(s, list)      bool   // substring contains any of the list entries
//	hasPrefixAny(s, list)     bool   // hasPrefix any of the list entries
//	lower(s)                  string // ascii lowercase
//	upper(s)                  string // ascii uppercase
//	urlDecode(s)              string // percent-decode (errors return empty string)
//
// # Performance
//
// Each Rule is compiled once when SetRules is called, into a stateless,
// thread-safe cel.Program. Evaluation uses ContextEval with a CostLimit and
// per-request deadline (WAF.EvalTimeout) to keep the request path bounded.
//
// # Security notes
//
//   - Cost limit is enforced (default 1_000_000 units) to prevent runaway rules.
//   - CEL macros (all/exists/filter/map/comprehensions) are kept enabled by
//     default but bounded by the cost limit. Set WAF.DisableMacros = true to
//     refuse rules that try to use them.
//   - Body inspection is OFF by default. Enabling it buffers up to InspectBody
//     bytes; set this conservatively to avoid memory-amplification attacks.
//   - Failed rule compilation returns an error from SetRules; the previous
//     ruleset stays in place.
package waf
