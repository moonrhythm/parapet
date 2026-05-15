package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/logger"
)

func decodeLog(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("not valid json: %v\n%s", err, string(b))
	}
	return d
}

func TestStdoutStderrConstructors(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, Stdout())
	assert.True(t, Stdout().OmitEmpty)
	assert.NotNil(t, Stderr())
	assert.True(t, Stderr().OmitEmpty)
}

func TestLoggerWritesRecord(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	m := &Logger{Writer: buf}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	}))

	r := httptest.NewRequest("GET", "/path?x=1", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Referer", "https://ref.example")
	r.Header.Set("User-Agent", "ua")
	r.RemoteAddr = "192.0.2.10:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	d := decodeLog(t, buf.Bytes())
	assert.Equal(t, "GET", d["requestMethod"])
	assert.EqualValues(t, http.StatusCreated, d["status"])
	assert.EqualValues(t, len("hello"), d["responseBodySize"])
	assert.Equal(t, "192.0.2.10", d["remoteIp"])
	assert.Equal(t, "https://ref.example", d["referer"])
	assert.Equal(t, "ua", d["userAgent"])
	assert.Equal(t, "https://example.com/path?x=1", d["requestUrl"])
	assert.Contains(t, d, "duration")
	assert.Contains(t, d, "durationHuman")
}

func TestLoggerWritesStatus200WhenHandlerOnlyWrites(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	m := &Logger{Writer: buf}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hi"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	d := decodeLog(t, buf.Bytes())
	assert.EqualValues(t, http.StatusOK, d["status"])
}

func TestLoggerOmitEmpty(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	m := &Logger{Writer: buf, OmitEmpty: true}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	d := decodeLog(t, buf.Bytes())
	_, hasReferer := d["referer"]
	_, hasUA := d["userAgent"]
	_, hasFF := d["forwardedFor"]
	_, hasRealIP := d["realIp"]
	assert.False(t, hasReferer)
	assert.False(t, hasUA)
	assert.False(t, hasFF)
	assert.False(t, hasRealIP)
}

func TestDisableSkipsRecord(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	m := &Logger{Writer: buf}
	inner := Disable().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h := m.ServeHandler(inner)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Empty(t, buf.Bytes(), "disabled logger should not write")
}

func TestSetGet(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	m := &Logger{Writer: buf}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Set(r.Context(), "custom", "value")
		assert.Equal(t, "value", Get(r.Context(), "custom"))
		w.WriteHeader(http.StatusOK)
	})
	h := m.ServeHandler(inner)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	d := decodeLog(t, buf.Bytes())
	assert.Equal(t, "value", d["custom"])
}

func TestSetGetWithoutRecord(t *testing.T) {
	t.Parallel()

	Set(context.Background(), "x", 1)
	assert.Nil(t, Get(context.Background(), "x"))
}

func TestLoggerDefaultWriter(t *testing.T) {
	t.Parallel()

	m := &Logger{}
	// just ensure that calling ServeHandler with a nil Writer does not panic
	assert.NotPanics(t, func() {
		_ = m.ServeHandler(http.NotFoundHandler())
	})
}

func TestLoggerCanceledContextStatus499(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	m := &Logger{Writer: buf}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// don't write anything; statusCode stays 0
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	d := decodeLog(t, buf.Bytes())
	assert.EqualValues(t, 499, d["status"])
}
