package transpiler

import (
	"fmt"

	"geblang/internal/token"
)

type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	default:
		return "unknown"
	}
}

type Diagnostic struct {
	Severity Severity
	File     string
	Line     int
	Column   int
	Message  string
	Hint     string
}

func (d Diagnostic) String() string {
	out := fmt.Sprintf("%s:%d:%d: %s: %s", d.File, d.Line, d.Column, d.Severity, d.Message)
	if d.Hint != "" {
		out += "\n  hint: " + d.Hint
	}
	return out
}

func ErrorAt(file string, tok token.Token, message, hint string) Diagnostic {
	return Diagnostic{
		Severity: SeverityError,
		File:     file,
		Line:     tok.Line,
		Column:   tok.Column,
		Message:  message,
		Hint:     hint,
	}
}

func WarningAt(file string, tok token.Token, message, hint string) Diagnostic {
	return Diagnostic{
		Severity: SeverityWarning,
		File:     file,
		Line:     tok.Line,
		Column:   tok.Column,
		Message:  message,
		Hint:     hint,
	}
}
