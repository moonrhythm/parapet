package h2push_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/h2push"
)

type pushRecorder struct {
	http.ResponseWriter
	pushes []string
}

func (p *pushRecorder) Push(target string, _ *http.PushOptions) error {
	p.pushes = append(p.pushes, target)
	return nil
}

func TestLinkPusherEmptyDoesNothing(t *testing.T) {
	t.Parallel()

	m := Push("")
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, ok := w.(http.Pusher)
		assert.False(t, ok, "should not wrap response writer when Link empty")
	}))
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.True(t, called)
}

func TestLinkPusherPushes(t *testing.T) {
	t.Parallel()

	m := Push("/static/app.js")
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := &pushRecorder{ResponseWriter: httptest.NewRecorder()}
	r := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, r)

	assert.Equal(t, []string{"/static/app.js"}, rec.pushes)
}

func TestLinkPusherNoPusherNoop(t *testing.T) {
	t.Parallel()

	m := Push("/static/app.js")
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	// httptest.ResponseRecorder is not a Pusher
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
}

func TestPreloadPusherSkipsWithoutPusher(t *testing.T) {
	t.Parallel()

	m := Preload()
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
}

func TestPreloadPusherSkipsNopushAndNonPreload(t *testing.T) {
	t.Parallel()

	m := Preload()
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Link", "</b.js>; rel=preload; nopush")
		w.Header().Add("Link", "</c.png>; rel=icon")
		w.WriteHeader(http.StatusOK)
	}))

	rec := &pushRecorder{ResponseWriter: httptest.NewRecorder()}
	r := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, r)

	assert.Empty(t, rec.pushes, "nopush and non-preload links must not be pushed")
}

func TestPreloadPusherImplicitWriteHeader(t *testing.T) {
	t.Parallel()

	m := Preload()
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	rec := &pushRecorder{ResponseWriter: httptest.NewRecorder()}
	r := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, r)

	// implicit Write must have produced an OK response on the underlying writer
	assert.Equal(t, http.StatusOK, rec.ResponseWriter.(*httptest.ResponseRecorder).Code)
}
