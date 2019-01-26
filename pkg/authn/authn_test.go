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
}
