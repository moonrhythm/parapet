package authn_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/authn"
)

func TestAuthenticator(t *testing.T) {
	t.Parallel()

	t.Run("Empty Authenticator", func(t *testing.T) {
		m := Authenticator{}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	deny := func(*http.Request) error { return assert.AnError }

	t.Run("WWW-Authenticate set on failure (default, fresh slice)", func(t *testing.T) {
		m := Authenticator{Type: "Bearer", Authenticate: deny}
		handler := m.ServeHandler(http.NotFoundHandler())

		w1 := httptest.NewRecorder()
		handler.ServeHTTP(w1, httptest.NewRequest("GET", "/", nil))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, httptest.NewRequest("GET", "/", nil))

		v1 := w1.Header()["Www-Authenticate"]
		v2 := w2.Header()["Www-Authenticate"]
		if assert.Len(t, v1, 1) && assert.Len(t, v2, 1) {
			assert.Equal(t, "Bearer", v1[0])
			assert.NotSame(t, &v1[0], &v2[0])
		}
	})

	t.Run("ShareValueSlice shares WWW-Authenticate across failures", func(t *testing.T) {
		m := Authenticator{Type: "Bearer", Authenticate: deny, ShareValueSlice: true}
		handler := m.ServeHandler(http.NotFoundHandler())

		w1 := httptest.NewRecorder()
		handler.ServeHTTP(w1, httptest.NewRequest("GET", "/", nil))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, httptest.NewRequest("GET", "/", nil))

		v1 := w1.Header()["Www-Authenticate"]
		v2 := w2.Header()["Www-Authenticate"]
		if assert.Len(t, v1, 1) && assert.Len(t, v2, 1) {
			assert.Equal(t, "Bearer", v1[0])
			assert.Same(t, &v1[0], &v2[0])
		}
	})
}
