package stripprefix_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/stripprefix"
)

func TestStripPrefix(t *testing.T) {
	t.Parallel()

	m := New("/prefix")
	r := httptest.NewRequest("GET", "/prefix/path", nil)
	w := httptest.NewRecorder()
	called := false
	m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.Equal(t, "/path", r.URL.Path)
	})).ServeHTTP(w, r)
	assert.True(t, called)
}
