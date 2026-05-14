package parapet_test

import (
	"crypto/x509"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet"
)

func TestGenerateSelfSignCertificateDefaults(t *testing.T) {
	t.Parallel()

	cert, err := GenerateSelfSignCertificate(SelfSign{
		CommonName: "test",
		Hosts:      []string{"localhost", "127.0.0.1"},
	})
	assert.NoError(t, err)
	if !assert.NotEmpty(t, cert.Certificate) {
		return
	}

	x, err := x509.ParseCertificate(cert.Certificate[0])
	assert.NoError(t, err)
	assert.Equal(t, "test", x.Subject.CommonName)
	assert.Contains(t, x.DNSNames, "localhost")
	if assert.NotEmpty(t, x.IPAddresses) {
		assert.Equal(t, "127.0.0.1", x.IPAddresses[0].String())
	}
	// NotAfter should default to ~10 years from now
	assert.True(t, x.NotAfter.After(time.Now().AddDate(9, 0, 0)))
	assert.True(t, x.NotAfter.Before(time.Now().AddDate(11, 0, 0)))
}

func TestGenerateSelfSignCertificateExplicitDates(t *testing.T) {
	t.Parallel()

	notBefore := time.Now().Add(-time.Hour).Truncate(time.Second)
	notAfter := notBefore.Add(24 * time.Hour)
	cert, err := GenerateSelfSignCertificate(SelfSign{
		CommonName: "explicit",
		Hosts:      []string{"example.com"},
		NotBefore:  notBefore,
		NotAfter:   notAfter,
	})
	assert.NoError(t, err)

	x, err := x509.ParseCertificate(cert.Certificate[0])
	assert.NoError(t, err)
	assert.WithinDuration(t, notBefore, x.NotBefore, time.Second)
	assert.WithinDuration(t, notAfter, x.NotAfter, time.Second)
	assert.Equal(t, []string{"example.com"}, x.DNSNames)
}
