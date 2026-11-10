package grpcgateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plygo/ply"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func testGateway(t *testing.T, opts ...Options) *Gateway {
	t.Helper()
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Resolver == nil {
		o.Resolver = func(method string) (proto.Message, proto.Message, error) {
			if method != "/test.Echo/Unary" {
				return nil, nil, ErrUnknownMethod
			}
			return &structpb.Struct{}, &structpb.Struct{}, nil
		}
	}
	if o.Invoker == nil {
		o.Invoker = func(ctx context.Context, method string, request, response proto.Message) error {
			in := request.(*structpb.Struct)
			out := response.(*structpb.Struct)
			out.Fields = map[string]*structpb.Value{
				"greeting": structpb.NewStringValue("hello " + in.Fields["name"].GetStringValue()),
			}
			return nil
		}
	}
	gateway, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func TestGateway_Unary(t *testing.T) {
	app := ply.New()
	route := testGateway(t).Mount(app, "/grpc")
	if !route.Hidden {
		t.Fatal("gateway route should be hidden from OpenAPI")
	}

	req := httptest.NewRequest(http.MethodPost, "/grpc/test.Echo/Unary", strings.NewReader(`{"name":"ply"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `"greeting"`) || !strings.Contains(body, "hello ply") {
		t.Fatalf("body = %s", body)
	}
}

func TestGateway_RequestContext(t *testing.T) {
	seen := false
	gateway := testGateway(t, Options{
		Invoker: func(ctx context.Context, method string, request, response proto.Message) error {
			c := RequestContext(ctx)
			seen = c != nil && c.Header("X-Test") == "yes"
			return nil
		},
	})
	app := ply.New()
	gateway.Mount(app, "/grpc")

	req := httptest.NewRequest(http.MethodPost, "/grpc/test.Echo/Unary", strings.NewReader(`{}`))
	req.Header.Set("X-Test", "yes")
	resp := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !seen {
		t.Fatal("request context was not available to the invoker")
	}
}

func TestGateway_HandlerWithoutMount(t *testing.T) {
	app := ply.New()
	app.Post("/test.Echo/*", testGateway(t).Handler())

	req := httptest.NewRequest(http.MethodPost, "/test.Echo/Unary", strings.NewReader(`{"name":"ply"}`))
	resp := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestGateway_Errors(t *testing.T) {
	app := ply.New()
	gateway := testGateway(t, Options{
		MaxBodyBytes: 8,
		StatusMapper: func(err error) (int, string) {
			if errors.Is(err, errDenied) {
				return http.StatusForbidden, "denied"
			}
			return 0, ""
		},
	})
	gateway.Mount(app, "/grpc")

	tests := []struct {
		name   string
		body   string
		ctype  string
		path   string
		status int
	}{
		{name: "unknown method", path: "/grpc/test.Echo/Missing", status: http.StatusNotFound},
		{name: "invalid path", path: "/grpc/test.Echo", status: http.StatusNotFound},
		{name: "invalid json", path: "/grpc/test.Echo/Unary", body: `{`, status: http.StatusBadRequest},
		{name: "wrong content type", path: "/grpc/test.Echo/Unary", ctype: "text/plain", status: http.StatusUnsupportedMediaType},
		{name: "too large", path: "/grpc/test.Echo/Unary", body: `{"name":"this is too large"}`, status: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			if tt.ctype != "" {
				req.Header.Set("Content-Type", tt.ctype)
			}
			resp := app.Test(req)
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d, body = %s", resp.StatusCode, tt.status, readBody(t, resp))
			}
		})
	}
}

func TestGateway_ErrorDetails(t *testing.T) {
	gateway := testGateway(t, Options{
		Invoker: func(ctx context.Context, method string, request, response proto.Message) error {
			return &Error{Status: http.StatusNotFound, Message: "missing user"}
		},
	})
	app := ply.New()
	gateway.Mount(app, "/grpc")

	resp := app.Test(httptest.NewRequest(http.MethodPost, "/grpc/test.Echo/Unary", strings.NewReader(`{}`)))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "missing user") {
		t.Fatalf("body = %s", body)
	}
}

func TestGateway_StatusMapper(t *testing.T) {
	gateway := testGateway(t, Options{
		Invoker: func(ctx context.Context, method string, request, response proto.Message) error {
			return errDenied
		},
		StatusMapper: func(err error) (int, string) {
			if errors.Is(err, errDenied) {
				return http.StatusForbidden, "denied"
			}
			return 0, ""
		},
	})
	app := ply.New()
	gateway.Mount(app, "/grpc")

	resp := app.Test(httptest.NewRequest(http.MethodPost, "/grpc/test.Echo/Unary", strings.NewReader(`{}`)))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestGateway_EmptyBody(t *testing.T) {
	app := ply.New()
	testGateway(t).Mount(app, "/grpc")

	resp := app.Test(httptest.NewRequest(http.MethodPost, "/grpc/test.Echo/Unary", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestNew_Validation(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected resolver error")
	}
	if _, err := New(Options{Resolver: func(string) (proto.Message, proto.Message, error) { return nil, nil, nil }}); err == nil {
		t.Fatal("expected invoker error")
	}
}

var errDenied = errors.New("denied")

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var buf [4096]byte
	n, _ := resp.Body.Read(buf[:])
	return string(buf[:n])
}
