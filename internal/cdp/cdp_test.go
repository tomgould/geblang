package cdp

import (
	"encoding/json"
	"testing"
	"time"
)

// Live transport test: launch Chrome, version, new page, navigate, evaluate a DOM read. Skips when no Chrome is available.
func TestLaunchNavigateEvaluate(t *testing.T) {
	chrome := FindChrome()
	if chrome == "" {
		t.Skip("no Chrome available (set GEBLANG_CHROME)")
	}
	b, err := Launch(LaunchOptions{Executable: chrome, Headless: true, Args: []string{"--no-sandbox"}, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer b.Close()

	if v, err := b.Version(); err != nil || v == "" {
		t.Fatalf("version: %q %v", v, err)
	}

	p, err := b.NewPage()
	if err != nil {
		t.Fatalf("newPage: %v", err)
	}
	if _, err := p.Send("Page.navigate", map[string]any{"url": "data:text/html,<h1>hello cdp</h1>"}); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	var text string
	for i := 0; i < 60; i++ {
		res, err := p.Send("Runtime.evaluate", map[string]any{
			"expression":    "document.querySelector('h1') && document.querySelector('h1').textContent",
			"returnByValue": true,
		})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		var r struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		}
		_ = json.Unmarshal(res, &r)
		_ = json.Unmarshal(r.Result.Value, &text)
		if text == "hello cdp" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if text != "hello cdp" {
		t.Fatalf("evaluate text: got %q, want %q", text, "hello cdp")
	}
}
