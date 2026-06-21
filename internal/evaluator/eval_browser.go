package evaluator

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"geblang/internal/ast"
	"geblang/internal/cdp"
	"geblang/internal/native"
	"geblang/internal/runtime"
)

type browserRoute struct {
	pattern string
	handler runtime.Function
	page    *cdp.Page
}

// jsLit JSON-encodes a string into a safe JavaScript string literal.
func jsLit(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (e *Evaluator) browserLaunch(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects (opts?)", call.Callee.String())
	}
	if err := native.RequireBrowser("browser.launch"); err != nil {
		return nil, err
	}
	opts := cdp.LaunchOptions{Headless: true}
	if len(args) == 1 {
		o, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("browser.launch: opts must be a dict")
		}
		if v, ok := dictStringField(o, "executable"); ok {
			opts.Executable = v
		}
		if v, ok := dictField(o, "headless"); ok {
			if b, ok := v.(runtime.Bool); ok {
				opts.Headless = b.Value
			}
		}
		if v, ok := dictField(o, "timeoutMs"); ok {
			if n, ok := native.AsInt64(v); ok {
				opts.Timeout = time.Duration(n) * time.Millisecond
			}
		}
		if v, ok := dictField(o, "args"); ok {
			if list, ok := v.(*runtime.List); ok {
				for _, el := range list.Elements {
					if s, ok := el.(runtime.String); ok {
						opts.Args = append(opts.Args, s.Value)
					}
				}
			}
		}
	}
	b, err := cdp.Launch(opts)
	if err != nil {
		return nil, fmt.Errorf("browser.launch: %w", err)
	}
	e.browserMu.Lock()
	id := e.nextBrowserID
	e.nextBrowserID++
	e.browsers[id] = b
	e.browserMu.Unlock()
	return &runtime.Instance{Class: e.browserClass(), Fields: map[string]runtime.Value{"handle": runtime.NewInt64(id)}}, nil
}

func (e *Evaluator) browserClass() *runtime.Class {
	if e.browserClassCache != nil {
		return e.browserClassCache
	}
	get := func(this *runtime.Instance) (*cdp.Browser, error) {
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("invalid browser handle")
		}
		e.browserMu.Lock()
		defer e.browserMu.Unlock()
		b, ok := e.browsers[id.Value.Int64()]
		if !ok {
			return nil, fmt.Errorf("browser is closed")
		}
		return b, nil
	}
	cls := &runtime.Class{Name: "Browser", Module: "browser", Fields: []runtime.Field{{Name: "handle"}}, Methods: map[string][]runtime.Function{}}
	cls.Methods["newpage"] = []runtime.Function{{Name: "newPage", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		b, err := get(this)
		if err != nil {
			return nil, err
		}
		p, err := b.NewPage()
		if err != nil {
			return nil, err
		}
		e.browserMu.Lock()
		pid := e.nextPageID
		e.nextPageID++
		e.pages[pid] = p
		e.browserMu.Unlock()
		return &runtime.Instance{Class: e.pageClass(), Fields: map[string]runtime.Value{"handle": runtime.NewInt64(pid)}}, nil
	}}}
	cls.Methods["version"] = []runtime.Function{{Name: "version", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		b, err := get(this)
		if err != nil {
			return nil, err
		}
		v, err := b.Version()
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: v}, nil
	}}}
	cls.Methods["pages"] = []runtime.Function{{Name: "pages", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		b, err := get(this)
		if err != nil {
			return nil, err
		}
		ids, err := b.PageTargets()
		if err != nil {
			return nil, err
		}
		e.browserMu.Lock()
		byTarget := map[string]int64{}
		for pid, pg := range e.pages {
			byTarget[pg.TargetID] = pid
		}
		e.browserMu.Unlock()
		out := make([]runtime.Value, 0, len(ids))
		for _, tid := range ids {
			pid, ok := byTarget[tid]
			if !ok {
				pg, aerr := b.AttachPage(tid)
				if aerr != nil {
					continue
				}
				e.browserMu.Lock()
				pid = e.nextPageID
				e.nextPageID++
				e.pages[pid] = pg
				e.browserMu.Unlock()
			}
			out = append(out, &runtime.Instance{Class: e.pageClass(), Fields: map[string]runtime.Value{"handle": runtime.NewInt64(pid)}})
		}
		return &runtime.List{Elements: out}, nil
	}}}
	cls.Methods["close"] = []runtime.Function{{Name: "close", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return runtime.Null{}, nil
		}
		e.browserMu.Lock()
		b := e.browsers[id.Value.Int64()]
		delete(e.browsers, id.Value.Int64())
		e.browserMu.Unlock()
		if b != nil {
			b.Close()
		}
		return runtime.Null{}, nil
	}}}
	e.browserClassCache = cls
	return cls
}

func (e *Evaluator) pageClass() *runtime.Class {
	if e.pageClassCache != nil {
		return e.pageClassCache
	}
	get := func(this *runtime.Instance) (*cdp.Page, error) {
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("invalid page handle")
		}
		e.browserMu.Lock()
		defer e.browserMu.Unlock()
		p, ok := e.pages[id.Value.Int64()]
		if !ok {
			return nil, fmt.Errorf("page is closed")
		}
		return p, nil
	}
	cls := &runtime.Class{Name: "Page", Module: "browser", Fields: []runtime.Field{{Name: "handle"}}, Methods: map[string][]runtime.Function{}}
	method := func(name string, fn func(p *cdp.Page, args []runtime.Value) (runtime.Value, error)) {
		cls.Methods[name] = []runtime.Function{{Name: name, Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
			p, err := get(this)
			if err != nil {
				return nil, err
			}
			return fn(p, args)
		}}}
	}
	method("goto", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Page.goto expects (url)")
		}
		url, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Page.goto: url must be a string")
		}
		if _, err := p.Send("Page.navigate", map[string]any{"url": url.Value}); err != nil {
			return nil, err
		}
		return runtime.Null{}, waitForReady(p, 30*time.Second)
	})
	method("evaluate", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Page.evaluate expects (jsExpression)")
		}
		js, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Page.evaluate: argument must be a string")
		}
		return pageEvaluate(p, js.Value)
	})
	method("content", func(p *cdp.Page, _ []runtime.Value) (runtime.Value, error) {
		return pageEvaluate(p, "document.documentElement.outerHTML")
	})
	method("title", func(p *cdp.Page, _ []runtime.Value) (runtime.Value, error) {
		return pageEvaluate(p, "document.title")
	})
	method("url", func(p *cdp.Page, _ []runtime.Value) (runtime.Value, error) {
		return pageEvaluate(p, "location.href")
	})
	method("waitfor", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("Page.waitFor expects (selector[, timeoutMs])")
		}
		sel, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Page.waitFor: selector must be a string")
		}
		timeout := 30 * time.Second
		if len(args) == 2 {
			if n, ok := native.AsInt64(args[1]); ok {
				timeout = time.Duration(n) * time.Millisecond
			}
		}
		deadline := time.Now().Add(timeout)
		js := fmt.Sprintf("!!document.querySelector(%s)", jsLit(sel.Value))
		for {
			if raw, err := pageEvalRaw(p, js); err == nil && string(raw) == "true" {
				return runtime.Null{}, nil
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("Page.waitFor: %q not found within %v", sel.Value, timeout)
			}
			time.Sleep(25 * time.Millisecond)
		}
	})
	method("click", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		sel, err := selArg(args, "Page.click")
		if err != nil {
			return nil, err
		}
		js := fmt.Sprintf("(() => { const el = document.querySelector(%s); if (!el) throw new Error('no element matches '+%s); el.scrollIntoView({block:'center',inline:'center'}); const r = el.getBoundingClientRect(); return [r.left + r.width/2, r.top + r.height/2]; })()", jsLit(sel), jsLit(sel))
		raw, err := pageEvalRaw(p, js)
		if err != nil {
			return nil, err
		}
		var xy []float64
		if json.Unmarshal(raw, &xy) != nil || len(xy) != 2 {
			return nil, fmt.Errorf("Page.click: %q is not clickable", sel)
		}
		for _, kind := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
			params := map[string]any{"type": kind, "x": xy[0], "y": xy[1]}
			if kind != "mouseMoved" {
				params["button"] = "left"
				params["clickCount"] = 1
			}
			if _, err := p.Send("Input.dispatchMouseEvent", params); err != nil {
				return nil, err
			}
		}
		return runtime.Null{}, nil
	})
	method("type", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		sel, text, err := twoStrings(args, "Page.type", "text")
		if err != nil {
			return nil, err
		}
		js := fmt.Sprintf("(() => { const el = document.querySelector(%s); if (!el) throw new Error('no element matches '+%s); el.focus(); })()", jsLit(sel), jsLit(sel))
		if _, err := pageEvalRaw(p, js); err != nil {
			return nil, err
		}
		_, err = p.Send("Input.insertText", map[string]any{"text": text})
		return runtime.Null{}, err
	})
	method("fill", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		sel, val, err := twoStrings(args, "Page.fill", "value")
		if err != nil {
			return nil, err
		}
		js := fmt.Sprintf("(() => { const el = document.querySelector(%s); if (!el) throw new Error('no element matches '+%s); el.value = %s; el.dispatchEvent(new Event('input', {bubbles:true})); el.dispatchEvent(new Event('change', {bubbles:true})); })()", jsLit(sel), jsLit(sel), jsLit(val))
		_, err = pageEvalRaw(p, js)
		return runtime.Null{}, err
	})
	method("press", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		key, err := selArg(args, "Page.press")
		if err != nil {
			return nil, err
		}
		for _, t := range []string{"keyDown", "keyUp"} {
			params := map[string]any{"type": t, "key": key, "code": key}
			if key == "Enter" {
				params["windowsVirtualKeyCode"] = 13
				if t == "keyDown" {
					params["text"] = "\r"
				}
			}
			if _, err := p.Send("Input.dispatchKeyEvent", params); err != nil {
				return nil, err
			}
		}
		return runtime.Null{}, nil
	})
	method("select", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		sel, val, err := twoStrings(args, "Page.select", "value")
		if err != nil {
			return nil, err
		}
		js := fmt.Sprintf("(() => { const el = document.querySelector(%s); if (!el) throw new Error('no element matches '+%s); el.value = %s; el.dispatchEvent(new Event('change', {bubbles:true})); })()", jsLit(sel), jsLit(sel), jsLit(val))
		_, err = pageEvalRaw(p, js)
		return runtime.Null{}, err
	})
	method("text", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		sel, err := selArg(args, "Page.text")
		if err != nil {
			return nil, err
		}
		return pageEvaluate(p, fmt.Sprintf("(() => { const el = document.querySelector(%s); return el ? el.textContent : null; })()", jsLit(sel)))
	})
	method("attribute", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		sel, name, err := twoStrings(args, "Page.attribute", "name")
		if err != nil {
			return nil, err
		}
		return pageEvaluate(p, fmt.Sprintf("(() => { const el = document.querySelector(%s); return el ? el.getAttribute(%s) : null; })()", jsLit(sel), jsLit(name)))
	})
	method("reload", func(p *cdp.Page, _ []runtime.Value) (runtime.Value, error) {
		if _, err := p.Send("Page.reload", nil); err != nil {
			return nil, err
		}
		return runtime.Null{}, waitForReady(p, 30*time.Second)
	})
	method("screenshot", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		path, err := selArg(args, "Page.screenshot")
		if err != nil {
			return nil, err
		}
		return browserCapture(p, "Page.captureScreenshot", map[string]any{"format": "png"}, path)
	})
	method("pdf", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		path, err := selArg(args, "Page.pdf")
		if err != nil {
			return nil, err
		}
		return browserCapture(p, "Page.printToPDF", map[string]any{}, path)
	})
	method("cookies", func(p *cdp.Page, _ []runtime.Value) (runtime.Value, error) {
		res, err := p.Send("Network.getCookies", nil)
		if err != nil {
			return nil, err
		}
		var r struct {
			Cookies json.RawMessage `json:"cookies"`
		}
		if err := json.Unmarshal(res, &r); err != nil || len(r.Cookies) == 0 {
			return &runtime.List{}, nil
		}
		val, perr := native.ParseJSONText(string(r.Cookies))
		if perr != nil {
			return &runtime.List{}, nil
		}
		return val, nil
	})
	method("setcookie", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Page.setCookie expects (cookie)")
		}
		d, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("Page.setCookie: argument must be a dict")
		}
		raw, err := native.ValueToJSON(d)
		if err != nil {
			return nil, err
		}
		params, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Page.setCookie: argument must be a dict")
		}
		_, err = p.Send("Network.setCookie", params)
		return runtime.Null{}, err
	})
	method("clearcookies", func(p *cdp.Page, _ []runtime.Value) (runtime.Value, error) {
		_, err := p.Send("Network.clearBrowserCookies", nil)
		return runtime.Null{}, err
	})
	method("route", func(p *cdp.Page, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("Page.route expects (urlPattern, handler)")
		}
		pattern, ok := args[0].(runtime.String)
		handler, ok2 := args[1].(runtime.Function)
		if !ok || !ok2 {
			return nil, fmt.Errorf("Page.route: (urlPattern, handler) expected")
		}
		if _, err := p.Send("Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": pattern.Value}}}); err != nil {
			return nil, err
		}
		e.browserMu.Lock()
		if e.browserRoutes == nil {
			e.browserRoutes = map[string][]browserRoute{}
		}
		e.browserRoutes[p.SessionID] = append(e.browserRoutes[p.SessionID], browserRoute{pattern: pattern.Value, handler: handler, page: p})
		e.browserMu.Unlock()
		p.Browser().OnEvent("Fetch.requestPaused", e.handleFetchPaused)
		return runtime.Null{}, nil
	})
	cls.Methods["close"] = []runtime.Function{{Name: "close", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return runtime.Null{}, nil
		}
		e.browserMu.Lock()
		p := e.pages[id.Value.Int64()]
		delete(e.pages, id.Value.Int64())
		e.browserMu.Unlock()
		if p != nil {
			p.Close()
		}
		return runtime.Null{}, nil
	}}}
	e.pageClassCache = cls
	return cls
}

// waitForReady polls document.readyState until the page has finished loading or the deadline passes.
func waitForReady(p *cdp.Page, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		v, err := pageEvaluate(p, "document.readyState")
		if err == nil {
			if s, ok := v.(runtime.String); ok && (s.Value == "complete" || s.Value == "interactive") {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// pageEvalRaw runs JS in the page and returns the raw JSON result value, surfacing JS exceptions as an error.
func pageEvalRaw(p *cdp.Page, js string) (json.RawMessage, error) {
	res, err := p.Send("Runtime.evaluate", map[string]any{"expression": js, "returnByValue": true, "awaitPromise": true})
	if err != nil {
		return nil, err
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	if r.ExceptionDetails != nil {
		msg := r.ExceptionDetails.Exception.Description
		if msg == "" {
			msg = r.ExceptionDetails.Text
		}
		return nil, fmt.Errorf("page.evaluate: %s", msg)
	}
	return r.Result.Value, nil
}

func pageEvaluate(p *cdp.Page, js string) (runtime.Value, error) {
	raw, err := pageEvalRaw(p, js)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return runtime.Null{}, nil
	}
	val, perr := native.ParseJSONText(string(raw))
	if perr != nil {
		return runtime.Null{}, nil
	}
	return val, nil
}

func selArg(args []runtime.Value, name string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%s expects one string argument", name)
	}
	s, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("%s: argument must be a string", name)
	}
	return s.Value, nil
}

func twoStrings(args []runtime.Value, name, second string) (string, string, error) {
	if len(args) != 2 {
		return "", "", fmt.Errorf("%s expects (selector, %s)", name, second)
	}
	a, ok := args[0].(runtime.String)
	b, ok2 := args[1].(runtime.String)
	if !ok || !ok2 {
		return "", "", fmt.Errorf("%s: (selector, %s) must be strings", name, second)
	}
	return a.Value, b.Value, nil
}

func browserCapture(p *cdp.Page, method string, params map[string]any, path string) (runtime.Value, error) {
	res, err := p.Send(method, params)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

// handleFetchPaused runs on the CDP read goroutine; it dispatches the Geblang route handler on its own goroutine so the read loop stays free to receive the resulting Fetch responses.
func (e *Evaluator) handleFetchPaused(sessionID string, params json.RawMessage) {
	var ev struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
		} `json:"request"`
		ResourceType string `json:"resourceType"`
	}
	if json.Unmarshal(params, &ev) != nil {
		return
	}
	e.browserMu.Lock()
	routes := append([]browserRoute(nil), e.browserRoutes[sessionID]...)
	e.browserMu.Unlock()

	var page *cdp.Page
	var handler runtime.Function
	for _, r := range routes {
		if matchURLPattern(r.pattern, ev.Request.URL) {
			page = r.page
			handler = r.handler
			break
		}
	}
	if page == nil {
		if len(routes) > 0 {
			_, _ = routes[0].page.Send("Fetch.continueRequest", map[string]any{"requestId": ev.RequestID})
		}
		return
	}

	go func() {
		reqDict := fetchRequestDict(ev.Request.URL, ev.Request.Method, ev.Request.Headers, ev.ResourceType)
		callbackEval, callbackHandler := e.callbackEvaluator(handler)
		if callbackEval != e {
			defer callbackEval.Cleanup()
		}
		result, err := callbackEval.applyFunction(callbackHandler, []runtime.Value{reqDict})
		if err != nil {
			_, _ = page.Send("Fetch.failRequest", map[string]any{"requestId": ev.RequestID, "errorReason": "Failed"})
			return
		}
		dict, isDict := result.(runtime.Dict)
		if !isDict {
			_, _ = page.Send("Fetch.continueRequest", map[string]any{"requestId": ev.RequestID})
			return
		}
		if v, ok := dictField(dict, "abort"); ok {
			if b, ok := v.(runtime.Bool); ok && b.Value {
				_, _ = page.Send("Fetch.failRequest", map[string]any{"requestId": ev.RequestID, "errorReason": "Aborted"})
				return
			}
		}
		fulfill := map[string]any{"requestId": ev.RequestID, "responseCode": 200}
		if v, ok := dictField(dict, "status"); ok {
			if n, ok := toInt64(v); ok {
				fulfill["responseCode"] = int(n)
			}
		}
		if v, ok := dictField(dict, "body"); ok {
			if s, ok := v.(runtime.String); ok {
				fulfill["body"] = base64.StdEncoding.EncodeToString([]byte(s.Value))
			}
		}
		if v, ok := dictField(dict, "headers"); ok {
			if hd, ok := v.(runtime.Dict); ok {
				var hdrs []map[string]any
				hd.ForEachEntry(func(_ string, entry runtime.DictEntry) bool {
					k, _ := entry.Key.(runtime.String)
					val, _ := entry.Value.(runtime.String)
					hdrs = append(hdrs, map[string]any{"name": k.Value, "value": val.Value})
					return true
				})
				fulfill["responseHeaders"] = hdrs
			}
		}
		_, _ = page.Send("Fetch.fulfillRequest", fulfill)
	}()
}

func fetchRequestDict(url, method string, headers map[string]string, resourceType string) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	put := func(k string, v runtime.Value) {
		key := runtime.String{Value: k}
		entries[dictKey(key)] = runtime.DictEntry{Key: key, Value: v}
	}
	put("url", runtime.String{Value: url})
	put("method", runtime.String{Value: method})
	put("resourceType", runtime.String{Value: resourceType})
	hdr := map[string]runtime.DictEntry{}
	for k, v := range headers {
		key := runtime.String{Value: k}
		hdr[dictKey(key)] = runtime.DictEntry{Key: key, Value: runtime.String{Value: v}}
	}
	put("headers", runtime.Dict{Entries: hdr})
	return runtime.Dict{Entries: entries}
}

// matchURLPattern matches a CDP-style url pattern (only * is a wildcard) against a URL.
func matchURLPattern(pattern, url string) bool {
	var sb strings.Builder
	sb.WriteString("^")
	for _, ch := range pattern {
		if ch == '*' {
			sb.WriteString(".*")
		} else {
			sb.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return false
	}
	return re.MatchString(url)
}
