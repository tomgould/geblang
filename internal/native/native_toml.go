package native

import (
	"bytes"
	"fmt"
	"geblang/internal/runtime"
	"strings"

	tomllib "github.com/BurntSushi/toml"
)

func registerTOML(r *Registry) {
	r.Register("toml", "parse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "toml.parse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseTOMLText(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return value, nil
	})
	r.Register("toml", "tryParse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "toml.tryParse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseTOMLText(text)
		if parseErr != nil {
			return ParseResult(false, runtime.Null{}, parseErr), nil
		}
		return ParseResult(true, value, nil), nil
	})
	r.Register("toml", "stringify", func(args []runtime.Value) (runtime.Value, error) {
		return tomlStringifyCtx(ConversionContext{}, args)
	})
	r.Register("toml", "parseAs", func(args []runtime.Value) (runtime.Value, error) {
		return tomlParseAsCtx(ConversionContext{}, args)
	})
	r.Register("toml", "validate", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "toml.validate")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseTOMLText(text)
		return runtime.Bool{Value: parseErr == nil}, nil
	})
	r.Register("toml", "validateDetailed", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "toml.validateDetailed")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseTOMLText(text)
		if parseErr != nil {
			return ValidationResult(false, parseErr), nil
		}
		return ValidationResult(true, nil), nil
	})
}

func tomlStringifyCtx(ctx ConversionContext, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("toml.stringify expects exactly one argument")
	}
	encoded, err := ValueToTOMLCtx(ctx, args[0])
	if err != nil {
		return nil, err
	}
	top, ok := encoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("toml.stringify expects a dict at top level")
	}
	var out bytes.Buffer
	if err := tomllib.NewEncoder(&out).Encode(top); err != nil {
		return nil, err
	}
	return runtime.String{Value: strings.TrimSuffix(out.String(), "\n")}, nil
}

func tomlParseAsCtx(ctx ConversionContext, args []runtime.Value) (runtime.Value, error) {
	text, class, err := parseAsArgs(args, "toml.parseAs")
	if err != nil {
		return nil, err
	}
	value, parseErr := ParseTOMLText(text)
	if parseErr != nil {
		return nil, fmt.Errorf("%s", parseErr.Message)
	}
	return deserializeIntoClassCtx(ctx, class, value)
}
