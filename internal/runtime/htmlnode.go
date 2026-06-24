package runtime

// HtmlNode wraps a parsed *html.Node (held as any to keep this package dependency-lean).
type HtmlNode struct {
	Node any
	Tag  string
}

func (v *HtmlNode) TypeName() string { return "html.Node" }

func (v *HtmlNode) Inspect() string {
	if v.Tag != "" {
		return "<html.Node " + v.Tag + ">"
	}
	return "<html.Node>"
}
