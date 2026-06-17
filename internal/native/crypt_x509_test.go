package native

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"geblang/internal/runtime"
)

func cryptRegistry() *Registry {
	r := NewRegistry()
	registerCryptPKI(r)
	registerCryptX509(r)
	return r
}

// syntheticAttestationCert builds a cert with an Android KeyDescription (challenge + origin tag 702).
func syntheticAttestationCert(t *testing.T, challenge []byte) string {
	t.Helper()
	// [702] EXPLICIT INTEGER 0, hand-encoded since asn1.Marshal omits high tag numbers for RawValue.
	originDER := []byte{0xBF, 0x85, 0x3E, 0x03, 0x02, 0x01, 0x00}
	teeEnforced := asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: originDER}
	emptyList := asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: []byte{}}

	kd := keyDescription{
		AttestationVersion:       3,
		AttestationSecurityLevel: 1,
		KeymasterVersion:         4,
		KeymasterSecurityLevel:   1,
		AttestationChallenge:     challenge,
		UniqueID:                 []byte{},
		SoftwareEnforced:         emptyList,
		TeeEnforced:              teeEnforced,
	}
	kdDER, err := asn1.Marshal(kd)
	if err != nil {
		t.Fatalf("marshal KeyDescription: %v", err)
	}
	ext := pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}, Value: kdDER}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:    big.NewInt(1),
		Subject:         pkix.Name{CommonName: "attested key"},
		NotBefore:       time.Now().Add(-time.Hour),
		NotAfter:        time.Now().Add(time.Hour),
		ExtraExtensions: []pkix.Extension{ext},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestParseAndroidAttestation(t *testing.T) {
	r := cryptRegistry()
	certPEM := syntheticAttestationCert(t, []byte("challenge-123"))

	out, err := r.Call("crypt", "parseAndroidAttestation", []runtime.Value{runtime.String{Value: certPEM}})
	if err != nil {
		t.Fatalf("parseAndroidAttestation: %v", err)
	}
	dict, ok := out.(runtime.Dict)
	if !ok {
		t.Fatalf("expected dict, got %T", out)
	}
	get := func(k string) runtime.Value {
		entry, ok := dict.GetEntry(DictKey(runtime.String{Value: k}))
		if !ok {
			t.Fatalf("missing key %q", k)
		}
		return entry.Value
	}
	if lvl, _ := get("attestationSecurityLevel").(runtime.String); lvl.Value != "TrustedEnvironment" {
		t.Errorf("securityLevel = %q, want TrustedEnvironment", lvl.Value)
	}
	if ch, _ := get("attestationChallenge").(runtime.Bytes); string(ch.Value) != "challenge-123" {
		t.Errorf("challenge = %q, want challenge-123", string(ch.Value))
	}
	origin, ok := AsInt64(get("keyOrigin"))
	if !ok || origin != 0 {
		t.Errorf("keyOrigin = %v (ok=%v), want 0", origin, ok)
	}
}

func TestParseAndroidAttestationRejectsPlainCert(t *testing.T) {
	r := cryptRegistry()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "plain"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))

	if _, err := r.Call("crypt", "parseAndroidAttestation", []runtime.Value{runtime.String{Value: certPEM}}); err == nil {
		t.Fatal("expected error for a cert without the attestation extension")
	}
}

func TestAsn1DecodeStructure(t *testing.T) {
	r := cryptRegistry()
	// SEQUENCE { INTEGER 42, OCTET STRING "hi", BOOLEAN true }
	der, err := asn1.Marshal(struct {
		N int
		S []byte
		B bool
	}{42, []byte("hi"), true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := r.Call("crypt", "asn1Decode", []runtime.Value{runtime.Bytes{Value: der}})
	if err != nil {
		t.Fatalf("asn1Decode: %v", err)
	}
	list, ok := out.(*runtime.List)
	if !ok || len(list.Elements) != 3 {
		t.Fatalf("expected 3-element list, got %T %v", out, out)
	}
	if n, ok := AsInt64(list.Elements[0]); !ok || n != 42 {
		t.Errorf("element 0 = %v, want 42", list.Elements[0])
	}
	if b, ok := list.Elements[1].(runtime.Bytes); !ok || string(b.Value) != "hi" {
		t.Errorf("element 1 = %v, want bytes hi", list.Elements[1])
	}
	if b, ok := list.Elements[2].(runtime.Bool); !ok || !b.Value {
		t.Errorf("element 2 = %v, want true", list.Elements[2])
	}
}
