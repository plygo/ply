// Package grpcgateway exposes gRPC-style method paths as JSON HTTP endpoints.
package grpcgateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/plygo/ply"
	"github.com/plygo/ply/core"
	"github.com/plygo/ply/internal/router"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var ErrUnknownMethod = errors.New("grpcgateway: unknown method")

type requestContextKey struct{}

func RequestContext(ctx context.Context) *core.Context {
	c, _ := ctx.Value(requestContextKey{}).(*core.Context)
	return c
}

type Resolver func(fullMethod string) (request, response proto.Message, err error)

type Invoker func(ctx context.Context, fullMethod string, request, response proto.Message) error

type Options struct {
	Resolver     Resolver
	Invoker      Invoker
	MaxBodyBytes int64
	Marshal      protojson.MarshalOptions
	Unmarshal    protojson.UnmarshalOptions
	StatusMapper func(error) (int, string)
}

type Gateway struct {
	resolver     Resolver
	invoker      Invoker
	maxBodyBytes int64
	marshal      protojson.MarshalOptions
	unmarshal    protojson.UnmarshalOptions
	statusMapper func(error) (int, string)
}

type Error struct {
	Status  int
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return "grpcgateway: error"
	}
	if e.Err != nil {
		if e.Message != "" {
			return fmt.Sprintf("grpcgateway: %s: %v", e.Message, e.Err)
		}
		return "grpcgateway: " + e.Err.Error()
	}
	if e.Message != "" {
		return "grpcgateway: " + e.Message
	}
	if e.Status != 0 {
		return fmt.Sprintf("grpcgateway: %s", http.StatusText(e.Status))
	}
	return "grpcgateway: error"
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func New(opts Options) (*Gateway, error) {
	if opts.Resolver == nil {
		return nil, errors.New("grpcgateway: resolver is required")
	}
	if opts.Invoker == nil {
		return nil, errors.New("grpcgateway: invoker is required")
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 4 << 20
	}
	return &Gateway{
		resolver:     opts.Resolver,
		invoker:      opts.Invoker,
		maxBodyBytes: opts.MaxBodyBytes,
		marshal:      opts.Marshal,
		unmarshal:    opts.Unmarshal,
		statusMapper: opts.StatusMapper,
	}, nil
}

func (g *Gateway) Handler(prefix ...string) core.Handler {
	p := ""
	if len(prefix) > 0 {
		p = cleanPrefix(prefix[0])
	}

	return func(c *core.Context) {
		if g == nil || g.resolver == nil || g.invoker == nil {
			writeError(c, http.StatusInternalServerError, "internal server error")
			return
		}

		fullMethod, err := g.method(c, p)
		if err != nil {
			writeError(c, http.StatusNotFound, "method not found")
			return
		}
		if !validContentType(c.Header("Content-Type")) {
			writeError(c, http.StatusUnsupportedMediaType, "content type must be application/json")
			return
		}

		request, response, err := g.resolver(fullMethod)
		if err != nil {
			if errors.Is(err, ErrUnknownMethod) {
				writeError(c, http.StatusNotFound, "method not found")
				return
			}
			g.writeMappedError(c, err)
			return
		}
		if request == nil || response == nil {
			g.writeMappedError(c, errors.New("grpcgateway: resolver returned a nil message"))
			return
		}

		body := c.Ctx.PostBody()
		if int64(len(body)) > g.maxBodyBytes {
			writeError(c, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		if len(bytes.TrimSpace(body)) == 0 {
			body = []byte("{}")
		}
		if err := g.unmarshal.Unmarshal(body, request); err != nil {
			writeError(c, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}

		ctx := context.WithValue(c.Ctx, requestContextKey{}, c)
		if err := g.invoker(ctx, fullMethod, request, response); err != nil {
			g.writeMappedError(c, err)
			return
		}

		payload, err := g.marshal.Marshal(response)
		if err != nil {
			g.writeMappedError(c, err)
			return
		}
		c.Ctx.SetStatusCode(http.StatusOK)
		c.Ctx.SetContentType("application/json")
		c.Ctx.SetBody(payload)
	}
}

func (g *Gateway) Mount(app *ply.App, prefix string) *router.Route {
	if app == nil {
		panic("grpcgateway: app cannot be nil")
	}
	p := cleanPrefix(prefix)
	path := p + "/*"
	if p == "" {
		path = "/*"
	}
	route := app.Post(path, g.Handler(p))
	route.Hidden = true
	return route
}

func (g *Gateway) method(c *core.Context, prefix string) (string, error) {
	path := string(c.Ctx.Path())
	if prefix != "" {
		if path == prefix {
			path = "/"
		} else if strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
		} else {
			return "", errors.New("grpcgateway: path does not match prefix")
		}
	}

	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("grpcgateway: invalid method path")
	}
	return "/" + parts[0] + "/" + parts[1], nil
}

func (g *Gateway) writeMappedError(c *core.Context, err error) {
	status, message := g.mapError(err)
	writeError(c, status, message)
}

func (g *Gateway) mapError(err error) (int, string) {
	if g.statusMapper != nil {
		status, message := g.statusMapper(err)
		if status >= 400 && status <= 599 {
			return status, defaultMessage(status, message)
		}
	}

	var gatewayErr *Error
	if errors.As(err, &gatewayErr) {
		status := gatewayErr.Status
		if status < 400 || status > 599 {
			status = http.StatusInternalServerError
		}
		return status, defaultMessage(status, gatewayErr.Message)
	}
	return http.StatusInternalServerError, "internal server error"
}

func defaultMessage(status int, message string) string {
	if message != "" {
		return message
	}
	if status >= 500 {
		return "internal server error"
	}
	return http.StatusText(status)
}

func writeError(c *core.Context, status int, message string) {
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	c.JSON(status, map[string]any{"code": status, "message": message})
}

func validContentType(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func cleanPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return ""
	}
	return "/" + strings.Trim(prefix, "/")
}
