package native

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"geblang/internal/runtime"
)

// FormatValueWithSpec formats a runtime.Value using a Python-style
// format spec subset: [[fill]align][sign][#][0][width][,][.precision][type].
//
// Supported type characters:
//
//	d   integer (decimal)
//	x   hex lowercase
//	X   hex uppercase
//	o   octal
//	b   binary
//	f   fixed-point float
//	e   scientific float
//	g   general float (auto fixed/scientific)
//	s   string
//	%   percentage (multiplied by 100, suffixed with %)
//
// When the type character is omitted, a default is chosen from the
// value's runtime type. An empty spec degrades to plain Inspect / String.
func FormatValueWithSpec(value runtime.Value, spec string) (string, error) {
	parsed, err := parseFormatSpec(spec)
	if err != nil {
		return "", err
	}
	body, err := renderFormatBody(value, parsed)
	if err != nil {
		return "", err
	}
	return padFormatted(body, parsed), nil
}

type formatSpec struct {
	fill      byte
	align     byte // '<', '>', '^', or 0
	sign      byte // '+', '-', ' ', or 0
	hash      bool
	zero      bool
	width     int
	hasWidth  bool
	comma     bool
	precision int
	hasPrec   bool
	typeChar  byte
}

func parseFormatSpec(spec string) (formatSpec, error) {
	var s formatSpec
	s.fill = ' '
	i := 0
	if len(spec) >= 2 {
		if isAlignChar(spec[1]) {
			s.fill = spec[0]
			s.align = spec[1]
			i = 2
		}
	}
	if s.align == 0 && i < len(spec) && isAlignChar(spec[i]) {
		s.align = spec[i]
		i++
	}
	if i < len(spec) && (spec[i] == '+' || spec[i] == '-' || spec[i] == ' ') {
		s.sign = spec[i]
		i++
	}
	if i < len(spec) && spec[i] == '#' {
		s.hash = true
		i++
	}
	if i < len(spec) && spec[i] == '0' {
		s.zero = true
		i++
	}
	for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
		s.width = s.width*10 + int(spec[i]-'0')
		s.hasWidth = true
		i++
	}
	if i < len(spec) && spec[i] == ',' {
		s.comma = true
		i++
	}
	if i < len(spec) && spec[i] == '.' {
		i++
		s.hasPrec = true
		for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
			s.precision = s.precision*10 + int(spec[i]-'0')
			i++
		}
	}
	if i < len(spec) {
		s.typeChar = spec[i]
		i++
	}
	if i != len(spec) {
		return s, fmt.Errorf("unrecognised format spec %q", spec)
	}
	if s.zero && s.align == 0 {
		s.align = '>'
		s.fill = '0'
	}
	return s, nil
}

func isAlignChar(c byte) bool {
	return c == '<' || c == '>' || c == '^'
}

func renderFormatBody(value runtime.Value, s formatSpec) (string, error) {
	switch s.typeChar {
	case 0:
		return defaultFormat(value, s)
	case 'd':
		return formatInteger(value, s, 10, false)
	case 'x':
		return formatInteger(value, s, 16, false)
	case 'X':
		return formatInteger(value, s, 16, true)
	case 'o':
		return formatInteger(value, s, 8, false)
	case 'b':
		return formatInteger(value, s, 2, false)
	case 'f':
		return formatFloat(value, s, 'f')
	case 'e':
		return formatFloat(value, s, 'e')
	case 'g':
		return formatFloat(value, s, 'g')
	case '%':
		return formatPercent(value, s)
	case 's':
		return formatString(value, s)
	}
	return "", fmt.Errorf("unsupported format type %q", string(s.typeChar))
}

func defaultFormat(value runtime.Value, s formatSpec) (string, error) {
	switch v := value.(type) {
	case runtime.SmallInt, runtime.Int:
		return formatInteger(v, s, 10, false)
	case runtime.Decimal, runtime.Float:
		if s.hasPrec {
			return formatFloat(v, s, 'f')
		}
		return formatString(v, s)
	case runtime.String:
		out := v.Value
		if s.hasPrec && len(out) > s.precision {
			out = out[:s.precision]
		}
		return out, nil
	case runtime.Bool:
		if v.Value {
			return "true", nil
		}
		return "false", nil
	case runtime.Null:
		return "null", nil
	}
	return value.Inspect(), nil
}

func formatInteger(value runtime.Value, s formatSpec, base int, upperHex bool) (string, error) {
	var sb strings.Builder
	bi, ok := integerBigValue(value)
	if !ok {
		return "", fmt.Errorf("format type %q expects integer, got %s", typeCharDisplay(s.typeChar), value.TypeName())
	}
	signByte := signPrefix(bi.Sign() < 0, s.sign)
	absBI := new(big.Int).Abs(bi)
	digits := absBI.Text(base)
	if upperHex {
		digits = strings.ToUpper(digits)
	}
	if s.comma && base == 10 {
		digits = addThousands(digits, ',')
	}
	if s.hash {
		switch base {
		case 16:
			if upperHex {
				sb.WriteString("0X")
			} else {
				sb.WriteString("0x")
			}
		case 8:
			sb.WriteString("0o")
		case 2:
			sb.WriteString("0b")
		}
	}
	if signByte != 0 {
		sb.WriteByte(signByte)
	}
	sb.WriteString(digits)
	return sb.String(), nil
}

func formatFloat(value runtime.Value, s formatSpec, verb byte) (string, error) {
	f, ok := floatValue(value)
	if !ok {
		return "", fmt.Errorf("format type %q expects numeric, got %s", string(verb), value.TypeName())
	}
	precision := s.precision
	if !s.hasPrec {
		if verb == 'g' {
			precision = -1
		} else {
			precision = 6
		}
	}
	body := strconv.FormatFloat(absFloat(f), verb, precision, 64)
	if s.comma && verb == 'f' {
		body = addThousandsFloat(body, ',')
	}
	signByte := signPrefix(f < 0, s.sign)
	if signByte != 0 {
		return string(signByte) + body, nil
	}
	return body, nil
}

func formatPercent(value runtime.Value, s formatSpec) (string, error) {
	f, ok := floatValue(value)
	if !ok {
		return "", fmt.Errorf("format type %% expects numeric, got %s", value.TypeName())
	}
	scaled := f * 100
	precision := s.precision
	if !s.hasPrec {
		precision = 6
	}
	body := strconv.FormatFloat(absFloat(scaled), 'f', precision, 64) + "%"
	signByte := signPrefix(scaled < 0, s.sign)
	if signByte != 0 {
		return string(signByte) + body, nil
	}
	return body, nil
}

func formatString(value runtime.Value, s formatSpec) (string, error) {
	var raw string
	if str, ok := value.(runtime.String); ok {
		raw = str.Value
	} else if dec, ok := value.(runtime.Decimal); ok {
		raw = dec.Inspect()
	} else if v, ok := value.(runtime.Float); ok {
		raw = strconv.FormatFloat(v.Value, 'g', -1, 64)
	} else if v, ok := value.(runtime.SmallInt); ok {
		raw = strconv.FormatInt(v.Value, 10)
	} else if v, ok := value.(runtime.Int); ok {
		raw = v.Value.String()
	} else {
		raw = value.Inspect()
	}
	if s.hasPrec && len(raw) > s.precision {
		raw = raw[:s.precision]
	}
	return raw, nil
}

func padFormatted(body string, s formatSpec) string {
	if !s.hasWidth || len([]rune(body)) >= s.width {
		return body
	}
	pad := s.width - len([]rune(body))
	switch s.align {
	case '<':
		return body + strings.Repeat(string(s.fill), pad)
	case '^':
		left := pad / 2
		right := pad - left
		return strings.Repeat(string(s.fill), left) + body + strings.Repeat(string(s.fill), right)
	case 0, '>':
		return strings.Repeat(string(s.fill), pad) + body
	}
	return body
}

func signPrefix(negative bool, mode byte) byte {
	if negative {
		return '-'
	}
	switch mode {
	case '+':
		return '+'
	case ' ':
		return ' '
	}
	return 0
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func integerBigValue(value runtime.Value) (*big.Int, bool) {
	switch v := value.(type) {
	case runtime.SmallInt:
		return big.NewInt(v.Value), true
	case runtime.Int:
		return new(big.Int).Set(v.Value), true
	}
	return nil, false
}

func floatValue(value runtime.Value) (float64, bool) {
	switch v := value.(type) {
	case runtime.SmallInt:
		return float64(v.Value), true
	case runtime.Int:
		f, _ := new(big.Float).SetInt(v.Value).Float64()
		return f, true
	case runtime.Float:
		return v.Value, true
	case runtime.Decimal:
		f, _ := v.Value.Float64()
		return f, true
	}
	return 0, false
}

func addThousands(digits string, sep byte) string {
	if len(digits) <= 3 {
		return digits
	}
	var sb strings.Builder
	first := len(digits) % 3
	if first > 0 {
		sb.WriteString(digits[:first])
		if len(digits) > first {
			sb.WriteByte(sep)
		}
	}
	for i := first; i < len(digits); i += 3 {
		sb.WriteString(digits[i : i+3])
		if i+3 < len(digits) {
			sb.WriteByte(sep)
		}
	}
	return sb.String()
}

func addThousandsFloat(body string, sep byte) string {
	dot := strings.IndexByte(body, '.')
	if dot < 0 {
		return addThousands(body, sep)
	}
	return addThousands(body[:dot], sep) + body[dot:]
}

func typeCharDisplay(c byte) string {
	if c == 0 {
		return "default"
	}
	return string(c)
}
