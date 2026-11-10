package websocket_test

import (
	"net"
	"testing"
	"time"

	wsclient "github.com/fasthttp/websocket"
	"github.com/plygo/ply"
	"github.com/plygo/ply/websocket"
	"github.com/valyala/fasthttp"
)

func startServer(t *testing.T, app *ply.App) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{Handler: app.Handler()}
	go func() {
		_ = srv.Serve(ln)
	}()
	return "ws://" + ln.Addr().String(), func() {
		_ = srv.Shutdown()
	}
}

func TestHandler_Echo(t *testing.T) {
	app := ply.New()
	app.Get("/ws", websocket.New(websocket.Options{
		PingInterval: time.Hour,
	}).Handler(func(c *websocket.Client) {
		for {
			messageType, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(messageType, msg); err != nil {
				return
			}
		}
	}))

	url, stop := startServer(t, app)
	defer stop()

	conn, _, err := wsclient.DefaultDialer.Dial(url+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	messageType, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.TextMessage || string(msg) != "hello" {
		t.Fatalf("message = %d %q", messageType, msg)
	}
}

func TestHandler_JSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	app := ply.New()
	app.Get("/ws", websocket.New(websocket.Options{
		PingInterval: time.Hour,
	}).Handler(func(c *websocket.Client) {
		var in payload
		if err := c.ReadJSON(&in); err != nil {
			return
		}
		_ = c.WriteJSON(payload{Name: "hello " + in.Name})
	}))

	url, stop := startServer(t, app)
	defer stop()

	conn, _, err := wsclient.DefaultDialer.Dial(url+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(payload{Name: "world"}); err != nil {
		t.Fatal(err)
	}
	var out payload
	if err := conn.ReadJSON(&out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "hello world" {
		t.Fatalf("name = %q", out.Name)
	}
}

func TestHandler_RejectsForeignOrigin(t *testing.T) {
	app := ply.New()
	app.Get("/ws", websocket.New(websocket.Options{
		PingInterval: time.Hour,
	}).Handler(func(c *websocket.Client) {
		_ = c.WriteText("unexpected")
	}))

	url, stop := startServer(t, app)
	defer stop()

	header := httpHeader("Origin", "https://evil.example")
	conn, _, err := wsclient.DefaultDialer.Dial(url+"/ws", header)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected origin rejection")
	}
}

func httpHeader(key, value string) map[string][]string {
	return map[string][]string{key: {value}}
}

func TestHandler_RejectsNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	websocket.New().Handler(nil)
}

func TestHandler_WriteText(t *testing.T) {
	app := ply.New()
	app.Get("/ws", websocket.New(websocket.Options{PingInterval: time.Hour}).Handler(func(c *websocket.Client) {
		_ = c.WriteText("ok")
	}))

	url, stop := startServer(t, app)
	defer stop()

	conn, _, err := wsclient.DefaultDialer.Dial(url+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "ok" {
		t.Fatalf("message = %q, err = %v", msg, err)
	}
}
