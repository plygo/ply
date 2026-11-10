// Package openapi builds an OpenAPI 3.0 document from Ply route metadata.
package openapi

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/plygo/ply"
	"github.com/plygo/ply/core"
	"github.com/plygo/ply/internal/router"
)

const Version = "3.0.3"

type Options struct {
	Info             Info
	Servers          []Server
	Components       *Components
	Tags             []Tag
	Security         []map[string][]string
	Operations       map[string]Operation
	IncludeWildcards bool
}

type Info struct {
	Title          string   `json:"title"`
	Version        string   `json:"version"`
	Description    string   `json:"description,omitempty"`
	TermsOfService string   `json:"termsOfService,omitempty"`
	Contact        *Contact `json:"contact,omitempty"`
	License        *License `json:"license,omitempty"`
}

type Contact struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

type License struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Document struct {
	OpenAPI    string                `json:"openapi"`
	Info       Info                  `json:"info"`
	Servers    []Server              `json:"servers,omitempty"`
	Tags       []Tag                 `json:"tags,omitempty"`
	Paths      map[string]PathItem   `json:"paths"`
	Components *Components           `json:"components,omitempty"`
	Security   []map[string][]string `json:"security,omitempty"`
}

type PathItem struct {
	Get        *Operation  `json:"get,omitempty"`
	Put        *Operation  `json:"put,omitempty"`
	Post       *Operation  `json:"post,omitempty"`
	Delete     *Operation  `json:"delete,omitempty"`
	Options    *Operation  `json:"options,omitempty"`
	Head       *Operation  `json:"head,omitempty"`
	Patch      *Operation  `json:"patch,omitempty"`
	Trace      *Operation  `json:"trace,omitempty"`
	Parameters []Parameter `json:"parameters,omitempty"`
}

type Operation struct {
	Tags        []string              `json:"tags,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	OperationID string                `json:"operationId,omitempty"`
	Parameters  []Parameter           `json:"parameters,omitempty"`
	RequestBody *RequestBody          `json:"requestBody,omitempty"`
	Responses   map[string]Response   `json:"responses"`
	Deprecated  bool                  `json:"deprecated,omitempty"`
	Security    []map[string][]string `json:"security,omitempty"`
}

type Parameter struct {
	Name        string  `json:"name"`
	In          string  `json:"in"`
	Description string  `json:"description,omitempty"`
	Required    bool    `json:"required,omitempty"`
	Deprecated  bool    `json:"deprecated,omitempty"`
	Schema      *Schema `json:"schema,omitempty"`
	Style       string  `json:"style,omitempty"`
	Explode     *bool   `json:"explode,omitempty"`
}

type RequestBody struct {
	Description string               `json:"description,omitempty"`
	Required    bool                 `json:"required,omitempty"`
	Content     map[string]MediaType `json:"content"`
}

type MediaType struct {
	Schema  *Schema `json:"schema,omitempty"`
	Example any     `json:"example,omitempty"`
}

type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
	Headers     map[string]Header    `json:"headers,omitempty"`
}

type Header struct {
	Description string  `json:"description,omitempty"`
	Schema      *Schema `json:"schema,omitempty"`
}

type Components struct {
	Schemas         map[string]*Schema        `json:"schemas,omitempty"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
}

type SecurityScheme struct {
	Type             string      `json:"type"`
	Description      string      `json:"description,omitempty"`
	Name             string      `json:"name,omitempty"`
	In               string      `json:"in,omitempty"`
	Scheme           string      `json:"scheme,omitempty"`
	BearerFormat     string      `json:"bearerFormat,omitempty"`
	OpenIDConnectURL string      `json:"openIdConnectUrl,omitempty"`
	Flows            *OAuthFlows `json:"flows,omitempty"`
}

type OAuthFlows struct {
	Implicit          *OAuthFlow `json:"implicit,omitempty"`
	Password          *OAuthFlow `json:"password,omitempty"`
	ClientCredentials *OAuthFlow `json:"clientCredentials,omitempty"`
	AuthorizationCode *OAuthFlow `json:"authorizationCode,omitempty"`
}

type OAuthFlow struct {
	AuthorizationURL string            `json:"authorizationUrl,omitempty"`
	TokenURL         string            `json:"tokenUrl,omitempty"`
	RefreshURL       string            `json:"refreshUrl,omitempty"`
	Scopes           map[string]string `json:"scopes"`
}

type Schema struct {
	Ref                  string             `json:"$ref,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Title                string             `json:"title,omitempty"`
	Description          string             `json:"description,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	Default              any                `json:"default,omitempty"`
	Example              any                `json:"example,omitempty"`
	Nullable             bool               `json:"nullable,omitempty"`
	AdditionalProperties any                `json:"additionalProperties,omitempty"`
	AllOf                []*Schema          `json:"allOf,omitempty"`
	OneOf                []*Schema          `json:"oneOf,omitempty"`
	AnyOf                []*Schema          `json:"anyOf,omitempty"`
}

type Builder struct {
	routes []*router.Route
	opts   Options
}

func New(app *ply.App, opts ...Options) *Builder {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	return &Builder{routes: app.Routes(), opts: o}
}

func (b *Builder) Document() *Document {
	info := b.opts.Info
	if info.Title == "" {
		info.Title = "Ply API"
	}
	if info.Version == "" {
		info.Version = "1.0.0"
	}

	doc := &Document{
		OpenAPI:    Version,
		Info:       info,
		Servers:    b.opts.Servers,
		Tags:       b.opts.Tags,
		Security:   b.opts.Security,
		Components: b.opts.Components,
		Paths:      make(map[string]PathItem),
	}

	for _, route := range b.routes {
		if route == nil || route.Hidden {
			continue
		}
		path, params, ok := ConvertPath(route.Path)
		if !ok {
			if !b.opts.IncludeWildcards {
				continue
			}
			path, params = wildcardPath(route.Path)
		}

		for _, method := range routeMethods(route) {
			op := b.operation(method, route, params)
			item := doc.Paths[path]
			item.set(method, op)
			doc.Paths[path] = item
		}
	}

	return doc
}

func (b *Builder) JSON() ([]byte, error) {
	return json.Marshal(b.Document())
}

func (b *Builder) Handler() core.Handler {
	data, err := b.JSON()
	if err != nil {
		return func(c *core.Context) {
			c.Text(500, "failed to generate openapi document")
		}
	}

	return func(c *core.Context) {
		c.Ctx.Response.Header.SetContentType("application/json")
		c.Ctx.SetBody(data)
	}
}

func Handler(doc *Document) core.Handler {
	data, err := json.Marshal(doc)
	if err != nil {
		return func(c *core.Context) {
			c.Text(500, "failed to generate openapi document")
		}
	}
	return func(c *core.Context) {
		c.Ctx.Response.Header.SetContentType("application/json")
		c.Ctx.SetBody(data)
	}
}

func (b *Builder) operation(method string, route *router.Route, params []Parameter) *Operation {
	op := Operation{
		Summary:     first(route.Summary, route.Name),
		Description: route.Description,
		Tags:        append([]string(nil), route.Tags...),
		OperationID: first(route.OperationID, operationID(method, route.Path)),
		Deprecated:  route.Deprecated,
		Parameters:  params,
		Responses: map[string]Response{
			"200": {Description: "OK"},
		},
	}

	overrides := []string{method + " " + route.Path}
	if canonical, _, ok := ConvertPath(route.Path); ok {
		overrides = append(overrides, method+" "+canonical)
	}
	for _, key := range overrides {
		override, ok := b.opts.Operations[key]
		if !ok {
			continue
		}
		merge(&op, override)
		break
	}
	return &op
}

func merge(dst *Operation, src Operation) {
	if len(src.Tags) > 0 {
		dst.Tags = src.Tags
	}
	if src.Summary != "" {
		dst.Summary = src.Summary
	}
	if src.Description != "" {
		dst.Description = src.Description
	}
	if src.OperationID != "" {
		dst.OperationID = src.OperationID
	}
	if len(src.Parameters) > 0 {
		dst.Parameters = src.Parameters
	}
	if src.RequestBody != nil {
		dst.RequestBody = src.RequestBody
	}
	if len(src.Responses) > 0 {
		dst.Responses = src.Responses
	}
	if src.Deprecated {
		dst.Deprecated = true
	}
	if len(src.Security) > 0 {
		dst.Security = src.Security
	}
}

func (p *PathItem) set(method string, op *Operation) {
	switch strings.ToUpper(method) {
	case "GET":
		p.Get = op
	case "PUT":
		p.Put = op
	case "POST":
		p.Post = op
	case "DELETE":
		p.Delete = op
	case "OPTIONS":
		p.Options = op
	case "HEAD":
		p.Head = op
	case "PATCH":
		p.Patch = op
	case "TRACE":
		p.Trace = op
	}
}

func routeMethods(route *router.Route) []string {
	methods := route.Methods
	if len(methods) == 0 && route.Method != "" {
		methods = []string{route.Method}
	}

	seen := make(map[string]struct{}, len(methods))
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		method = strings.ToUpper(method)
		if method == "" || method == "CONNECT" {
			continue
		}
		if _, ok := seen[method]; ok {
			continue
		}
		seen[method] = struct{}{}
		out = append(out, method)
	}
	return out
}

// ConvertPath turns Ply path parameters into OpenAPI parameters.
func ConvertPath(path string) (string, []Parameter, bool) {
	if strings.Contains(path, "*") {
		return "", nil, false
	}
	if path == "" {
		path = "/"
	}

	segments := strings.Split(path, "/")
	params := make([]Parameter, 0, 2)
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			name := strings.TrimPrefix(segment, ":")
			if name == "" {
				return "", nil, false
			}
			segments[i] = "{" + name + "}"
			params = append(params, Parameter{
				Name:     name,
				In:       "path",
				Required: true,
				Schema:   &Schema{Type: "string"},
			})
		}
	}
	return strings.Join(segments, "/"), params, true
}

func wildcardPath(path string) (string, []Parameter) {
	path = strings.TrimSuffix(path, "*")
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	if path != "/" {
		path += "/{path}"
	}
	return path, []Parameter{{
		Name:     "path",
		In:       "path",
		Required: true,
		Schema:   &Schema{Type: "string"},
	}}
}

func operationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, ":") {
			b.WriteString("By")
			b.WriteString(upperCamel(strings.TrimPrefix(segment, ":")))
			continue
		}
		b.WriteString(upperCamel(segment))
	}
	return b.String()
}

func upperCamel(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upper = true
			continue
		}
		if upper {
			b.WriteRune(unicode.ToUpper(r))
			upper = false
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "Root"
	}
	return b.String()
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
