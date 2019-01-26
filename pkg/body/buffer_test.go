package body_test

import (
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/body"
)

func TestRequestBufferer(t *testing.T) {
	t.Parallel()

	t.Run("Buffered Unknown Size Body", func(t *testing.T) {
		t.Parallel()

		done := false

		pr, pw := io.Pipe()
		defer pr.Close()
		defer pw.Close()

		r := httptest.NewRequest("POST", "/", pr)
		w := httptest.NewRecorder()

		time.AfterFunc(10*time.Millisecond, func() {
			pw.Write([]byte("test"))
			done = true
			pw.Close()
		})

		BufferRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !done {
				assert.Fail(t, "can not done")
			}
			p, _ := ioutil.ReadAll(r.Body)
			assert.Equal(t, []byte("test"), p)
		})).ServeHTTP(w, r)
	})

	t.Run("Buffered Small Size Body", func(t *testing.T) {
		t.Parallel()

		done := false

		pr, pw := io.Pipe()
		defer pr.Close()
		defer pw.Close()

		r := httptest.NewRequest("POST", "/", pr)
		r.ContentLength = 4
		w := httptest.NewRecorder()

		time.AfterFunc(10*time.Millisecond, func() {
			done = true
			pw.Write([]byte("test"))
			pw.Close()
		})

		BufferRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !done {
				assert.Fail(t, "can not done")
			}
			p, _ := ioutil.ReadAll(r.Body)
			assert.Equal(t, []byte("test"), p)
		})).ServeHTTP(w, r)
	})

	t.Run("Empty Body", func(t *testing.T) {
		t.Parallel()

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		called := false
		BufferRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Buffered Cancelled Request", func(t *testing.T) {
		t.Parallel()

		pr, pw := io.Pipe()
		defer pr.Close()
		defer pw.Close()

		r := httptest.NewRequest("POST", "/", pr)
		ctx, cancel := context.WithCancel(r.Context())
		r = r.WithContext(ctx)
		r.ContentLength = 4
		w := httptest.NewRecorder()

		time.AfterFunc(10*time.Millisecond, func() {
			pw.Write([]byte("te"))
			cancel()
			pw.Write([]byte("te"))
			pw.Close()
		})

		BufferRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
	})

	t.Run("Buffered Unexpected Closed Body Request", func(t *testing.T) {
		t.Parallel()

		pr, pw := io.Pipe()
		defer pr.Close()
		defer pw.Close()

		r := httptest.NewRequest("POST", "/", pr)
		r.ContentLength = 4
		w := httptest.NewRecorder()

		time.AfterFunc(10*time.Millisecond, func() {
			pw.Write([]byte("te"))
			pw.Close()
		})

		BufferRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
	})

	t.Run("Buffered Unexpected Closed Unknown Size Body Request", func(t *testing.T) {
		t.Parallel()

		pr, pw := io.Pipe()
		defer pr.Close()
		defer pw.Close()

		r := httptest.NewRequest("POST", "/", pr)
		w := httptest.NewRecorder()

		time.AfterFunc(10*time.Millisecond, func() {
			pw.Write([]byte("te"))
			pw.CloseWithError(io.ErrUnexpectedEOF)
		})

		BufferRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
	})
}
