package loadbalancer_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet/pkg/upstream"
	. "github.com/moonrhythm/parapet/pkg/upstream/loadbalancer"
)

func TestRoundRobin(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		l := NewRoundRobin(nil)
		r := httptest.NewRequest("GET", "/", nil)
		resp, err := l.RoundTrip(r)
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.Equal(t, upstream.ErrUnavailable, err)
	})

	t.Run("One target", func(t *testing.T) {
		tr := &fakeTransport{}
		l := NewRoundRobin([]*Target{{Host: "upstream1", Transport: tr}})

		r := httptest.NewRequest("GET", "/", nil)
		resp, err := l.RoundTrip(r)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.True(t, tr.called)
		assert.Equal(t, "upstream1", tr.host)

		*tr = fakeTransport{}
		r = httptest.NewRequest("GET", "/", nil)
		resp, err = l.RoundTrip(r)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.True(t, tr.called)
		assert.Equal(t, "upstream1", tr.host)
	})

	t.Run("Two targets", func(t *testing.T) {
		tr0 := &fakeTransport{}
		tr1 := &fakeTransport{}
		l := NewRoundRobin([]*Target{
			{Host: "upstream0", Transport: tr0},
			{Host: "upstream1", Transport: tr1},
		})

		r := httptest.NewRequest("GET", "/", nil)
		resp, err := l.RoundTrip(r)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.True(t, tr0.called)
		assert.Equal(t, "upstream0", tr0.host)
		assert.False(t, tr1.called)

		*tr0 = fakeTransport{}
		r = httptest.NewRequest("GET", "/", nil)
		resp, err = l.RoundTrip(r)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.False(t, tr0.called)
		assert.True(t, tr1.called)
		assert.Equal(t, "upstream1", tr1.host)

		*tr0 = fakeTransport{}
		*tr1 = fakeTransport{}
		r = httptest.NewRequest("GET", "/", nil)
		resp, err = l.RoundTrip(r)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.True(t, tr0.called)
		assert.False(t, tr1.called)
	})
}

type fakeTransport struct {
	called bool
	host   string
}

func (tr *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.called = true
	tr.host = r.URL.Host
	return httptest.NewRecorder().Result(), nil
}
