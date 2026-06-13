package native

import (
	"bytes"
	"fmt"
	"geblang/internal/runtime"
	htmltemplate "html/template"
	"os"
	"path/filepath"
	"strings"
)

func registerTemplate(r *Registry) {
	r.Register("template", "renderString", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("template.renderString expects template text and data")
		}
		text, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("template.renderString template text must be string")
		}
		rendered, err := renderTemplateText("inline", text.Value, args[1])
		if err != nil {
			return nil, fmt.Errorf("template.renderString: %v", err)
		}
		return runtime.String{Value: rendered}, nil
	})
	r.Register("template", "Template", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("template.Template expects text and optional name")
		}
		text, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("template.Template text must be string")
		}
		name := "inline"
		if len(args) == 2 {
			nameValue, ok := args[1].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("template.Template name must be string")
			}
			name = nameValue.Value
		}
		return runtime.TemplateValue{Name: name, Text: text.Value}, nil
	})
	r.Register("template", "load", func(args []runtime.Value) (runtime.Value, error) {
		path, err := singleString(args, "template.load")
		if err != nil {
			return nil, err
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("template.load: %v", err)
		}
		return runtime.TemplateValue{Name: filepath.Base(path), Text: string(text), Path: path}, nil
	})
	r.Register("template", "Engine", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) > 1 {
			return nil, fmt.Errorf("template.Engine expects optional directory or options dict")
		}
		dir := "templates"
		if len(args) == 1 {
			switch value := args[0].(type) {
			case runtime.String:
				dir = value.Value
			case runtime.Dict:
				if configured := dictString(value, "dir"); configured != "" {
					dir = configured
				} else if configured := dictString(value, "templates"); configured != "" {
					dir = configured
				}
			default:
				return nil, fmt.Errorf("template.Engine expects string or dict")
			}
		}
		return runtime.TemplateEngine{Dir: dir}, nil
	})
}

func TemplateMethod(receiver runtime.TemplateValue, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "name":
		if len(args) != 0 {
			return nil, fmt.Errorf("template.Template.name expects no arguments")
		}
		return runtime.String{Value: receiver.Name}, nil
	case "path":
		if len(args) != 0 {
			return nil, fmt.Errorf("template.Template.path expects no arguments")
		}
		if receiver.Path == "" {
			return runtime.Null{}, nil
		}
		return runtime.String{Value: receiver.Path}, nil
	case "render":
		if len(args) != 1 {
			return nil, fmt.Errorf("template.Template.render expects data")
		}
		rendered, err := renderTemplateText(receiver.Name, receiver.Text, args[0])
		if err != nil {
			return nil, fmt.Errorf("template.Template.render: %v", err)
		}
		return runtime.String{Value: rendered}, nil
	case "toString":
		if len(args) != 0 {
			return nil, fmt.Errorf("template.Template.toString expects no arguments")
		}
		return runtime.String{Value: receiver.Text}, nil
	default:
		return nil, fmt.Errorf("template.Template has no method %s", name)
	}
}

func TemplateEngineMethod(receiver runtime.TemplateEngine, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "dir":
		if len(args) != 0 {
			return nil, fmt.Errorf("template.Engine.dir expects no arguments")
		}
		return runtime.String{Value: receiver.Dir}, nil
	case "load":
		path, err := singleString(args, "template.Engine.load")
		if err != nil {
			return nil, err
		}
		fullPath, err := templatePath(receiver.Dir, path)
		if err != nil {
			return nil, fmt.Errorf("template.Engine.load: %v", err)
		}
		text, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("template.Engine.load: %v", err)
		}
		return runtime.TemplateValue{Name: path, Text: string(text), Path: fullPath}, nil
	case "render":
		if len(args) != 2 {
			return nil, fmt.Errorf("template.Engine.render expects name and data")
		}
		name, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("template.Engine.render name must be string")
		}
		fullPath, err := templatePath(receiver.Dir, name.Value)
		if err != nil {
			return nil, fmt.Errorf("template.Engine.render: %v", err)
		}
		text, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("template.Engine.render: %v", err)
		}
		rendered, err := renderTemplateText(name.Value, string(text), args[1])
		if err != nil {
			return nil, fmt.Errorf("template.Engine.render: %v", err)
		}
		return runtime.String{Value: rendered}, nil
	default:
		return nil, fmt.Errorf("template.Engine has no method %s", name)
	}
}

func renderTemplateText(name string, text string, data runtime.Value) (string, error) {
	tmpl, err := htmltemplate.New(name).Parse(text)
	if err != nil {
		return "", err
	}
	goData, err := ValueToTemplateData(data)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, goData); err != nil {
		return "", err
	}
	return out.String(), nil
}

func templatePath(dir string, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("template name is required")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("template name must be relative")
	}
	cleanName := filepath.Clean(name)
	if cleanName == "." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) || cleanName == ".." {
		return "", fmt.Errorf("template name escapes template directory")
	}
	return filepath.Join(dir, cleanName), nil
}
