package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/plygo/ply/core"
	"github.com/valyala/fasthttp"
)

var ErrNotFound = errors.New("session not found")

type Store interface {
	Load(id string) (map[string]any, error)
	Save(id string, values map[string]any, ttl time.Duration) error
	Delete(id string) error
}

type Manager struct {
	Store      Store
	CookieName string
	Path       string
	TTL        time.Duration
	Secure     bool
	HTTPOnly   bool
	SameSite   fasthttp.CookieSameSite
	OnError    func(error)
}

const contextKey = "__ply_session"

type sessionData struct {
	id        string
	storedID  string
	values    map[string]any
	destroyed bool
}

func NewManager(store Store) *Manager {
	if store == nil {
		panic("session store cannot be nil")
	}
	return &Manager{
		Store:      store,
		CookieName: "ply_session",
		Path:       "/",
		TTL:        24 * time.Hour,
		Secure:     true,
		HTTPOnly:   true,
		SameSite:   fasthttp.CookieSameSiteLaxMode,
	}
}

func (m *Manager) Middleware() core.Handler {
	return func(c *core.Context) {
		data := &sessionData{}
		if id := c.Cookie(m.CookieName); id != "" {
			values, err := m.Store.Load(id)
			switch {
			case err == nil:
				data.id = id
				data.storedID = id
				data.values = values
			case !errors.Is(err, ErrNotFound):
				m.report(err)
				c.Text(500, "session unavailable")
				c.Abort()
				return
			}
		}
		if data.values == nil {
			data.id = newID()
			data.values = make(map[string]any)
		}

		c.SetSession(contextKey, data)
		c.Next()

		if data.destroyed {
			if data.storedID != "" {
				m.report(m.Store.Delete(data.storedID))
			}
			m.expireCookie(c)
			return
		}

		if err := m.Store.Save(data.id, data.values, m.TTL); err != nil {
			m.report(err)
			return
		}
		if data.storedID != "" && data.storedID != data.id {
			m.report(m.Store.Delete(data.storedID))
		}
		m.setCookie(c, data.id)
	}
}

func (m *Manager) Get(c *core.Context, key string) any {
	data, _ := c.GetSession(contextKey).(*sessionData)
	if data == nil {
		return nil
	}
	return data.values[key]
}

func (m *Manager) Set(c *core.Context, key string, value any) {
	data, _ := c.GetSession(contextKey).(*sessionData)
	if data == nil {
		return
	}
	data.values[key] = value
}

func (m *Manager) Delete(c *core.Context, key string) {
	data, _ := c.GetSession(contextKey).(*sessionData)
	if data == nil {
		return
	}
	delete(data.values, key)
}

func (m *Manager) Destroy(c *core.Context) {
	data, _ := c.GetSession(contextKey).(*sessionData)
	if data == nil {
		return
	}
	data.destroyed = true
}

func (m *Manager) Renew(c *core.Context) {
	data, _ := c.GetSession(contextKey).(*sessionData)
	if data == nil {
		return
	}
	data.id = newID()
}

func (m *Manager) setCookie(c *core.Context, id string) {
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)

	cookie.SetKey(m.CookieName)
	cookie.SetValue(id)
	cookie.SetPath(m.Path)
	cookie.SetHTTPOnly(m.HTTPOnly)
	cookie.SetSecure(m.Secure)
	cookie.SetSameSite(m.SameSite)
	if m.TTL > 0 {
		cookie.SetMaxAge(int(m.TTL.Seconds()))
	}
	c.SetCookie(cookie)
}

func (m *Manager) expireCookie(c *core.Context) {
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)

	cookie.SetKey(m.CookieName)
	cookie.SetValue("")
	cookie.SetPath(m.Path)
	cookie.SetHTTPOnly(m.HTTPOnly)
	cookie.SetSecure(m.Secure)
	cookie.SetSameSite(m.SameSite)
	cookie.SetExpire(time.Unix(0, 0))
	c.SetCookie(cookie)
}

func (m *Manager) report(err error) {
	if err != nil && m.OnError != nil {
		m.OnError(err)
	}
}

func newID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("session: cannot generate id: " + err.Error())
	}
	return hex.EncodeToString(b)
}
