package transpilert

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// XML helpers mirror internal/native xml functions over encoding/xml (stdlib),
// so --native matches the interpreter byte-for-byte and stays zero-dep. Parsed
// element dicts use keys inserted sorted (the interpreter's nil-Order dicts
// render keys alphabetically).

func XMLValidate(text string) bool { return xmlValidateDetailed(text) == nil }

func xmlValidateDetailed(text string) error {
	decoder := xml.NewDecoder(strings.NewReader(text))
	depth := 0
	roots := 0
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			if roots == 1 && depth == 0 {
				return nil
			}
			return fmt.Errorf("XML document must contain exactly one root element")
		}
		if err != nil {
			return err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 {
					return fmt.Errorf("XML document must contain exactly one root element")
				}
			}
			depth++
		case xml.EndElement:
			depth--
			if depth < 0 {
				return fmt.Errorf("unexpected XML end element %s", tok.Name.Local)
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(tok)) != "" {
				return fmt.Errorf("XML document contains non-whitespace text outside the root element")
			}
		}
	}
}

type xmlNode struct {
	name     string
	attrs    map[string]string
	children []*xmlNode
	text     strings.Builder
}

// XMLParse parses an XML document into a nested element dict, mirroring the
// interpreter's ParseXML; a parse error panics so the uncaught render matches.
func XMLParse(text string) any {
	decoder := xml.NewDecoder(strings.NewReader(text))
	stack := []*xmlNode{}
	var root *xmlNode
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			if root == nil || len(stack) != 0 {
				panic(NewError("RuntimeError", "XML document must contain exactly one root element"))
			}
			return xmlNodeValue(root)
		}
		if err != nil {
			panic(NewError("RuntimeError", err.Error()))
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			node := &xmlNode{name: tok.Name.Local, attrs: map[string]string{}}
			for _, attr := range tok.Attr {
				node.attrs[attr.Name.Local] = attr.Value
			}
			if len(stack) == 0 {
				if root != nil {
					panic(NewError("RuntimeError", "XML document must contain exactly one root element"))
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				panic(NewError("RuntimeError", fmt.Sprintf("unexpected XML end element %s", tok.Name.Local)))
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(tok)) != "" {
					panic(NewError("RuntimeError", "XML document contains non-whitespace text outside the root element"))
				}
				continue
			}
			stack[len(stack)-1].text.WriteString(string(tok))
		}
	}
}

func xmlNodeValue(node *xmlNode) *OrderedDict[string, any] {
	attrKeys := make([]string, 0, len(node.attrs))
	for k := range node.attrs {
		attrKeys = append(attrKeys, k)
	}
	sort.Strings(attrKeys)
	attrs := NewOrderedDict[string, any]()
	for _, k := range attrKeys {
		attrs.Set(k, node.attrs[k])
	}
	children := make([]any, 0, len(node.children))
	for _, child := range node.children {
		children = append(children, xmlNodeValue(child))
	}
	d := NewOrderedDict[string, any]()
	d.Set("attributes", attrs)
	d.Set("children", children)
	d.Set("name", node.name)
	d.Set("text", strings.TrimSpace(node.text.String()))
	return d
}

// XMLStringify backs xml.stringify(dict): serializes an element dict to XML,
// mirroring the interpreter's writeXMLValue (xml.EscapeText for attrs + text).
func XMLStringify(value any) string {
	var out bytes.Buffer
	xmlWriteValue(&out, value)
	return out.String()
}

func xmlWriteValue(out *bytes.Buffer, value any) {
	_, get, ok := xmlStringKeyDict(value)
	if !ok {
		panic(NewError("RuntimeError", "xml.stringify expects an element dict"))
	}
	name := xmlStringField(get, "name")
	out.WriteByte('<')
	out.WriteString(name)
	attrsVal, _ := get("attributes")
	if attrKeys, attrGet, ok := xmlStringKeyDict(attrsVal); ok {
		for _, key := range attrKeys {
			v, _ := attrGet(key)
			value, ok := v.(string)
			if !ok {
				continue
			}
			out.WriteByte(' ')
			out.WriteString(key)
			out.WriteString(`="`)
			xml.EscapeText(out, []byte(value))
			out.WriteByte('"')
		}
	}
	out.WriteByte('>')
	if textVal, ok := get("text"); ok {
		if text, ok := textVal.(string); ok {
			xml.EscapeText(out, []byte(text))
		}
	}
	if childrenVal, ok := get("children"); ok {
		for _, child := range xmlChildList(childrenVal) {
			xmlWriteValue(out, child)
		}
	}
	out.WriteString("</")
	out.WriteString(name)
	out.WriteByte('>')
}

// xmlChildList normalizes the children value to []any; a list of element dicts
// lowers to a homogeneous []*OrderedDict slice, not []any.
func xmlChildList(v any) []any {
	switch list := v.(type) {
	case []any:
		return list
	case []*OrderedDict[string, any]:
		out := make([]any, len(list))
		for i, e := range list {
			out[i] = e
		}
		return out
	}
	return nil
}

// xmlStringKeyDict extracts (keys, getter) from either OrderedDict variant the
// transpiler emits for a string-keyed dict.
func xmlStringKeyDict(arg any) ([]string, func(string) (any, bool), bool) {
	switch d := arg.(type) {
	case *OrderedDict[string, any]:
		return d.Keys(), func(k string) (any, bool) { v, ok := d.Get(k); return v, ok }, true
	case *OrderedDict[string, string]:
		return d.Keys(), func(k string) (any, bool) { v, ok := d.Get(k); return v, ok }, true
	}
	return nil, nil, false
}

func xmlStringField(get func(string) (any, bool), name string) string {
	v, ok := get(name)
	if !ok {
		panic(NewError("RuntimeError", "xml element missing "+name))
	}
	s, ok := v.(string)
	if !ok {
		panic(NewError("RuntimeError", "xml element "+name+" must be string"))
	}
	return s
}
