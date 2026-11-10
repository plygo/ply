package openapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plygo/ply"
	"github.com/plygo/ply/core"
)

func TestDocument_FromRoutes(t *testing.T) {
	app := ply.New()
	app.Get("/users/:id", func(c *core.Context) {})
	getRoute := app.Route("/users/:id")
	getRoute.Summary = "Get user"
	getRoute.Description = "Returns one user."
	getRoute.Tags = []string{"users"}
	getRoute.OperationID = "getUser"

	app.Post("/users", func(c *core.Context) {})
	app.Get("/hidden", func(c *core.Context) {}).Hidden = true
	app.Get("/files/*", func(c *core.Context) {})

	doc := New(app, Options{
		Info:    Info{Title: "Test API", Version: "2.0.0"},
		Servers: []Server{{URL: "https://api.example.com"}},
		Operations: map[string]Operation{
			"POST /users": {
				Summary: "Create user",
				RequestBody: &RequestBody{
					Required: true,
					Content: map[string]MediaType{
						"application/json": {Schema: &Schema{Ref: "#/components/schemas/User"}},
					},
				},
				Responses: map[string]Response{
					"201": {Description: "Created"},
				},
			},
		},
		Components: &Components{
			Schemas: map[string]*Schema{
				"User": {Type: "object", Properties: map[string]*Schema{"id": {Type: "string"}}},
			},
		},
	}).Document()

	if doc.OpenAPI != Version {
		t.Fatalf("version = %q", doc.OpenAPI)
	}
	if doc.Info.Title != "Test API" {
		t.Fatalf("title = %q", doc.Info.Title)
	}

	item, ok := doc.Paths["/users/{id}"]
	if !ok {
		t.Fatalf("missing converted path: %+v", doc.Paths)
	}
	if item.Get == nil {
		t.Fatal("missing GET operation")
	}
	if item.Get.Summary != "Get user" || item.Get.OperationID != "getUser" {
		t.Fatalf("operation = %+v", item.Get)
	}
	if len(item.Get.Parameters) != 1 || item.Get.Parameters[0].Name != "id" || item.Get.Parameters[0].In != "path" {
		t.Fatalf("parameters = %+v", item.Get.Parameters)
	}

	post := doc.Paths["/users"].Post
	if post == nil || post.Summary != "Create user" {
		t.Fatalf("post = %+v", post)
	}
	if post.RequestBody == nil || !post.RequestBody.Required {
		t.Fatalf("request body = %+v", post.RequestBody)
	}
	if _, ok := post.Responses["201"]; !ok {
		t.Fatalf("responses = %+v", post.Responses)
	}

	if _, ok := doc.Paths["/hidden"]; ok {
		t.Fatal("hidden route should be skipped")
	}
	if _, ok := doc.Paths["/files/{path}"]; ok {
		t.Fatal("wildcard route should be skipped by default")
	}
}

func TestDocument_AllMethodsAndWildcard(t *testing.T) {
	app := ply.New()
	app.All("/items", func(c *core.Context) {})
	app.Get("/files/*", func(c *core.Context) {})

	doc := New(app, Options{IncludeWildcards: true}).Document()
	item := doc.Paths["/items"]
	if item.Get == nil || item.Post == nil || item.Put == nil || item.Delete == nil || item.Patch == nil || item.Options == nil || item.Head == nil {
		t.Fatalf("missing method in %+v", item)
	}
	if _, ok := doc.Paths["/files/{path}"]; !ok {
		t.Fatalf("wildcard path missing: %+v", doc.Paths)
	}
}

func TestHandler(t *testing.T) {
	app := ply.New()
	app.Get("/openapi.json", New(app, Options{Info: Info{Title: "API"}}).Handler())

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}

	var doc Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Info.Title != "API" {
		t.Fatalf("title = %q", doc.Info.Title)
	}
}

func TestConvertPath(t *testing.T) {
	path, params, ok := ConvertPath("/users/:id/posts/:postID")
	if !ok {
		t.Fatal("expected valid path")
	}
	if path != "/users/{id}/posts/{postID}" {
		t.Fatalf("path = %q", path)
	}
	if len(params) != 2 || params[1].Name != "postID" {
		t.Fatalf("params = %+v", params)
	}

	if _, _, ok := ConvertPath("/files/*"); ok {
		t.Fatal("wildcard should not be a regular path")
	}
}
