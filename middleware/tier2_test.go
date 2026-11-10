package middleware

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/plygo/ply/core"
	"github.com/valyala/fasthttp"
)

func TestSecurityHeaders_Defaults(t *testing.T) {
	var fctx fasthttp.RequestCtx
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{SecurityHeaders(), func(c *core.Context) {}})
	c.Next()

	if got := string(fctx.Response.Header.Peek("X-Content-Type-Options")); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := string(fctx.Response.Header.Peek("X-Frame-Options")); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := string(fctx.Response.Header.Peek("Referrer-Policy")); got == "" {
		t.Fatal("missing Referrer-Policy")
	}
	if got := string(fctx.Response.Header.Peek("Strict-Transport-Security")); got != "" {
		t.Fatalf("unexpected HSTS header %q", got)
	}
}

func TestSecurityHeaders_HSTSAndCSP(t *testing.T) {
	var fctx fasthttp.RequestCtx
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{
		SecurityHeaders(SecurityHeadersOptions{
			HSTSMaxAge:            time.Hour,
			HSTSIncludeSubdomains: true,
			HSTSPreload:           true,
			ContentSecurityPolicy: "default-src 'self'",
		}),
		func(c *core.Context) {},
	})
	c.Next()

	if got := string(fctx.Response.Header.Peek("Strict-Transport-Security")); got != "max-age=3600; includeSubDomains; preload" {
		t.Fatalf("HSTS = %q", got)
	}
	if got := string(fctx.Response.Header.Peek("Content-Security-Policy")); got != "default-src 'self'" {
		t.Fatalf("CSP = %q", got)
	}
}

func TestBodyLimit_RejectsBody(t *testing.T) {
	var fctx fasthttp.RequestCtx
	fctx.Request.SetBodyString("12345")
	c := core.NewContext(&fctx, nil, nil)
	called := false
	c.SetHandlers([]core.Handler{BodyLimit(4), func(c *core.Context) { called = true }})
	c.Next()

	if called {
		t.Fatal("handler should not run")
	}
	if fctx.Response.StatusCode() != fasthttp.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", fctx.Response.StatusCode())
	}
	if !c.Aborted() {
		t.Fatal("context should be aborted")
	}
}

func TestBodyLimit_RejectsContentLength(t *testing.T) {
	var fctx fasthttp.RequestCtx
	fctx.Request.Header.SetContentLength(10)
	c := core.NewContext(&fctx, nil, nil)
	called := false
	c.SetHandlers([]core.Handler{BodyLimit(4), func(c *core.Context) { called = true }})
	c.Next()

	if called {
		t.Fatal("handler should not run")
	}
	if fctx.Response.StatusCode() != fasthttp.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", fctx.Response.StatusCode())
	}
}

func TestBodyLimit_AllowsSmallBody(t *testing.T) {
	var fctx fasthttp.RequestCtx
	fctx.Request.SetBodyString("ok")
	c := core.NewContext(&fctx, nil, nil)
	called := false
	c.SetHandlers([]core.Handler{BodyLimit(4), func(c *core.Context) { called = true }})
	c.Next()

	if !called {
		t.Fatal("handler should run")
	}
}

func TestHealth(t *testing.T) {
	var fctx fasthttp.RequestCtx
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{Health()})
	c.Next()

	if fctx.Response.StatusCode() != 200 {
		t.Fatalf("status = %d", fctx.Response.StatusCode())
	}
	if !strings.Contains(string(fctx.Response.Body()), `"status":"ok"`) {
		t.Fatalf("body = %s", fctx.Response.Body())
	}
}

func TestReadiness(t *testing.T) {
	var fctx fasthttp.RequestCtx
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{Readiness(func() error { return nil })})
	c.Next()
	if fctx.Response.StatusCode() != 200 {
		t.Fatalf("status = %d", fctx.Response.StatusCode())
	}

	var failed fasthttp.RequestCtx
	c2 := core.NewContext(&failed, nil, nil)
	c2.SetHandlers([]core.Handler{Readiness(func() error { return errReadiness })})
	c2.Next()
	if failed.Response.StatusCode() != 503 {
		t.Fatalf("status = %d", failed.Response.StatusCode())
	}
}

var errReadiness = errors.New("not ready")

func TestSlogLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	var fctx fasthttp.RequestCtx
	fctx.Request.Header.SetMethod("GET")
	fctx.Request.SetRequestURI("/hello")
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{
		RequestID(),
		SlogLogger(logger),
		func(c *core.Context) { c.Text(201, "created") },
	})
	c.Next()

	out := buf.String()
	for _, want := range []string{"method=GET", "path=/hello", "status=201", "duration=", "request_id="} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q missing %q", out, want)
		}
	}
}

func TestSlogLogger_SkipPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	var fctx fasthttp.RequestCtx
	fctx.Request.SetRequestURI("/health")
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{SlogLogger(logger, SlogLoggerOptions{SkipPaths: []string{"/health"}})})
	c.Next()

	if buf.Len() != 0 {
		t.Fatalf("expected no log, got %q", buf.String())
	}
}

func TestETag_NotModified(t *testing.T) {
	var fctx fasthttp.RequestCtx
	fctx.Request.SetRequestURI("/")
	fctx.Request.Header.SetMethod("GET")
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{ETag(), func(c *core.Context) { c.Text(200, "hello") }})
	c.Next()

	tag := string(fctx.Response.Header.Peek("ETag"))
	if tag == "" {
		t.Fatal("missing ETag")
	}

	var fctx2 fasthttp.RequestCtx
	fctx2.Request.SetRequestURI("/")
	fctx2.Request.Header.SetMethod("GET")
	fctx2.Request.Header.Set("If-None-Match", tag)
	c2 := core.NewContext(&fctx2, nil, nil)
	c2.SetHandlers([]core.Handler{ETag(), func(c *core.Context) { c.Text(200, "hello") }})
	c2.Next()

	if fctx2.Response.StatusCode() != 304 {
		t.Fatalf("status = %d", fctx2.Response.StatusCode())
	}
	if len(fctx2.Response.Body()) != 0 {
		t.Fatalf("expected empty body, got %q", fctx2.Response.Body())
	}
}

func TestETag_WeakMatch(t *testing.T) {
	var fctx fasthttp.RequestCtx
	fctx.Request.SetRequestURI("/")
	fctx.Request.Header.SetMethod("GET")
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{ETag(), func(c *core.Context) { c.Text(200, "hello") }})
	c.Next()

	tag := string(fctx.Response.Header.Peek("ETag"))

	var fctx2 fasthttp.RequestCtx
	fctx2.Request.SetRequestURI("/")
	fctx2.Request.Header.SetMethod("GET")
	fctx2.Request.Header.Set("If-None-Match", "W/"+tag)
	c2 := core.NewContext(&fctx2, nil, nil)
	c2.SetHandlers([]core.Handler{ETag(), func(c *core.Context) { c.Text(200, "hello") }})
	c2.Next()

	if fctx2.Response.StatusCode() != 304 {
		t.Fatalf("status = %d", fctx2.Response.StatusCode())
	}
}

func TestCacheControl(t *testing.T) {
	var fctx fasthttp.RequestCtx
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{CacheControl("public, max-age=60")})
	c.Next()

	if got := string(fctx.Response.Header.Peek("Cache-Control")); got != "public, max-age=60" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestMetrics(t *testing.T) {
	m := NewMetrics()

	var fctx fasthttp.RequestCtx
	fctx.Request.Header.SetMethod("GET")
	fctx.Request.SetRequestURI("/")
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{m.Middleware(), func(c *core.Context) { c.Text(200, "ok") }})
	c.Next()

	var metricsCtx fasthttp.RequestCtx
	mc := core.NewContext(&metricsCtx, nil, nil)
	mc.SetHandlers([]core.Handler{m.Handler()})
	mc.Next()

	body := string(metricsCtx.Response.Body())
	for _, want := range []string{
		`ply_http_requests_total{method="GET",status="200"} 1`,
		`ply_http_request_duration_seconds_count{method="GET",status="200"} 1`,
		`ply_http_request_duration_seconds_bucket{method="GET",status="200",le="+Inf"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}
