package lower

import "fmt"

type Error struct {
	File    string
	Line    int
	Column  int
	Message string
	Hint    string
}

func (e Error) Error() string {
	out := fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Message)
	if e.Hint != "" {
		out += " (hint: " + e.Hint + ")"
	}
	return out
}
