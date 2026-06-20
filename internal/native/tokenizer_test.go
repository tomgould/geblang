package native

import "testing"

const testTokJSON = `{"normalizer":{"type":"BertNormalizer","lowercase":true,"strip_accents":true},` +
	`"model":{"type":"WordPiece","unk_token":"[UNK]","continuing_subword_prefix":"##",` +
	`"vocab":{"[PAD]":0,"[UNK]":1,"[CLS]":2,"[SEP]":3,"hello":4,"world":5,"play":6,"##ing":7,"!":8,"cafe":9}}}`

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWordPieceEncode(t *testing.T) {
	tok, err := parseTokenizer([]byte(testTokJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		text string
		want []int
	}{
		{"Hello, World!", []int{2, 4, 1, 5, 8, 3}}, // lowercase + punctuation split; "," is UNK
		{"playing", []int{2, 6, 7, 3}},             // WordPiece continuation play + ##ing
		{"café", []int{2, 9, 3}},                   // strip accents -> cafe
		{"", []int{2, 3}},                          // empty -> just specials
		{"zzz", []int{2, 1, 3}},                    // unknown word -> UNK
	}
	for _, c := range cases {
		got := tok.encode(c.text, 512, true)
		if !equalInts(got, c.want) {
			t.Errorf("encode(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestWordPieceNoSpecialTokens(t *testing.T) {
	tok, _ := parseTokenizer([]byte(testTokJSON))
	got := tok.encode("hello world", 512, false)
	if !equalInts(got, []int{4, 5}) {
		t.Errorf("got %v, want [4 5]", got)
	}
}

func TestWordPieceTruncation(t *testing.T) {
	tok, _ := parseTokenizer([]byte(testTokJSON))
	// maxLen 4 with specials leaves room for 2 content tokens.
	got := tok.encode("hello world play", 4, true)
	if !equalInts(got, []int{2, 4, 5, 3}) {
		t.Errorf("got %v, want [2 4 5 3]", got)
	}
}

func TestParseTokenizerRejectsEmptyVocab(t *testing.T) {
	if _, err := parseTokenizer([]byte(`{"model":{"vocab":{}}}`)); err == nil {
		t.Fatal("expected error for empty vocab")
	}
}
