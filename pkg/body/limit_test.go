package body_test

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/body"
)

func TestRequestLimiter(t *testing.T) {
	t.Parallel()

	t.Run("Known Body Size", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			Name        string
			Size        int64
			RequestSize int64
			Limited     bool
		}{
			{"Unlimited", -1, 10, false},
			{"Zero Body", 0, 0, false},
			{"Zero Body Limited", 0, 1, true},
			{"Allow Limited", 10, 5, false},
			{"Limited", 10, 20, true},
		}

		for _, c := range cases {
			t.Run(c.Name, func(t *testing.T) {
				m := LimitRequest(c.Size)

				b := make([]byte, c.RequestSize)
				r := httptest.NewRequest("POST", "/", bytes.NewReader(b))
				w := httptest.NewRecorder()

				called := false
				m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					w.WriteHeader(http.StatusOK)
				})).ServeHTTP(w, r)
				if c.Limited {
					assert.False(t, called)
					assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
				} else {
					assert.True(t, called)
					assert.Equal(t, http.StatusOK, w.Code)
				}
			})
		}
	})

	t.Run("Limited Unknown Body Size", func(t *testing.T) {
		t.Parallel()

		m := LimitRequest(5)

		r := httptest.NewRequest("POST", "/", readerFunc(func(p []byte) (int, error) {
			return 10, nil
		}))
		w := httptest.NewRecorder()

		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ioutil.ReadAll(r.Body)
		})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})
}

type readerFunc func(p []byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) {
	return f(p)
}
