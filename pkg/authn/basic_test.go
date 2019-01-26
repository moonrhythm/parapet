package authn_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/authn"
)

func TestBasic(t *testing.T) {
	t.Parallel()

	t.Run("Unauthorized", func(t *testing.T) {
		m := Basic("root", "pass")

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Wrong Credentials", func(t *testing.T) {
		m := Basic("root", "pass")

		r := httptest.NewRequest("GET", "/", nil)
		r.SetBasicAuth("user", "pass")
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Authorized", func(t *testing.T) {
		m := Basic("root", "pass")

		r := httptest.NewRequest("GET", "/", nil)
		r.SetBasicAuth("root", "pass")
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Header", func(t *testing.T) {
		m := Basic("root", "pass")
		m.Realm = "test"

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, `Basic realm="test"`, w.Header().Get("WWW-Authenticate"))
	})
}
