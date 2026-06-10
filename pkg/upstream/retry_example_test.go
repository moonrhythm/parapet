package upstream_test

import (
	"net/http"

	"github.com/moonrhythm/parapet/pkg/upstream"
)

// ExampleUpstream_retryPolicy widens retry eligibility to idempotent PUT and DELETE.
// The default policy retries only GET/HEAD/OPTIONS/TRACE (and only when a body, if
// present, is rewindable via GetBody). A custom RetryPolicy lets the operator opt in
// to methods they know their upstream applies idempotently — but a retried request
// can hit upstreams up to Retries+1 times, so only widen this when the upstream is
// genuinely idempotent for the method.
func ExampleUpstream_retryPolicy() {
	up := upstream.SingleHost("backend:8080", &upstream.HTTPTransport{})
	up.RetryPolicy = func(r *http.Request) bool {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
			// A body-bearing PUT is still only safe to retry when it can be rewound;
			// require GetBody so each re-attempt resends the full body.
			return r.Body == nil || r.Body == http.NoBody || r.GetBody != nil
		default:
			return false
		}
	}
	_ = up
	// Output:
}
