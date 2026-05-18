package native

import (
	gocrypto "crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"geblang/internal/runtime"
)

func registerCryptPKI(r *Registry) {
	r.Register("crypt", "generateRsaKey", func(args []runtime.Value) (runtime.Value, error) {
		bits := int64(2048)
		if len(args) == 1 {
			n, err := singleInt64(args, "crypt.generateRsaKey")
			if err != nil {
				return nil, err
			}
			bits = n
		} else if len(args) > 1 {
			return nil, fmt.Errorf("crypt.generateRsaKey expects 0 or 1 arguments")
		}
		if bits < 1024 || bits > 8192 {
			return nil, fmt.Errorf("crypt.generateRsaKey bits must be between 1024 and 8192")
		}
		key, err := rsa.GenerateKey(rand.Reader, int(bits))
		if err != nil {
			return nil, fmt.Errorf("crypt.generateRsaKey: %w", err)
		}
		return marshalPrivKeyPEM(key)
	})

	r.Register("crypt", "generateEcKey", func(args []runtime.Value) (runtime.Value, error) {
		curveName := "P-256"
		if len(args) == 1 {
			s, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("crypt.generateEcKey curve must be string")
			}
			curveName = s.Value
		} else if len(args) > 1 {
			return nil, fmt.Errorf("crypt.generateEcKey expects 0 or 1 arguments")
		}
		curve, err := namedCurve(curveName, "crypt.generateEcKey")
		if err != nil {
			return nil, err
		}
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("crypt.generateEcKey: %w", err)
		}
		return marshalPrivKeyPEM(key)
	})

	r.Register("crypt", "generateEd25519Key", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("crypt.generateEd25519Key expects no arguments")
		}
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("crypt.generateEd25519Key: %w", err)
		}
		return marshalPrivKeyPEM(priv)
	})

	r.Register("crypt", "publicKey", func(args []runtime.Value) (runtime.Value, error) {
		pemStr, err := singleString(args, "crypt.publicKey")
		if err != nil {
			return nil, err
		}
		priv, err := parsePrivKeyPEM(pemStr, "crypt.publicKey")
		if err != nil {
			return nil, err
		}
		var pub interface{}
		switch k := priv.(type) {
		case *rsa.PrivateKey:
			pub = k.Public()
		case *ecdsa.PrivateKey:
			pub = k.Public()
		case ed25519.PrivateKey:
			pub = k.Public()
		default:
			return nil, fmt.Errorf("crypt.publicKey unsupported key type")
		}
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			return nil, fmt.Errorf("crypt.publicKey marshal: %w", err)
		}
		return runtime.String{Value: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))}, nil
	})

	r.Register("crypt", "generateSelfSignedCert", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("crypt.generateSelfSignedCert expects an options dict")
		}
		opts, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("crypt.generateSelfSignedCert options must be dict")
		}

		subject := parsePkixName(opts, "subject")
		dnsNames := parseDNSNames(opts)
		ipAddresses := parseIPAddresses(opts)
		validDays := dictInt(opts, "validDays", 365)

		var privKey interface{}
		var privKeyPEM string

		if existingPEM := dictStr(opts, "key"); existingPEM != "" {
			k, err := parsePrivKeyPEM(existingPEM, "crypt.generateSelfSignedCert")
			if err != nil {
				return nil, err
			}
			privKey = k
			privKeyPEM = existingPEM
		} else {
			keyType := dictStr(opts, "keyType")
			if keyType == "" {
				keyType = "EC-P256"
			}
			k, pemStr, err := generateKeyByType(keyType, "crypt.generateSelfSignedCert")
			if err != nil {
				return nil, err
			}
			privKey = k
			privKeyPEM = pemStr
		}

		pubKey, err := extractPublicKey(privKey, "crypt.generateSelfSignedCert")
		if err != nil {
			return nil, err
		}

		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return nil, fmt.Errorf("crypt.generateSelfSignedCert serial: %w", err)
		}
		now := time.Now()
		tmpl := &x509.Certificate{
			SerialNumber:          serial,
			Subject:               subject,
			NotBefore:             now,
			NotAfter:              now.Add(time.Duration(validDays) * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			BasicConstraintsValid: true,
			IsCA:                  true,
			DNSNames:              dnsNames,
			IPAddresses:           ipAddresses,
		}
		certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pubKey, privKey)
		if err != nil {
			return nil, fmt.Errorf("crypt.generateSelfSignedCert: %w", err)
		}
		certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))

		certKey := runtime.String{Value: "cert"}
		keyKey := runtime.String{Value: "key"}
		entries := map[string]runtime.DictEntry{
			DictKey(certKey): {Key: certKey, Value: runtime.String{Value: certPEM}},
			DictKey(keyKey):  {Key: keyKey, Value: runtime.String{Value: privKeyPEM}},
		}
		return runtime.Dict{Entries: entries}, nil
	})

	r.Register("crypt", "generateCsr", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("crypt.generateCsr expects an options dict")
		}
		opts, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("crypt.generateCsr options must be dict")
		}

		keyPEM := dictStr(opts, "key")
		if keyPEM == "" {
			return nil, fmt.Errorf("crypt.generateCsr options must include 'key'")
		}
		privKey, err := parsePrivKeyPEM(keyPEM, "crypt.generateCsr")
		if err != nil {
			return nil, err
		}

		subject := parsePkixName(opts, "subject")
		dnsNames := parseDNSNames(opts)
		ipAddresses := parseIPAddresses(opts)

		tmpl := &x509.CertificateRequest{
			Subject:     subject,
			DNSNames:    dnsNames,
			IPAddresses: ipAddresses,
		}
		csrDER, err := x509.CreateCertificateRequest(rand.Reader, tmpl, privKey)
		if err != nil {
			return nil, fmt.Errorf("crypt.generateCsr: %w", err)
		}
		return runtime.String{Value: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))}, nil
	})

	r.Register("crypt", "parseCert", func(args []runtime.Value) (runtime.Value, error) {
		pemStr, err := singleString(args, "crypt.parseCert")
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode([]byte(pemStr))
		if block == nil {
			return nil, fmt.Errorf("crypt.parseCert invalid PEM")
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("crypt.parseCert expected CERTIFICATE PEM, got %q", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("crypt.parseCert: %w", err)
		}

		dnsElems := make([]runtime.Value, len(cert.DNSNames))
		for i, n := range cert.DNSNames {
			dnsElems[i] = runtime.String{Value: n}
		}
		ipElems := make([]runtime.Value, len(cert.IPAddresses))
		for i, ip := range cert.IPAddresses {
			ipElems[i] = runtime.String{Value: ip.String()}
		}

		keyType := "unknown"
		switch cert.PublicKey.(type) {
		case *rsa.PublicKey:
			keyType = "RSA"
		case *ecdsa.PublicKey:
			keyType = "EC"
		case ed25519.PublicKey:
			keyType = "Ed25519"
		}

		subjectDict := pkixNameToDict(cert.Subject)
		issuerDict := pkixNameToDict(cert.Issuer)

		entries := map[string]runtime.DictEntry{}
		setEntry := func(key string, val runtime.Value) {
			k := runtime.String{Value: key}
			entries[DictKey(k)] = runtime.DictEntry{Key: k, Value: val}
		}
		setEntry("subject", subjectDict)
		setEntry("issuer", issuerDict)
		setEntry("dnsNames", runtime.List{Elements: dnsElems})
		setEntry("ipAddresses", runtime.List{Elements: ipElems})
		setEntry("notBefore", runtime.String{Value: cert.NotBefore.UTC().Format(time.RFC3339)})
		setEntry("notAfter", runtime.String{Value: cert.NotAfter.UTC().Format(time.RFC3339)})
		setEntry("serialNumber", runtime.String{Value: cert.SerialNumber.Text(16)})
		setEntry("keyType", runtime.String{Value: keyType})
		setEntry("isCA", runtime.Bool{Value: cert.IsCA})

		return runtime.Dict{Entries: entries}, nil
	})

	// Asymmetric JWT

	r.Register("crypt", "jwtSignRS256", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("crypt.jwtSignRS256 expects payload and privatePem")
		}
		pemStr, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtSignRS256 privatePem must be string")
		}
		priv, err := parsePrivKeyPEM(pemStr.Value, "crypt.jwtSignRS256")
		if err != nil {
			return nil, err
		}
		rsaKey, ok := priv.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtSignRS256 key must be RSA")
		}
		sigInput, err := jwtBuildSigInput(args[0], "RS256")
		if err != nil {
			return nil, fmt.Errorf("crypt.jwtSignRS256: %w", err)
		}
		digest := sha256Sum(sigInput)
		sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, gocrypto.SHA256, digest)
		if err != nil {
			return nil, fmt.Errorf("crypt.jwtSignRS256 sign: %w", err)
		}
		return runtime.String{Value: sigInput + "." + base64.RawURLEncoding.EncodeToString(sig)}, nil
	})

	r.Register("crypt", "jwtVerifyRS256", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("crypt.jwtVerifyRS256 expects token and publicPem")
		}
		token, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtVerifyRS256 token must be string")
		}
		pemStr, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtVerifyRS256 publicPem must be string")
		}
		parts := strings.SplitN(token.Value, ".", 3)
		if len(parts) != 3 {
			return runtime.Null{}, nil
		}
		sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return runtime.Null{}, nil
		}
		digest := sha256Sum(parts[0] + "." + parts[1])
		pub, err := parsePublicKeyPEM(pemStr.Value, "crypt.jwtVerifyRS256")
		if err != nil {
			return nil, err
		}
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return runtime.Null{}, nil
		}
		if err := rsa.VerifyPKCS1v15(rsaPub, gocrypto.SHA256, digest, sigBytes); err != nil {
			return runtime.Null{}, nil
		}
		return jwtDecodePayload(parts[1])
	})

	r.Register("crypt", "jwtSignES256", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("crypt.jwtSignES256 expects payload and privatePem")
		}
		pemStr, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtSignES256 privatePem must be string")
		}
		priv, err := parsePrivKeyPEM(pemStr.Value, "crypt.jwtSignES256")
		if err != nil {
			return nil, err
		}
		ecKey, ok := priv.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtSignES256 key must be EC")
		}
		sigInput, err := jwtBuildSigInput(args[0], "ES256")
		if err != nil {
			return nil, fmt.Errorf("crypt.jwtSignES256: %w", err)
		}
		digest := sha256Sum(sigInput)
		rVal, sVal, err := ecdsa.Sign(rand.Reader, ecKey, digest)
		if err != nil {
			return nil, fmt.Errorf("crypt.jwtSignES256 sign: %w", err)
		}
		keySize := (ecKey.Curve.Params().N.BitLen() + 7) / 8
		sig := make([]byte, 2*keySize)
		rVal.FillBytes(sig[:keySize])
		sVal.FillBytes(sig[keySize:])
		return runtime.String{Value: sigInput + "." + base64.RawURLEncoding.EncodeToString(sig)}, nil
	})

	r.Register("crypt", "jwtVerifyES256", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("crypt.jwtVerifyES256 expects token and publicPem")
		}
		token, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtVerifyES256 token must be string")
		}
		pemStr, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtVerifyES256 publicPem must be string")
		}
		parts := strings.SplitN(token.Value, ".", 3)
		if len(parts) != 3 {
			return runtime.Null{}, nil
		}
		sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil || len(sigBytes) == 0 || len(sigBytes)%2 != 0 {
			return runtime.Null{}, nil
		}
		digest := sha256Sum(parts[0] + "." + parts[1])
		pub, err := parsePublicKeyPEM(pemStr.Value, "crypt.jwtVerifyES256")
		if err != nil {
			return nil, err
		}
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return runtime.Null{}, nil
		}
		half := len(sigBytes) / 2
		rVal := new(big.Int).SetBytes(sigBytes[:half])
		sVal := new(big.Int).SetBytes(sigBytes[half:])
		if !ecdsa.Verify(ecPub, digest, rVal, sVal) {
			return runtime.Null{}, nil
		}
		return jwtDecodePayload(parts[1])
	})
}

// — helpers —

func marshalPrivKeyPEM(key interface{}) (runtime.Value, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	return runtime.String{Value: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}, nil
}

func parsePrivKeyPEM(pemStr, label string) (interface{}, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("%s invalid PEM", label)
	}
	switch block.Type {
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s invalid PKCS#8 key: %w", label, err)
		}
		return key, nil
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s invalid RSA key: %w", label, err)
		}
		return key, nil
	case "EC PRIVATE KEY":
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s invalid EC key: %w", label, err)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("%s expected private key PEM, got %q", label, block.Type)
	}
}

func parsePublicKeyPEM(pemStr, label string) (interface{}, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("%s invalid PEM", label)
	}
	switch block.Type {
	case "PUBLIC KEY":
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s invalid public key: %w", label, err)
		}
		return pub, nil
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s invalid certificate: %w", label, err)
		}
		return cert.PublicKey, nil
	default:
		return nil, fmt.Errorf("%s expected PUBLIC KEY or CERTIFICATE PEM, got %q", label, block.Type)
	}
}

func extractPublicKey(priv interface{}, label string) (interface{}, error) {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return k.Public(), nil
	case *ecdsa.PrivateKey:
		return k.Public(), nil
	case ed25519.PrivateKey:
		return k.Public(), nil
	default:
		return nil, fmt.Errorf("%s unsupported key type", label)
	}
}

func namedCurve(name, label string) (elliptic.Curve, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("%s unsupported curve %q; use P-256, P-384, or P-521", label, name)
	}
}

func generateKeyByType(keyType, label string) (interface{}, string, error) {
	var key interface{}
	var err error
	switch keyType {
	case "RSA2048":
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	case "RSA4096":
		key, err = rsa.GenerateKey(rand.Reader, 4096)
	case "EC-P256":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "EC-P384":
		key, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "EC-P521":
		key, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	case "Ed25519":
		_, key, err = ed25519.GenerateKey(rand.Reader)
	default:
		return nil, "", fmt.Errorf("%s unsupported keyType %q; use RSA2048, RSA4096, EC-P256, EC-P384, EC-P521, or Ed25519", label, keyType)
	}
	if err != nil {
		return nil, "", fmt.Errorf("%s key gen: %w", label, err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, "", fmt.Errorf("%s marshal: %w", label, err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	return key, pemStr, nil
}

func dictStr(d runtime.Dict, key string) string {
	e, ok := d.Entries[DictKey(runtime.String{Value: key})]
	if !ok {
		return ""
	}
	s, ok := e.Value.(runtime.String)
	if !ok {
		return ""
	}
	return s.Value
}

func dictInt(d runtime.Dict, key string, defaultVal int) int {
	e, ok := d.Entries[DictKey(runtime.String{Value: key})]
	if !ok {
		return defaultVal
	}
	if v, ok := e.Value.(runtime.Int); ok && v.Value != nil {
		return int(v.Value.Int64())
	}
	return defaultVal
}

func parsePkixName(opts runtime.Dict, key string) pkix.Name {
	entry, ok := opts.Entries[DictKey(runtime.String{Value: key})]
	if !ok {
		return pkix.Name{}
	}
	sub, ok := entry.Value.(runtime.Dict)
	if !ok {
		return pkix.Name{}
	}
	name := pkix.Name{}
	if cn := dictStr(sub, "commonName"); cn != "" {
		name.CommonName = cn
	}
	if org := dictStr(sub, "organization"); org != "" {
		name.Organization = []string{org}
	}
	if country := dictStr(sub, "country"); country != "" {
		name.Country = []string{country}
	}
	if state := dictStr(sub, "state"); state != "" {
		name.Province = []string{state}
	}
	if locality := dictStr(sub, "locality"); locality != "" {
		name.Locality = []string{locality}
	}
	return name
}

func parseDNSNames(opts runtime.Dict) []string {
	entry, ok := opts.Entries[DictKey(runtime.String{Value: "dnsNames"})]
	if !ok {
		return nil
	}
	list, ok := entry.Value.(runtime.List)
	if !ok {
		return nil
	}
	var names []string
	for _, v := range list.Elements {
		if s, ok := v.(runtime.String); ok {
			names = append(names, s.Value)
		}
	}
	return names
}

func parseIPAddresses(opts runtime.Dict) []net.IP {
	entry, ok := opts.Entries[DictKey(runtime.String{Value: "ipAddresses"})]
	if !ok {
		return nil
	}
	list, ok := entry.Value.(runtime.List)
	if !ok {
		return nil
	}
	var ips []net.IP
	for _, v := range list.Elements {
		if s, ok := v.(runtime.String); ok {
			if ip := net.ParseIP(s.Value); ip != nil {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

func pkixNameToDict(name pkix.Name) runtime.Value {
	entries := map[string]runtime.DictEntry{}
	setStr := func(key, val string) {
		if val == "" {
			return
		}
		k := runtime.String{Value: key}
		entries[DictKey(k)] = runtime.DictEntry{Key: k, Value: runtime.String{Value: val}}
	}
	setStr("commonName", name.CommonName)
	if len(name.Organization) > 0 {
		setStr("organization", name.Organization[0])
	}
	if len(name.Country) > 0 {
		setStr("country", name.Country[0])
	}
	if len(name.Province) > 0 {
		setStr("state", name.Province[0])
	}
	if len(name.Locality) > 0 {
		setStr("locality", name.Locality[0])
	}
	return runtime.Dict{Entries: entries}
}

func sha256Sum(input string) []byte {
	h := sha256.New()
	h.Write([]byte(input))
	return h.Sum(nil)
}

func jwtBuildSigInput(payloadVal runtime.Value, alg string) (string, error) {
	payloadJSON, err := ValueToJSON(payloadVal)
	if err != nil {
		return "", fmt.Errorf("payload: %w", err)
	}
	payloadBytes, err := json.Marshal(payloadJSON)
	if err != nil {
		return "", fmt.Errorf("payload encoding: %w", err)
	}
	headerJSON := fmt.Sprintf(`{"alg":%q,"typ":"JWT"}`, alg)
	header := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload, nil
}

func jwtDecodePayload(b64 string) (runtime.Value, error) {
	payloadBytes, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return runtime.Null{}, nil
	}
	value, parseErr := ParseJSONText(string(payloadBytes))
	if parseErr != nil {
		return runtime.Null{}, nil
	}
	return value, nil
}
