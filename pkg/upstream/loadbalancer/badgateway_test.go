package loadbalancer

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBadGateway(t *testing.T) {
	t.Parallel()

	l := badGateway{}

	r := httptest.NewRequest("GET", "/", nil)
	resp, err := l.RoundTrip(r)
	assert.Nil(t, resp)
	assert.Error(t, err)
}
