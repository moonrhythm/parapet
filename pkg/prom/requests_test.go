package prom_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/prom"
)

func TestRequests(t *testing.T) {
	t.Parallel()

	t.Run("Base", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		called := false
		Requests().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(400)
		})).ServeHTTP(w, r)

		assert.True(t, called)
		assert.Equal(t, 400, w.Code)
	})
}
