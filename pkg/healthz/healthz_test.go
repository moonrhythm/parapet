package healthz_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/healthz"
)

func TestHealthz(t *testing.T) {
	t.Parallel()

	m := New()

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	m.ServeHandler(http.NotFoundHandler()).ServeHTTP(w, r)
	assert.EqualValues(t, 200, w.Code)
}
