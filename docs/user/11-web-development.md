# Web Development

Geblang's web story supports APIs and server-rendered applications without
trying to become a large full-stack framework.

The intended shape is Flask-like: a small set of routing, middleware, request,
response, session, cache, and rendering primitives that framework authors can
compose. Applications can use those primitives directly, and larger frameworks
can layer conventions on top.

## Native Web Module

```gb
import http;
import web;

let app = web.new();

web.before(app, func(dict<string, any> request): any {
    return null;
});

web.get(app, "/users/:id", func(dict<string, any> request): dict<string, any> {
    return web.jsonStatus({"id": request["params"]["id"]}, 200);
});

http.serve("127.0.0.1:8080", func(dict<string, any> request): dict<string, any> {
    return web.handle(app, request);
});
```

Middleware registered with `web.before` can short-circuit before the route
handler. `web.use` and `web.after` transform responses after the handler.

Handlers receive a request dictionary and return a response dictionary, a
string, or `null`. Strings are normalized to `200` text responses and `null` is
normalized to `204 No Content`. The request contains method, path, headers,
query/body data where available, and route parameters after routing. Response
helpers build dictionaries with `status`, `headers`, and `body` fields.

Before middleware receives `(request)` and should return `null` to continue or
a response-compatible value to stop the pipeline. Response middleware receives
`(request, response)` and should return the transformed response:

```gb
web.before(app, func(dict<string, any> request): any {
    if (!request["headers"].hasKey("authorization")) {
        return web.jsonStatus({"error": "missing token"}, 401);
    }
    return null;
});

web.use(app, func(dict<string, any> request, dict<string, any> response): dict<string, any> {
    return web.withHeader(response, "X-App", "Geblang");
});
```

## Source Web Modules

Source web modules are split by responsibility: `web.http` handles
request/response/context helpers, `web.router` handles routing and decorator
mounting, `web.session` handles sessions and flash messages, `web.cache`
handles cache stores, `web.auth` handles current-user helpers and CSRF,
`web.validation` wraps schema validation for request input, `web.forms`
provides SSR form helpers, `web.middleware` provides reusable middleware,
`web.sse` formats server-sent events, `web.websocket` handles WebSocket
upgrades and clients, and `web.testing` provides dispatch helpers for route
tests.

```gb
import web.http as wh;
import web.router as router;

let app = router.newRouter();
let api = router.group(app, "/api");

router.get(api, "/users/:id", func(dict<string, any> request): dict<string, any> {
    let ctx = wh.context(request);
    return wh.jsonStatus({"id": ctx.param("id")}, 200);
});
```

Route groups add a prefix without hiding the underlying router:

```gb
let admin = router.group(app, "/admin");

router.before(admin, auth.requireRole(sessionStore, "admin"));
router.get(admin, "/users", listUsers);
router.post(admin, "/users", createUser);
```

`wh.context(request)` provides a higher-level request wrapper for params,
query values, form data, cookies, sessions, rendering, and response helpers.
`wh.requestObject(request)` and `wh.responseFrom(response)` are useful when a
handler or middleware wants explicit request/response objects without adopting
the full context wrapper:

```gb
router.get(api, "/users/:id", func(dict<string, any> request): dict<string, any> {
    let req = wh.requestObject(request);
    return wh.responseObject(200, {"id": req.param("id")})
        .header("X-Route", "users.show")
        .toDict();
});

router.use(api, func(dict<string, any> request, dict<string, any> response): dict<string, any> {
    if (wh.statusCode(response) >= 500) {
        return wh.withHeader(response, "X-Error", "server");
    }
    return response;
});
```

## Request Validation

`web.validation` wraps the lower-level `schema` module for common API and form
handlers. Validation results include `valid`, `errors`, and parsed `data`.

```gb
import web.validation as validation;

let userRules = validation.object({
    "name": validation.stringField(),
    "roles": validation.arrayOf(validation.stringField())
}, ["name"]);

router.post(api, "/users", func(dict<string, any> request): dict<string, any> {
    let result = validation.json(request, userRules);
    if (!validation.isValid(result)) {
        return validation.errorResponse(result);
    }
    let data = validation.data(result);
    return wh.jsonCreated({"name": data["name"]});
});
```

Use `validation.form(request, rules)` for SSR form posts and
`validation.validate(data, rules)` when the input is already parsed.

## SSR Forms

`web.forms` builds on `web.validation`, `web.auth`, and `web.session` for
server-rendered form workflows:

```gb
import web.forms as forms;
import web.validation as validation;

let profileRules = validation.object({
    "name": validation.stringField()
}, ["name"]);

router.post(app, "/profile", func(dict<string, any> request): dict<string, any> {
    let result = forms.validate(request, profileRules);
    if (!forms.isValid(result)) {
        return wh.htmlStatus(forms.firstFieldError(result, "name"), 422);
    }
    return forms.redirectSuccess(sessionStore, request, "/profile", "Saved", {});
});
```

Use `forms.csrfField(token)` when rendering a hidden CSRF input,
`forms.withCsrf(response, secret, options)` to set the CSRF cookie, and
`forms.verifyCsrf(request, secret)` when handling the post.

## Common Middleware

`web.middleware` provides reusable response middleware for the common HTTP
concerns that most apps need early:

```gb
import web.middleware as middleware;

router.use(app, middleware.securityHeaders());
router.use(app, middleware.requestId());
router.use(app, middleware.cors("https://example.com", "GET, POST", "Content-Type, Authorization"));
```

`securityHeaders()` adds conservative browser security headers,
`requestId()` propagates or creates an `X-Request-ID` response header, and
`cors()`/`corsCredentials()` add CORS headers. `accessLog(logger)` logs method,
path, and status through the `log` module after a response is produced.

## Server-Sent Events

`web.sse` formats event-stream responses using the same response dictionary
shape as the rest of the web modules:

```gb
import web.sse as sse;

router.get(app, "/events", func(dict<string, any> request): dict<string, any> {
    return sse.response([
        sse.comment("ready"),
        sse.event("user.created", "{\"id\":42}", {"id": "42"}),
        sse.retry(5000)
    ]);
});
```

Use `sse.streaming(handler)` for long-lived event streams. The handler receives
an `sse.EventStream` and can write and flush SSE frames while the request stays
open:

```gb
router.get(app, "/live", func(dict<string, any> request): dict<string, any> {
    return sse.streaming(func(sse.EventStream stream): void {
        stream.write(sse.comment("connected"));
        stream.flush();
    });
});
```

## WebSockets

WebSocket routes use the same router and response dictionary contract as HTTP
routes. `web.websocket.upgrade(handler)` produces an upgrade response; the
handler runs after the HTTP connection has become a WebSocket:

```gb
import web.websocket as ws;

router.get(app, "/ws", func(dict<string, any> request): dict<string, any> {
    return ws.upgrade(func(ws.Connection conn): void {
        let message = conn.readJson();
        conn.sendJson({"echo": message["text"]});
        conn.close();
    });
});
```

The source wrapper also provides clients, so examples and integration tests can
exercise both sides without dropping to the lower-level native module:

```gb
let conn = ws.connect("ws://127.0.0.1:8080/ws");
conn.sendJson({"text": "hello"});
let reply = conn.readJson();
conn.close();
```

## Sessions, Auth, Cache

Server-side session stores are available for Redis, files, and SQL databases.
Cache stores follow a shared `get`, `set`, `delete`, `has` contract.

Auth helpers store the current user in the session and provide middleware
guards:

```gb
router.before(api, auth.requireAuth(sessionStore));
router.before(admin, auth.requireRole(sessionStore, "admin"));
router.before(editor, auth.requirePermission(sessionStore, "posts.edit"));
```

The middleware helpers return callable guard values, so they can be passed directly
to `router.before`, decorator policy maps, or a framework layer. Lower-level
helpers such as `auth.currentUser`, `auth.userHasRole`, and
`auth.userHasPermission` remain available when custom guard logic needs more
than a single role or permission check.

Use file sessions for local apps and small deployments, Redis sessions when
multiple app processes need shared state, and database sessions when the
application already depends on SQL persistence and operational simplicity is
more important than raw session throughput.

Flash messages and CSRF helpers are included for server-rendered forms:

```gb
let response = session.withFlash(sessionStore, wh.redirect("/settings"), request, "success", "Saved", {});
response = auth.withCsrf(response, secret, {});
```

## SSR

SSR uses regular string/template helpers rather than a custom Geblang template
language:

```gb
router.get(app, "/page", func(dict<string, any> request): dict<string, any> {
    let ctx = wh.context(request);
    return ctx.render("<h1>{{.title}}</h1>", {"title": "Geblang"});
});
```

For larger apps, keep templates as files and render them through the `template`
module or a thin application wrapper. Geblang deliberately avoids requiring a
custom template language for SSR.

## Decorator Direction

Decorator-driven routing and middleware let framework code register handlers
from metadata:

```gb
@route("GET", "/users/:id")
@loginRequired
func showUser(dict<string, any> request): dict<string, any> {
    ...
}
```

Framework code should scan decorator metadata with `reflect` and register
routes/middleware without new syntax.

The building blocks are:

- Decorators attach metadata to functions, methods, classes, and static methods.
- `reflect.decorators(value)` and `reflect.hasDecorator(value, name)` expose
  that metadata.
- `web.router.mount(router, controller)` registers plain route metadata.
- `web.router.mountWithOptions(router, controller, options)` can map decorator
  metadata to middleware and policy guards.
- Decorators such as `loginRequired`, `isGranted`, `requireRole`, or
  `requirePermission` can stay metadata-only and be interpreted by the router.

Example controller shape:

```gb
import web.http as wh;
import web.session as session;

let sessions = session.fileSessionStore("/tmp/app-sessions", 3600);

class UserController {
    @route("GET", "/users/:id")
    @loginRequired
    @isGranted("admin")
    func show(dict<string, any> request): dict<string, any> {
        let ctx = wh.context(request);
        return wh.jsonStatus({"id": ctx.param("id")}, 200);
    }
}

let app = router.newRouter();
let api = router.group(app, "/api");

router.mountWithOptions(api, UserController(), {
    "sessionStore": sessions
});
```

The goal is not to make decorators mandatory. Direct registration remains the
lowest-level API and the best fit for small scripts.

`mountWithOptions` currently understands:

- Route decorators: `@route("GET", "/path")`, plus verb aliases such as
  `@get("/path")`, `@post("/path")`, `@put`, `@patch`, `@delete`, and
  `@options` where supported by the parser/runtime path in use.
- Auth decorators with a `sessionStore` option: `@loginRequired`,
  `@requireAuth`, `@isGranted("role")`, `@requireRole("role")`, and
  `@requirePermission("permission")`.
- Named middleware decorators through the `middleware` option map.
- Class-level prefix decorators such as `@prefix("/api")` are represented in
  metadata, but grouping with `router.group` is the most explicit and portable
  option today.

For custom policy decorators with arguments, a framework layer can read
`reflect.decorators` directly and register the route however it wants. The
stdlib router keeps the built-in policy mapping intentionally small.

## Practical App Layout

An application can keep HTTP wiring thin and put behavior in modules:

```text
src/
  main.gb
  http/
    routes.gb
    controllers.gb
    middleware.gb
  domain/
    users.gb
  storage/
    users_repository.gb
```

`main.gb` should create shared services, create the router, mount routes, and
start `http.serve`. Controllers should translate HTTP requests to domain calls
and return response dictionaries.
