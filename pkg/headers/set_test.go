package headers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/headers"
)

func TestResponseSetter(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	called := false
	SetResponse("X-Header", "1").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("X-Header", "0")
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, r)
	assert.True(t, called)
	assert.Equal(t, "1", w.Header().Get("X-Header"))
}
