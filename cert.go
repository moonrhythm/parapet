package parapet

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"time"
)

// SelfSign options
type SelfSign struct {
	CommonName string
	Hosts      []string
	NotBefore  time.Time
	NotAfter   time.Time
}

// GenerateSelfSignCertificate generates new self sign certificate
func GenerateSelfSignCertificate(opt SelfSign) (cert tls.Certificate, err error) {
	pri, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return
	}

	sn, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	if opt.NotBefore.IsZero() {
		opt.NotBefore = time.Now()
	}
	if opt.NotAfter.IsZero() {
		opt.NotAfter = time.Now().AddDate(10, 0, 0)
	}

	x509Cert := x509.Certificate{
		SerialNumber: sn,
		Subject: pkix.Name{
			CommonName: opt.CommonName,
		},
		NotBefore:             opt.NotBefore,
		NotAfter:              opt.NotAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	for _, h := range opt.Hosts {
		if ip := net.ParseIP(h); ip != nil {
			x509Cert.IPAddresses = append(x509Cert.IPAddresses, ip)
		} else {
			x509Cert.DNSNames = append(x509Cert.DNSNames, h)
		}
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &x509Cert, &x509Cert, &pri.PublicKey, pri)
	if err != nil {
		return
	}

	return tls.Certificate{
		Certificate: [][]byte{certBytes},
		PrivateKey:  pri,
	}, nil
}
