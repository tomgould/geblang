# Headless Browser (`browser`)

> Experimental (1.25.0). Drives a headless Chrome/Chromium over the Chrome
> DevTools Protocol for functional/E2E testing and scripted browser control. The
> surface may change.

The `browser` module launches a real headless browser and lets a Geblang program
navigate, interact with the page, read content, take screenshots, manage cookies,
and intercept requests. It speaks the DevTools Protocol directly (no external
driver), is gated behind the `--allow-browser` launch flag (it spawns a browser
subprocess and opens sockets), and always terminates the browser on close so a
process is never orphaned.

## Setup

Install Chrome or Chromium yourself; it is not bundled. The module finds it via
`opts.executable`, then `$GEBLANG_CHROME`, then common install locations
(`google-chrome`, `chromium`, ...). Run with `--allow-browser`:

```sh
export GEBLANG_CHROME=/usr/bin/google-chrome   # or pass {"executable": "..."}
geblang --allow-browser script.gb
```

In containers Chrome usually needs `--no-sandbox`; pass it through `opts.args`.
Without `--allow-browser`, `browser.launch` raises a `PermissionError`.

## Launching

`browser.launch(opts = {})` returns a `Browser`. `opts`: `headless` (default
`true`), `executable`, `args` (list of extra Chrome flags), `timeoutMs`.

| `Browser` method | Description |
|---|---|
| `newPage()` | Opens a new page/tab; returns a `Page`. |
| `pages()` | All open pages/tabs (including ones the app opened). |
| `version()` | The browser product version string. |
| `close()` | Closes the browser and terminates the process. |

## Pages

```gb
import browser;
import io;

let b = browser.launch({"args": ["--no-sandbox"]});   # geblang --allow-browser
let p = b.newPage();
p.goto("https://example.com");
p.waitFor("h1");
io.println(p.title());
io.println(p.text("h1"));
io.println(p.evaluate("document.querySelectorAll('a').length"));
p.screenshot("example.png");
b.close();
```

| `Page` method | Description |
|---|---|
| `goto(url)` | Navigates and waits for load. |
| `waitFor(selector, timeoutMs = 30000)` | Waits until a selector matches. |
| `click(selector)` | Scrolls to and clicks with real mouse events. |
| `type(selector, text)` | Focuses the element and types text. |
| `fill(selector, value)` | Sets a form field's value and fires input/change. |
| `press(key)` | Presses a key, e.g. `"Enter"`, `"Tab"`. |
| `select(selector, value)` | Selects a dropdown option by value. |
| `evaluate(js)` | Runs JavaScript and returns the JSON-serializable result. |
| `text(selector)` / `attribute(selector, name)` | Read an element's text / attribute (or null). |
| `content()` / `title()` / `url()` | The page HTML / document title / current URL. |
| `reload()` | Reloads and waits for load. |
| `screenshot(path)` / `pdf(path)` | Write a PNG screenshot / PDF to a file. |
| `cookies()` / `setCookie(cookie)` / `clearCookies()` | Read / set / clear cookies. |
| `route(urlPattern, handler)` | Intercept matching requests (see below). |
| `close()` | Closes the page. |

`evaluate` returns the JS value marshalled to a Geblang value; a JavaScript
exception surfaces as a catchable error.

## Request interception

`page.route(urlPattern, handler)` intercepts every request whose URL matches the
pattern (`*` is the wildcard). The handler receives a request dict
(`url`, `method`, `headers`, `resourceType`) and returns one of:

- `null` - let the request proceed unchanged;
- `{"abort": true}` - block the request;
- `{"status": ..., "headers": {...}, "body": "..."}` - fulfill it with a mock
  response.

```gb
p.route("*/api/*", func(dict<string, any> req): ?dict<string, any> {
    if ((req["url"] as string).contains("/api/users")) {
        return {"status": 200, "headers": {"Content-Type": "application/json"},
                "body": "[{\"id\":1,\"name\":\"Ada\"}]"};
    }
    return null;   # everything else proceeds normally
});
```

This makes it straightforward to stub a backend in a functional test or to block
trackers/assets while scripting a page.

## A functional test

```gb
import browser;
import test;

class LoginTest extends test.Test {
    @test
    func logsIn(): void {
        let b = browser.launch({"args": ["--no-sandbox"]});
        let p = b.newPage();
        p.goto("https://app.example.com/login");
        p.fill("#email", "ada@example.com");
        p.fill("#password", "hunter2");
        p.click("button[type=submit]");
        p.waitFor(".dashboard");
        this.assertTrue((p.url() as string).contains("/dashboard"));
        b.close();
    }
}
```

## Building a binary

A built binary (`geblang build`) carries the capability when baked in: declare
`permissions: { browser: true }` in `geblang.yaml`, or pass
`geblang build --allow-browser`. See [Bundling](../13-bundling.md). The end user
still needs Chrome installed on the machine that runs the binary.
