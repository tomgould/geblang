package transpilert

import "testing"

func TestEncodingBase64RoundTrip(t *testing.T) {
	in := "Geblang rocks! 123"
	enc := EncodingBase64Encode(in)
	if enc != "R2VibGFuZyByb2NrcyEgMTIz" {
		t.Fatalf("base64Encode = %q", enc)
	}
	if got := EncodingBase64Decode(enc); got != in {
		t.Fatalf("base64Decode = %q", got)
	}
}

func TestEncodingBase64UrlUnpadded(t *testing.T) {
	enc := EncodingBase64UrlEncode("subjects?_d=qp+x")
	if enc != "c3ViamVjdHM_X2Q9cXAreA" {
		t.Fatalf("base64UrlEncode = %q", enc)
	}
	if got := EncodingBase64UrlDecode(enc); got != "subjects?_d=qp+x" {
		t.Fatalf("base64UrlDecode = %q", got)
	}
}

func TestEncodingBase32And58AcceptBytes(t *testing.T) {
	in := "Geblang"
	b32 := EncodingBase32Encode(in)
	if got := EncodingBase32Encode(EncodingBase32Decode(b32)); got != b32 {
		t.Fatalf("base32 round trip via bytes = %q, want %q", got, b32)
	}
	b58 := EncodingBase58Encode([]byte(in))
	if got := EncodingBase58Encode(EncodingBase58Decode(b58)); got != b58 {
		t.Fatalf("base58 round trip via bytes = %q, want %q", got, b58)
	}
}

func TestEncodingUrlAndHtml(t *testing.T) {
	if EncodingUrlEncode("a b&c=d/e") != "a+b%26c%3Dd%2Fe" {
		t.Fatalf("urlEncode = %q", EncodingUrlEncode("a b&c=d/e"))
	}
	if EncodingUrlDecode("a+b%26c%3Dd%2Fe") != "a b&c=d/e" {
		t.Fatalf("urlDecode mismatch")
	}
	if EncodingHtmlEscape("<a>A & B</a>") != "&lt;a&gt;A &amp; B&lt;/a&gt;" {
		t.Fatalf("htmlEscape = %q", EncodingHtmlEscape("<a>A & B</a>"))
	}
	if EncodingHtmlUnescape("&lt;a&gt;") != "<a>" {
		t.Fatalf("htmlUnescape mismatch")
	}
}

func TestEncodingBadBase64Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on invalid base64")
		}
	}()
	EncodingBase64Decode("!!!not-valid!!!")
}
