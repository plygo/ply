package session

import (
	"strings"
	"testing"
	"time"

	"github.com/plygo/ply/core"
	"github.com/valyala/fasthttp"
)

func runRequest(manager *Manager, cookie string, handler core.Handler) (*fasthttp.RequestCtx, *core.Context) {
	var fctx fasthttp.RequestCtx
	if cookie != "" {
		fctx.Request.Header.SetCookie(manager.CookieName, cookie)
	}
	c := core.NewContext(&fctx, nil, nil)
	c.SetHandlers([]core.Handler{manager.Middleware(), handler})
	c.Next()
	return &fctx, c
}

func sessionID(ctx *fasthttp.RequestCtx) string {
	raw := string(ctx.Response.Header.Peek("Set-Cookie"))
	prefix := "ply_session="
	if len(raw) < len(prefix) || raw[:len(prefix)] != prefix {
		return ""
	}
	value := raw[len(prefix):]
	for i, b := range value {
		if b == ';' {
			return value[:i]
		}
	}
	return value
}

func TestManager_NewAndLoad(t *testing.T) {
	manager := NewManager(NewMemoryStore())
	manager.Secure = false

	resp, _ := runRequest(manager, "", func(c *core.Context) {
		manager.Set(c, "user", "ada")
	})
	id := sessionID(resp)
	if id == "" {
		t.Fatal("missing session cookie")
	}

	_, _ = runRequest(manager, id, func(c *core.Context) {
		if got := manager.Get(c, "user"); got != "ada" {
			t.Fatalf("user = %v", got)
		}
	})
}

func TestManager_Destroy(t *testing.T) {
	manager := NewManager(NewMemoryStore())
	manager.Secure = false

	resp, _ := runRequest(manager, "", func(c *core.Context) {
		manager.Set(c, "user", "ada")
	})
	id := sessionID(resp)

	resp2, _ := runRequest(manager, id, func(c *core.Context) {
		manager.Destroy(c)
	})
	if got := sessionID(resp2); got != "" {
		t.Fatalf("expected empty cookie, got %q", got)
	}

	_, _ = runRequest(manager, id, func(c *core.Context) {
		if got := manager.Get(c, "user"); got != nil {
			t.Fatalf("user = %v after destroy", got)
		}
	})
}

func TestManager_Renew(t *testing.T) {
	manager := NewManager(NewMemoryStore())
	manager.Secure = false

	resp, _ := runRequest(manager, "", func(c *core.Context) {
		manager.Set(c, "user", "ada")
	})
	oldID := sessionID(resp)

	resp2, _ := runRequest(manager, oldID, func(c *core.Context) {
		manager.Renew(c)
	})
	newID := sessionID(resp2)
	if newID == "" || newID == oldID {
		t.Fatalf("new id = %q, old id = %q", newID, oldID)
	}

	_, _ = runRequest(manager, oldID, func(c *core.Context) {
		if got := manager.Get(c, "user"); got != nil {
			t.Fatalf("old session still has user = %v", got)
		}
	})

	_, _ = runRequest(manager, newID, func(c *core.Context) {
		if got := manager.Get(c, "user"); got != "ada" {
			t.Fatalf("new session user = %v", got)
		}
	})
}

func TestMemoryStore_Expiry(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Save("id", map[string]any{"user": "ada"}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := store.Load("id"); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestManager_CookieDefaults(t *testing.T) {
	manager := NewManager(NewMemoryStore())

	resp, _ := runRequest(manager, "", func(c *core.Context) {})
	raw := string(resp.Response.Header.Peek("Set-Cookie"))
	lower := strings.ToLower(raw)
	for _, want := range []string{
		"ply_session=",
		"path=/",
		"httponly",
		"secure",
		"samesite=lax",
		"max-age=86400",
	} {
		if !strings.Contains(lower, want) {
			t.Fatalf("cookie %q missing %q", raw, want)
		}
	}
}
