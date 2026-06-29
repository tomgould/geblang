package native

import (
	"fmt"
	"geblang/internal/runtime"
)

func registerXML(r *Registry) {
	r.Register("xml", "validate", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "xml.validate")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: ValidateXML(text)}, nil
	})
	r.Register("xml", "parse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "xml.parse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseXML(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return value, nil
	})
	r.Register("xml", "tryParse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "xml.tryParse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseXML(text)
		if parseErr != nil {
			return ParseResult(false, runtime.Null{}, parseErr), nil
		}
		return ParseResult(true, value, nil), nil
	})
	r.Register("xml", "parseAs", func(args []runtime.Value) (runtime.Value, error) {
		return xmlParseAsCtx(ConversionContext{}, args)
	})
	r.Register("xml", "stringify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("xml.stringify expects exactly one argument")
		}
		text, err := StringifyXML(args[0])
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: text}, nil
	})
	r.Register("xml", "validateDetailed", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "xml.validateDetailed")
		if err != nil {
			return nil, err
		}
		parseErr := ValidateXMLDetailed(text)
		if parseErr != nil {
			return ValidationResult(false, parseErr), nil
		}
		return ValidationResult(true, nil), nil
	})
}

func xmlParseAsCtx(ctx ConversionContext, args []runtime.Value) (runtime.Value, error) {
	text, class, err := parseAsArgs(args, "xml.parseAs")
	if err != nil {
		return nil, err
	}
	value, parseErr := ParseXML(text)
	if parseErr != nil {
		return nil, fmt.Errorf("%s", parseErr.Message)
	}
	return deserializeIntoClassCtx(ctx, class, value)
}
