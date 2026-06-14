package transpilert

import "testing"

func TestCryptHashesHex(t *testing.T) {
	msg := "The quick brown fox"
	cases := map[string]struct {
		got, want string
	}{
		"md5":    {CryptMd5(msg), "a2004f37730b9445670a738fa0fc9ee5"},
		"sha1":   {CryptSha1(msg), "c519c1a06cdbeb2bc499e22137fb48683858b345"},
		"sha256": {CryptSha256(msg), "5cac4f980fedc3d3f1f99b4be3472c9b30d56523e632d151237ec9309048bda9"},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", name, c.got, c.want)
		}
	}
}

func TestCryptHashAcceptsBytes(t *testing.T) {
	if CryptSha256("abc") != CryptSha256([]byte("abc")) {
		t.Fatal("sha256 string vs bytes input differ")
	}
}

func TestCryptCrc32(t *testing.T) {
	if CryptCrc32("The quick brown fox") != 3074782430 {
		t.Fatalf("crc32 = %d", CryptCrc32("The quick brown fox"))
	}
}

func TestCryptHmacSha256(t *testing.T) {
	got := CryptHmacSha256("secret-key", "The quick brown fox")
	if got != "fb8626bea6af7aca505231f3fa99c27995e34bf32128625aac73ac2a67d8b409" {
		t.Fatalf("hmacSha256 = %q", got)
	}
	if len(CryptHmacSha256Bytes("secret-key", "x")) != 32 {
		t.Fatal("hmacSha256Bytes wrong length")
	}
}

func TestCryptBase64(t *testing.T) {
	enc := CryptBase64Encode("The quick brown fox")
	if enc != "VGhlIHF1aWNrIGJyb3duIGZveA==" {
		t.Fatalf("base64Encode = %q", enc)
	}
	if CryptBase64Decode(enc) != "The quick brown fox" {
		t.Fatal("base64Decode mismatch")
	}
}
