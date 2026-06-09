package upstream_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/upstream"
)

func appendInfo(dst *[]RoundTripInfo) RoundTripFunc {
	// The proxy runs RoundTrip (and any retries) synchronously on the serving
	// goroutine, so no lock is needed.
	return func(_ *http.Request, info RoundTripInfo) { *dst = append(*dst, info) }
}

func TestUpstream_OnRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("success captures host, status, duration", func(t *testing.T) {
		var infos []RoundTripInfo
		u := Upstream{
			Transport: &mockTransport{roundTripFunc: func(r *http.Request) (*http.Response, error) {
				r.URL.Host = "backend-1" // the load balancer resolves the target here
				time.Sleep(time.Millisecond)
				w := httptest.NewRecorder()
				w.WriteHeader(http.StatusBadGateway) // a real origin 5xx, err==nil -> no retry
				return w.Result(), nil
			}},
			OnRoundTrip: appendInfo(&infos),
		}
		u.ServeHandler(nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

		require.Len(t, infos, 1)
		assert.Equal(t, "backend-1", infos[0].Host)
		assert.Equal(t, http.StatusBadGateway, infos[0].Status, "an origin 5xx is captured, not retried")
		assert.NoError(t, infos[0].Err)
		assert.Positive(t, infos[0].Duration, "TTFB is measured")
	})

	t.Run("transport error reports Err and zero Status", func(t *testing.T) {
		var infos []RoundTripInfo
		u := Upstream{
			Transport: &mockTransport{roundTripFunc: func(r *http.Request) (*http.Response, error) {
				r.URL.Host = "backend-2"
				return nil, fmt.Errorf("dial fail")
			}},
			OnRoundTrip: appendInfo(&infos),
			// Retries left at 0: exactly one attempt.
		}
		u.ServeHandler(nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

		require.Len(t, infos, 1)
		assert.Equal(t, "backend-2", infos[0].Host)
		assert.Zero(t, infos[0].Status)
		assert.Error(t, infos[0].Err)
	})

	t.Run("fires once per attempt including retries", func(t *testing.T) {
		var infos []RoundTripInfo
		u := Upstream{
			Transport: &mockTransport{roundTripFunc: func(r *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("dial fail")
			}},
			OnRoundTrip:   appendInfo(&infos),
			Retries:       2,
			BackoffFactor: time.Millisecond,
		}
		u.ServeHandler(nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

		assert.Len(t, infos, 3, "1 initial attempt + 2 retries, each observed")
		for _, info := range infos {
			assert.Error(t, info.Err)
		}
	})

	t.Run("nil OnRoundTrip is a no-op", func(t *testing.T) {
		assert.NotPanics(t, func() {
			Upstream{Transport: &mockTransport{roundTripFunc: func(r *http.Request) (*http.Response, error) {
				return httptest.NewRecorder().Result(), nil
			}}}.ServeHandler(nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		})
	})
}

func TestUpstream_OnRoundTripAttempt(t *testing.T) {
	t.Parallel()
	// The retry index increments across attempts: 0 first try, 1 first retry, ...
	var infos []RoundTripInfo
	u := Upstream{
		Transport: &mockTransport{roundTripFunc: func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial fail")
		}},
		Retries:       2,
		BackoffFactor: time.Millisecond,
		OnRoundTrip:   appendInfo(&infos),
	}
	u.ServeHandler(nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	require.Len(t, infos, 3, "1 initial + 2 retries")
	assert.Equal(t, 0, infos[0].Attempt)
	assert.Equal(t, 1, infos[1].Attempt)
	assert.Equal(t, 2, infos[2].Attempt)
}
