package native

import (
	"fmt"
	"geblang/internal/runtime"
	"strings"

	yamllib "gopkg.in/yaml.v3"
)

func registerYAML(r *Registry) {
	r.Register("yaml", "parse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "yaml.parse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseYAMLText(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return value, nil
	})
	r.Register("yaml", "tryParse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "yaml.tryParse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseYAMLText(text)
		if parseErr != nil {
			return ParseResult(false, runtime.Null{}, parseErr), nil
		}
		return ParseResult(true, value, nil), nil
	})
	r.Register("yaml", "stringify", func(args []runtime.Value) (runtime.Value, error) {
		return yamlStringifyCtx(ConversionContext{}, args)
	})
	r.Register("yaml", "parseAs", func(args []runtime.Value) (runtime.Value, error) {
		return yamlParseAsCtx(ConversionContext{}, args)
	})
	r.Register("yaml", "validate", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "yaml.validate")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseYAMLText(text)
		return runtime.Bool{Value: parseErr == nil}, nil
	})
	r.Register("yaml", "validateDetailed", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "yaml.validateDetailed")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseYAMLText(text)
		if parseErr != nil {
			return ValidationResult(false, parseErr), nil
		}
		return ValidationResult(true, nil), nil
	})
}

func yamlStringifyCtx(ctx ConversionContext, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("yaml.stringify expects exactly one argument")
	}
	encoded, err := ValueToYAMLCtx(ctx, args[0])
	if err != nil {
		return nil, err
	}
	data, err := yamllib.Marshal(encoded)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: strings.TrimSuffix(string(data), "\n")}, nil
}

func yamlParseAsCtx(ctx ConversionContext, args []runtime.Value) (runtime.Value, error) {
	text, class, err := parseAsArgs(args, "yaml.parseAs")
	if err != nil {
		return nil, err
	}
	value, parseErr := ParseYAMLText(text)
	if parseErr != nil {
		return nil, fmt.Errorf("%s", parseErr.Message)
	}
	return deserializeIntoClassCtx(ctx, class, value)
}
