package timeout_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/timeout"
)

// handlerPtr returns the underlying code pointer of an http.Handler so two
// handlers can be compared for identity (http.HandlerFunc values are not
// comparable with ==).
func handlerPtr(h http.Handler) uintptr {
	return reflect.ValueOf(h).Pointer()
}

func TestRequestDeadlineCancelsContext(t *testing.T) {
	t.Parallel()

	m := NewRequestDeadline(20 * time.Millisecond)

	var ctxErr error
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the deadline cancels the request context. This
		// synchronizes deterministically (no sleep-and-hope): the handler
		// only returns once the deadline fires.
		<-r.Context().Done()
		ctxErr = r.Context().Err()
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.ErrorIs(t, ctxErr, context.DeadlineExceeded)
}

func TestRequestDeadlineFastHandlerCompletes(t *testing.T) {
	t.Parallel()

	// A generous deadline with a fast handler must complete normally and the
	// context must NOT be errored when the handler runs.
	m := NewRequestDeadline(time.Minute)

	called := false
	var ctxErr error
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		ctxErr = r.Context().Err()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
	assert.NoError(t, ctxErr)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}

func TestRequestDeadlineZeroIsPassThrough(t *testing.T) {
	t.Parallel()

	m := NewRequestDeadline(0)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := m.ServeHandler(inner)

	// <= 0 duration is a pass-through: the exact same handler instance is
	// returned, with no context deadline armed.
	assert.Equal(t,
		handlerPtr(inner),
		handlerPtr(h),
		"zero-duration RequestDeadline must return the handler unchanged",
	)

	// And it must not arm a deadline.
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	hadDeadline := true
	wrapped := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadDeadline = r.Context().Deadline()
	}))
	wrapped.ServeHTTP(w, r)
	assert.False(t, hadDeadline, "pass-through must not set a context deadline")
}

func TestRequestDeadlineNegativeIsPassThrough(t *testing.T) {
	t.Parallel()

	m := NewRequestDeadline(-time.Second)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := m.ServeHandler(inner)

	assert.Equal(t, handlerPtr(inner), handlerPtr(h))
}

func TestTimeoutAliasUsable(t *testing.T) {
	t.Parallel()

	// The Timeout alias is interchangeable with Timout: a Timout value is
	// accepted where a Timeout is required (they are the same underlying type),
	// and a Timeout value is usable as a middleware.
	takesTimeout := func(Timeout) {}
	takesTimeout(Timout{Timeout: time.Second})

	m := Timeout{Timeout: time.Second}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusTeapot, w.Code)
}
