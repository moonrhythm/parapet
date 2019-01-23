package requestid_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/requestid"
)

func TestRequestID(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() { New() })

	t.Run("Default Header", func(t *testing.T) {
		m := RequestID{}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NotEmpty(t, r.Header.Get(DefaultHeader))
			assert.Equal(t, r.Header.Get(DefaultHeader), w.Header().Get(DefaultHeader))
		})).ServeHTTP(w, r)
		assert.NotEmpty(t, w.Header().Get(DefaultHeader))
	})

	t.Run("Custom Header", func(t *testing.T) {
		header := "X-Custom-Id"

		m := RequestID{
			Header: header,
		}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NotEmpty(t, r.Header.Get(header))
			assert.Equal(t, r.Header.Get(header), w.Header().Get(header))
		})).ServeHTTP(w, r)
		assert.NotEmpty(t, w.Header().Get(header))
	})

	t.Run("Trust Proxy", func(t *testing.T) {
		value := "123aaa"

		m := RequestID{
			TrustProxy: true,
		}

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(DefaultHeader, value)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, r.Header.Get(DefaultHeader), value)
		})).ServeHTTP(w, r)
		assert.Equal(t, value, w.Header().Get(DefaultHeader))
	})

	t.Run("Not Trust Proxy", func(t *testing.T) {
		value := "123aaa"

		m := RequestID{
			TrustProxy: false,
		}

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(DefaultHeader, value)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NotEqual(t, r.Header.Get(DefaultHeader), value)
		})).ServeHTTP(w, r)
		assert.NotEqual(t, value, w.Header().Get(DefaultHeader))
	})
}
