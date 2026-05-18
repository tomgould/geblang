package sourcedoc

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

type Report struct {
	Files []File `json:"files"`
}

type File struct {
	Path    string `json:"path"`
	Module  string `json:"module,omitempty"`
	Symbols []Item `json:"symbols"`
}

type Item struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Signature  string   `json:"signature,omitempty"`
	Doc        string   `json:"doc,omitempty"`
	Decorators []string `json:"decorators,omitempty"`
	Generics   []string `json:"generics,omitempty"`
	Extends    string   `json:"extends,omitempty"`
	Implements []string `json:"implements,omitempty"`
	Parents    []string `json:"parents,omitempty"`
	Variants   []string `json:"variants,omitempty"`
	Fields     []Item   `json:"fields,omitempty"`
	Methods    []Item   `json:"methods,omitempty"`
	Static     bool     `json:"static,omitempty"`
	Async      bool     `json:"async,omitempty"`
	Exported   bool     `json:"exported"`
}

func Collect(path string) (Report, error) {
	paths, err := collectPaths(path)
	if err != nil {
		return Report{}, err
	}
	report := Report{}
	for _, sourcePath := range paths {
		file, err := collectFile(sourcePath)
		if err != nil {
			return Report{}, err
		}
		if len(file.Symbols) > 0 {
			report.Files = append(report.Files, file)
		}
	}
	return report, nil
}

func collectPaths(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		if filepath.Ext(path) != ".gb" {
			return nil, fmt.Errorf("doc input must be a .gb file or directory")
		}
		return []string{path}, nil
	}
	var paths []string
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "build" || current == filepath.Join("docs", "site") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(current) == ".gb" {
			paths = append(paths, current)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", path, err)
	}
	sort.Strings(paths)
	return paths, nil
}

func collectFile(path string) (File, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", path, err)
	}
	p := parser.New(lexer.New(string(source)))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return File{}, fmt.Errorf("parse %s: %s", path, strings.Join(p.Errors(), "; "))
	}
	file := File{Path: filepath.ToSlash(path)}
	for _, stmt := range program.Statements {
		if module, ok := stmt.(*ast.ModuleStatement); ok {
			file.Module = strings.Join(module.Path, ".")
			break
		}
	}
	hasExports := false
	for _, stmt := range program.Statements {
		if _, ok := stmt.(*ast.ExportStatement); ok {
			hasExports = true
			break
		}
	}
	for _, stmt := range program.Statements {
		exported := false
		if export, ok := stmt.(*ast.ExportStatement); ok {
			stmt = export.Statement
			exported = true
		} else if hasExports {
			continue
		}
		if item, ok := itemFromStatement(stmt, exported); ok {
			file.Symbols = append(file.Symbols, item)
		}
	}
	return file, nil
}

func itemFromStatement(stmt ast.Statement, exported bool) (Item, bool) {
	switch stmt := stmt.(type) {
	case *ast.FunctionStatement:
		return itemFromFunction(stmt, "function", exported), true
	case *ast.ClassStatement:
		item := Item{
			Kind:       "class",
			Name:       identName(stmt.Name),
			Signature:  classSignature(stmt),
			Doc:        stmt.Doc,
			Decorators: decoratorsString(stmt.Decorators),
			Generics:   typeParamsString(stmt.Generics),
			Extends:    typeRefString(stmt.Extends),
			Implements: typeRefsString(stmt.Implements),
			Exported:   exported,
		}
		for _, member := range stmt.Members {
			switch member := member.(type) {
			case *ast.FunctionStatement:
				item.Methods = append(item.Methods, itemFromFunction(member, "method", exported))
			case *ast.DeclarationStatement:
				item.Fields = append(item.Fields, itemFromDeclaration(member, exported))
			}
		}
		return item, true
	case *ast.InterfaceStatement:
		item := Item{
			Kind:      "interface",
			Name:      identName(stmt.Name),
			Signature: interfaceSignature(stmt),
			Doc:       stmt.Doc,
			Generics:  typeParamsString(stmt.Generics),
			Parents:   typeRefsString(stmt.Parents),
			Exported:  exported,
		}
		for _, method := range stmt.Methods {
			item.Methods = append(item.Methods, itemFromSignature(method, exported))
		}
		return item, true
	case *ast.TypeAliasStatement:
		return Item{
			Kind:      "type",
			Name:      identName(stmt.Name),
			Signature: "type " + identName(stmt.Name) + " = " + typeRefString(stmt.Type),
			Exported:  exported,
		}, true
	case *ast.EnumStatement:
		var variants []string
		for _, variant := range stmt.Variants {
			signature := identName(variant.Name)
			if len(variant.FieldTypes) > 0 {
				signature += "(" + strings.Join(typeRefsString(variant.FieldTypes), ", ") + ")"
			}
			variants = append(variants, signature)
		}
		return Item{
			Kind:      "enum",
			Name:      identName(stmt.Name),
			Signature: "enum " + identName(stmt.Name),
			Variants:  variants,
			Exported:  exported,
		}, true
	default:
		return Item{}, false
	}
}

func itemFromFunction(stmt *ast.FunctionStatement, kind string, exported bool) Item {
	return Item{
		Kind:       kind,
		Name:       identName(stmt.Name),
		Signature:  functionSignature(stmt),
		Doc:        stmt.Doc,
		Decorators: decoratorsString(stmt.Decorators),
		Generics:   typeParamsString(stmt.Generics),
		Static:     stmt.Static,
		Async:      stmt.Async,
		Exported:   exported,
	}
}

func itemFromSignature(stmt *ast.FunctionSignature, exported bool) Item {
	return Item{
		Kind:      "method",
		Name:      identName(stmt.Name),
		Signature: signatureString(stmt.Name, false, false, stmt.Generics, stmt.Parameters, stmt.ReturnType),
		Doc:       stmt.Doc,
		Generics:  typeParamsString(stmt.Generics),
		Exported:  exported,
	}
}

func itemFromDeclaration(stmt *ast.DeclarationStatement, exported bool) Item {
	signature := strings.TrimSpace(strings.Join([]string{stmt.Kind, typeRefString(stmt.Type), identName(stmt.Name)}, " "))
	return Item{
		Kind:      "field",
		Name:      identName(stmt.Name),
		Signature: signature,
		Exported:  exported,
	}
}

func WriteJSON(writer io.Writer, report Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func WriteMarkdown(writer io.Writer, report Report) {
	fmt.Fprintln(writer, "# API Documentation")
	if len(report.Files) == 0 {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "No documentable Geblang declarations found.")
		return
	}
	for _, file := range report.Files {
		fmt.Fprintln(writer)
		fmt.Fprintf(writer, "## %s\n", file.Path)
		if file.Module != "" {
			fmt.Fprintf(writer, "\nModule: `%s`\n", file.Module)
		}
		for _, item := range file.Symbols {
			writeItemMarkdown(writer, item, 3)
		}
	}
}

func WritePageMarkdown(writer io.Writer, title string, report Report) {
	fmt.Fprintf(writer, "# %s\n", title)
	if len(report.Files) == 0 {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "No documentable Geblang declarations found.")
		return
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Generated from Geblang source declarations and docblocks.")
	for _, file := range report.Files {
		fmt.Fprintln(writer)
		name := file.Path
		if file.Module != "" {
			name = file.Module
		}
		fmt.Fprintf(writer, "## `%s`\n", name)
		if file.Module != "" {
			fmt.Fprintf(writer, "\nSource: `%s`\n", file.Path)
		}
		for _, item := range file.Symbols {
			writeItemMarkdown(writer, item, 3)
		}
	}
}

func writeItemMarkdown(writer io.Writer, item Item, level int) {
	prefix := strings.Repeat("#", level)
	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "%s %s `%s`\n", prefix, titleKind(item), item.Name)
	if item.Signature != "" {
		fmt.Fprintf(writer, "\n```gb\n%s\n```\n", item.Signature)
	}
	if len(item.Decorators) > 0 {
		fmt.Fprintf(writer, "\nDecorators: `%s`\n", strings.Join(item.Decorators, "`, `"))
	}
	if strings.TrimSpace(item.Doc) != "" {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, strings.TrimSpace(item.Doc))
	}
	if len(item.Variants) > 0 {
		fmt.Fprintln(writer, "\nVariants:")
		for _, variant := range item.Variants {
			fmt.Fprintf(writer, "- `%s`\n", variant)
		}
	}
	for _, field := range item.Fields {
		writeItemMarkdown(writer, field, level+1)
	}
	for _, method := range item.Methods {
		writeItemMarkdown(writer, method, level+1)
	}
}

func titleKind(item Item) string {
	if item.Static && item.Kind == "method" {
		return "Static Method"
	}
	return strings.ToUpper(item.Kind[:1]) + item.Kind[1:]
}

func functionSignature(stmt *ast.FunctionStatement) string {
	return signatureString(stmt.Name, stmt.Async, stmt.Static, stmt.Generics, stmt.Parameters, stmt.ReturnType)
}

func signatureString(name *ast.Identifier, async bool, static bool, generics []*ast.TypeParam, parameters []ast.Parameter, returnType *ast.TypeRef) string {
	parts := []string{}
	if async {
		parts = append(parts, "async")
	}
	if static {
		parts = append(parts, "static")
	}
	parts = append(parts, "func")
	signature := strings.Join(parts, " ") + " " + identName(name)
	if len(generics) > 0 {
		signature += "<" + strings.Join(typeParamsString(generics), ", ") + ">"
	}
	params := make([]string, 0, len(parameters))
	for _, param := range parameters {
		params = append(params, param.String())
	}
	signature += "(" + strings.Join(params, ", ") + ")"
	if returnType != nil {
		signature += ": " + returnType.String()
	}
	return signature
}

func classSignature(stmt *ast.ClassStatement) string {
	signature := "class " + identName(stmt.Name)
	if len(stmt.Generics) > 0 {
		signature += "<" + strings.Join(typeParamsString(stmt.Generics), ", ") + ">"
	}
	if stmt.Extends != nil {
		signature += " extends " + stmt.Extends.String()
	}
	if len(stmt.Implements) > 0 {
		signature += " implements " + strings.Join(typeRefsString(stmt.Implements), ", ")
	}
	return signature
}

func interfaceSignature(stmt *ast.InterfaceStatement) string {
	signature := "interface " + identName(stmt.Name)
	if len(stmt.Generics) > 0 {
		signature += "<" + strings.Join(typeParamsString(stmt.Generics), ", ") + ">"
	}
	if len(stmt.Parents) > 0 {
		signature += " extends " + strings.Join(typeRefsString(stmt.Parents), ", ")
	}
	return signature
}

func typeParamsString(params []*ast.TypeParam) []string {
	out := make([]string, 0, len(params))
	for _, param := range params {
		if param == nil {
			continue
		}
		value := identName(param.Name)
		if param.Constraint != nil {
			value += " implements " + param.Constraint.String()
		}
		out = append(out, value)
	}
	return out
}

func typeRefsString(refs []*ast.TypeRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref != nil {
			out = append(out, ref.String())
		}
	}
	return out
}

func typeRefString(ref *ast.TypeRef) string {
	if ref == nil {
		return ""
	}
	return ref.String()
}

func decoratorsString(decorators []ast.Decorator) []string {
	out := make([]string, 0, len(decorators))
	for _, decorator := range decorators {
		if decorator.Name == nil {
			continue
		}
		value := decorator.Name.String()
		if len(decorator.Arguments) > 0 {
			args := make([]string, 0, len(decorator.Arguments))
			for _, arg := range decorator.Arguments {
				rendered := ""
				if arg.Name != nil {
					rendered += arg.Name.String() + ": "
				}
				if arg.Spread {
					rendered += "..."
				}
				if arg.Value != nil {
					rendered += arg.Value.String()
				}
				args = append(args, rendered)
			}
			value += "(" + strings.Join(args, ", ") + ")"
		}
		out = append(out, value)
	}
	return out
}

func identName(ident *ast.Identifier) string {
	if ident == nil {
		return ""
	}
	return ident.String()
}
