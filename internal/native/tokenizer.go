package native

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"geblang/internal/runtime"
)

// WordPiece tokenizer for BERT-family sentence encoders; loads a HuggingFace tokenizer.json (BPE / SentencePiece out of scope).
type wordPieceTokenizer struct {
	vocab            map[string]int
	unkToken         string
	unkID            int
	contPrefix       string
	maxInputChars    int
	lowercase        bool
	stripAccents     bool
	handleChinese    bool
	clsID, sepID     int
	clsTok, sepTok   string
	padID            int
}

type tokenizerJSON struct {
	Normalizer *struct {
		Type            string `json:"type"`
		Lowercase       *bool  `json:"lowercase"`
		StripAccents    *bool  `json:"strip_accents"`
		HandleChinese   *bool  `json:"handle_chinese_chars"`
	} `json:"normalizer"`
	Model struct {
		UnkToken      string         `json:"unk_token"`
		ContPrefix    string         `json:"continuing_subword_prefix"`
		MaxInputChars *int           `json:"max_input_chars_per_word"`
		Vocab         map[string]int `json:"vocab"`
	} `json:"model"`
}

func parseTokenizer(data []byte) (*wordPieceTokenizer, error) {
	var raw tokenizerJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("transformers.tokenize: invalid tokenizer.json: %w", err)
	}
	if len(raw.Model.Vocab) == 0 {
		return nil, fmt.Errorf("transformers.tokenize: tokenizer.json has no model.vocab (only WordPiece tokenizers are supported)")
	}
	t := &wordPieceTokenizer{
		vocab:         raw.Model.Vocab,
		unkToken:      raw.Model.UnkToken,
		contPrefix:    raw.Model.ContPrefix,
		maxInputChars: 100,
		clsTok:        "[CLS]",
		sepTok:        "[SEP]",
		handleChinese: true,
	}
	if t.unkToken == "" {
		t.unkToken = "[UNK]"
	}
	if t.contPrefix == "" {
		t.contPrefix = "##"
	}
	if raw.Model.MaxInputChars != nil {
		t.maxInputChars = *raw.Model.MaxInputChars
	}
	if n := raw.Normalizer; n != nil {
		if n.Lowercase != nil {
			t.lowercase = *n.Lowercase
		}
		// BertNormalizer: strip_accents defaults to the lowercase setting when unset.
		if n.StripAccents != nil {
			t.stripAccents = *n.StripAccents
		} else {
			t.stripAccents = t.lowercase
		}
		if n.HandleChinese != nil {
			t.handleChinese = *n.HandleChinese
		}
	}
	id, ok := t.vocab[t.unkToken]
	if !ok {
		return nil, fmt.Errorf("transformers.tokenize: unk token %q not in vocab", t.unkToken)
	}
	t.unkID = id
	t.clsID = t.vocab[t.clsTok]
	t.sepID = t.vocab[t.sepTok]
	t.padID = t.vocab["[PAD]"]
	return t, nil
}

func (t *wordPieceTokenizer) normalize(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r == 0 || r == 0xFFFD || isControl(r) {
			continue
		}
		if isWhitespace(r) {
			b.WriteRune(' ')
			continue
		}
		if t.handleChinese && isCJK(r) {
			b.WriteByte(' ')
			b.WriteRune(r)
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if t.stripAccents {
		out = stripAccents(out)
	}
	if t.lowercase {
		out = strings.ToLower(out)
	}
	return out
}

// preTokenize splits on whitespace and peels each punctuation rune into its own token.
func preTokenize(text string) []string {
	var tokens []string
	for _, field := range strings.Fields(text) {
		var cur strings.Builder
		flush := func() {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		}
		for _, r := range field {
			if isPunctuation(r) {
				flush()
				tokens = append(tokens, string(r))
			} else {
				cur.WriteRune(r)
			}
		}
		flush()
	}
	return tokens
}

// wordPiece greedily matches the longest vocab subword from each position, prefixing continuations.
func (t *wordPieceTokenizer) wordPiece(word string) []int {
	runes := []rune(word)
	if len(runes) > t.maxInputChars {
		return []int{t.unkID}
	}
	var out []int
	start := 0
	for start < len(runes) {
		end := len(runes)
		curID := -1
		for start < end {
			sub := string(runes[start:end])
			if start > 0 {
				sub = t.contPrefix + sub
			}
			if id, ok := t.vocab[sub]; ok {
				curID = id
				break
			}
			end--
		}
		if curID == -1 {
			return []int{t.unkID}
		}
		out = append(out, curID)
		start = end
	}
	return out
}

func (t *wordPieceTokenizer) encode(text string, maxLen int, addSpecial bool) []int {
	var ids []int
	for _, word := range preTokenize(t.normalize(text)) {
		ids = append(ids, t.wordPiece(word)...)
	}
	limit := maxLen
	if addSpecial {
		limit -= 2
	}
	if limit >= 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	if addSpecial {
		ids = append([]int{t.clsID}, append(ids, t.sepID)...)
	}
	return ids
}

func registerTokenizer(r *Registry) {
	r.Register("transformers", "tokenize", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("transformers.tokenize expects (tokenizerJson, texts[, opts])")
		}
		jsonStr, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("transformers.tokenize: first argument must be the tokenizer.json string")
		}
		texts, ok := args[1].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("transformers.tokenize: second argument must be a list of strings")
		}
		maxLen := 512
		addSpecial := true
		padToMax := false
		if len(args) == 3 {
			if opts, ok := args[2].(runtime.Dict); ok {
				maxLen = dictInt(opts, "maxLength", maxLen)
				if _, present := dictLookup(opts, "addSpecialTokens"); present {
					addSpecial = dictBool(opts, "addSpecialTokens")
				}
				padToMax = dictString(opts, "padding") == "max_length"
			}
		}
		tok, err := cachedTokenizer(jsonStr.Value)
		if err != nil {
			return nil, err
		}

		rows := make([][]int, len(texts.Elements))
		maxRow := 0
		for i, el := range texts.Elements {
			s, ok := el.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("transformers.tokenize: text %d is %s, expected a string", i, el.TypeName())
			}
			rows[i] = tok.encode(s.Value, maxLen, addSpecial)
			if len(rows[i]) > maxRow {
				maxRow = len(rows[i])
			}
		}
		padLen := maxRow
		if padToMax {
			padLen = maxLen
		}

		inputIDs := make([]runtime.Value, len(rows))
		attention := make([]runtime.Value, len(rows))
		tokenTypes := make([]runtime.Value, len(rows))
		for i, row := range rows {
			ids := make([]runtime.Value, padLen)
			mask := make([]runtime.Value, padLen)
			types := make([]runtime.Value, padLen)
			for j := 0; j < padLen; j++ {
				if j < len(row) {
					ids[j] = runtime.SmallInt{Value: int64(row[j])}
					mask[j] = runtime.SmallInt{Value: 1}
				} else {
					ids[j] = runtime.SmallInt{Value: int64(tok.padID)}
					mask[j] = runtime.SmallInt{Value: 0}
				}
				types[j] = runtime.SmallInt{Value: 0}
			}
			inputIDs[i] = &runtime.List{Elements: ids}
			attention[i] = &runtime.List{Elements: mask}
			tokenTypes[i] = &runtime.List{Elements: types}
		}

		out := runtime.NewDictHint(3)
		putTokenField(&out, "input_ids", inputIDs)
		putTokenField(&out, "attention_mask", attention)
		putTokenField(&out, "token_type_ids", tokenTypes)
		return out, nil
	})
}

func putTokenField(d *runtime.Dict, name string, rows []runtime.Value) {
	key := runtime.String{Value: name}
	d.PutEntry(DictKey(key), runtime.DictEntry{Key: key, Value: &runtime.List{Elements: rows}})
}

var tokenizerCache sync.Map // sha256(json) -> *wordPieceTokenizer

func cachedTokenizer(jsonStr string) (*wordPieceTokenizer, error) {
	key := sha256.Sum256([]byte(jsonStr))
	if v, ok := tokenizerCache.Load(key); ok {
		return v.(*wordPieceTokenizer), nil
	}
	t, err := parseTokenizer([]byte(jsonStr))
	if err != nil {
		return nil, err
	}
	tokenizerCache.Store(key, t)
	return t, nil
}

func stripAccents(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isControl(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

func isWhitespace(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
		return true
	}
	return unicode.IsSpace(r)
}

func isPunctuation(r rune) bool {
	if (r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126) {
		return true
	}
	return unicode.IsPunct(r)
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) || (r >= 0xF900 && r <= 0xFAFF)
}
