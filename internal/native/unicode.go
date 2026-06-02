package native

import (
	"fmt"

	"geblang/internal/runtime"

	"golang.org/x/text/unicode/norm"
)

func registerUnicode(r *Registry) {
	r.Register("unicode", "normalize", func(args []runtime.Value) (runtime.Value, error) {
		s, form, err := parseUnicodeArgs(args, "unicode.normalize")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: form.String(s)}, nil
	})
	r.Register("unicode", "isNormalized", func(args []runtime.Value) (runtime.Value, error) {
		s, form, err := parseUnicodeArgs(args, "unicode.isNormalized")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: form.IsNormalString(s)}, nil
	})
}

func parseUnicodeArgs(args []runtime.Value, label string) (string, norm.Form, error) {
	if len(args) != 2 {
		return "", 0, fmt.Errorf("%s expects (string, form)", label)
	}
	s, ok := args[0].(runtime.String)
	if !ok {
		return "", 0, fmt.Errorf("%s first argument must be string", label)
	}
	formName, ok := args[1].(runtime.String)
	if !ok {
		return "", 0, fmt.Errorf("%s second argument must be string", label)
	}
	form, err := normFormByName(formName.Value)
	if err != nil {
		return "", 0, fmt.Errorf("%s: %s", label, err.Error())
	}
	return s.Value, form, nil
}

func normFormByName(name string) (norm.Form, error) {
	switch name {
	case "NFC":
		return norm.NFC, nil
	case "NFD":
		return norm.NFD, nil
	case "NFKC":
		return norm.NFKC, nil
	case "NFKD":
		return norm.NFKD, nil
	default:
		return 0, fmt.Errorf("unknown form %q (want NFC, NFD, NFKC, NFKD)", name)
	}
}
