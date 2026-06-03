package evaluator

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"

	"geblang/internal/runtime"
)

func pemBytesFromValue(v runtime.Value, label string) ([]byte, error) {
	switch x := v.(type) {
	case runtime.String:
		return []byte(x.Value), nil
	case runtime.Bytes:
		return x.Value, nil
	default:
		return nil, fmt.Errorf("%s must be a PEM string or bytes", label)
	}
}

// buildHTTPClientTLSConfig parses a client `tls` options block. Returns nil
// when nothing about TLS was customised (the default secure transport stands).
func buildHTTPClientTLSConfig(tlsVal runtime.Value, label string) (*tls.Config, error) {
	opts, ok := tlsVal.(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s tls must be a dict", label)
	}
	cfg := &tls.Config{} //nolint:gosec // verify:false is an explicit opt-in below
	changed := false
	if verify, ok := dictBoolField(opts, "verify"); ok && !verify {
		cfg.InsecureSkipVerify = true
		changed = true
	}
	if caVal, ok := dictField(opts, "caCerts"); ok {
		caPEM, err := pemBytesFromValue(caVal, label+" tls.caCerts")
		if err != nil {
			return nil, err
		}
		var pool *x509.CertPool
		if only, _ := dictBoolField(opts, "caCertsOnly"); only {
			pool = x509.NewCertPool()
		} else if sys, err := x509.SystemCertPool(); err == nil && sys != nil {
			pool = sys
		} else {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("%s tls.caCerts: no valid certificate found", label)
		}
		cfg.RootCAs = pool
		changed = true
	}
	cert, err := tlsKeyPairFromOpts(opts, "clientCert", "clientKey", label)
	if err != nil {
		return nil, err
	}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return cfg, nil
}

// buildHTTPServerTLSConfig parses a server opts dict's `tls` block. Returns
// (nil, nil, nil) for plain HTTP. certPEM is the served certificate, exposed
// to callers via http.serverCert so a client can trust it precisely.
func buildHTTPServerTLSConfig(optsVal runtime.Value, addr, label string) (*tls.Config, []byte, error) {
	opts, ok := optsVal.(runtime.Dict)
	if !ok {
		return nil, nil, nil
	}
	tlsVal, ok := dictField(opts, "tls")
	if !ok {
		return nil, nil, nil
	}
	tlsOpts, ok := tlsVal.(runtime.Dict)
	if !ok {
		return nil, nil, fmt.Errorf("%s tls must be a dict", label)
	}
	_, hasCert := dictField(tlsOpts, "cert")
	_, hasKey := dictField(tlsOpts, "key")
	selfVal, hasSelf := dictField(tlsOpts, "selfSigned")
	if (hasCert || hasKey) && hasSelf {
		return nil, nil, fmt.Errorf("%s tls: use either cert/key or selfSigned, not both", label)
	}
	if hasCert || hasKey {
		cert, err := tlsKeyPairFromOpts(tlsOpts, "cert", "key", label)
		if err != nil {
			return nil, nil, err
		}
		certPEM, _ := pemBytesFromValue(mustField(tlsOpts, "cert"), label)
		return &tls.Config{Certificates: []tls.Certificate{*cert}}, certPEM, nil
	}
	if hasSelf {
		hosts, err := selfSignedHosts(selfVal, addr, label)
		if err != nil {
			return nil, nil, err
		}
		cert, certPEM, err := generateSelfSignedTLSCert(hosts)
		if err != nil {
			return nil, nil, fmt.Errorf("%s tls.selfSigned: %v", label, err)
		}
		return &tls.Config{Certificates: []tls.Certificate{cert}}, certPEM, nil
	}
	return nil, nil, fmt.Errorf("%s tls: provide cert/key or selfSigned: true", label)
}

func mustField(opts runtime.Dict, key string) runtime.Value {
	v, _ := dictField(opts, key)
	return v
}

// tlsKeyPairFromOpts loads a cert/key PEM pair under the given option names.
// Both must be present together; returns nil when neither is set.
func tlsKeyPairFromOpts(opts runtime.Dict, certKey, keyKey, label string) (*tls.Certificate, error) {
	certVal, hasCert := dictField(opts, certKey)
	keyVal, hasKey := dictField(opts, keyKey)
	if hasCert != hasKey {
		return nil, fmt.Errorf("%s tls: %s and %s must be provided together", label, certKey, keyKey)
	}
	if !hasCert {
		return nil, nil
	}
	certPEM, err := pemBytesFromValue(certVal, label+" tls."+certKey)
	if err != nil {
		return nil, err
	}
	keyPEM, err := pemBytesFromValue(keyVal, label+" tls."+keyKey)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s tls.%s/%s: %v", label, certKey, keyKey, err)
	}
	return &pair, nil
}

func selfSignedHosts(selfVal runtime.Value, addr, label string) ([]string, error) {
	base := []string{"localhost", "127.0.0.1", "::1"}
	switch v := selfVal.(type) {
	case runtime.Bool:
		if !v.Value {
			return nil, fmt.Errorf("%s tls.selfSigned must be true or a list of hosts", label)
		}
		if host, _, err := net.SplitHostPort(addr); err == nil && host != "" && host != "0.0.0.0" && host != "::" {
			base = append(base, host)
		}
		return base, nil
	case *runtime.List:
		hosts := make([]string, 0, len(v.Elements))
		for i, elem := range v.Elements {
			s, ok := elem.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("%s tls.selfSigned[%d] must be a string host", label, i)
			}
			hosts = append(hosts, s.Value)
		}
		if len(hosts) == 0 {
			return base, nil
		}
		return hosts, nil
	default:
		return nil, fmt.Errorf("%s tls.selfSigned must be true or a list of hosts", label)
	}
}

func generateSelfSignedTLSCert(hosts []string) (tls.Certificate, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	cn := "localhost"
	if len(hosts) > 0 {
		cn = hosts[0]
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Geblang self-signed"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return pair, certPEM, nil
}
