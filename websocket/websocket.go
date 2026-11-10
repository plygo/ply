// Package websocket adds WebSocket upgrade and connection helpers to Ply.
package websocket

import (
	"context"
	"sync"
	"time"

	ws "github.com/fasthttp/websocket"
	"github.com/plygo/ply/core"
	"github.com/valyala/fasthttp"
)

const (
	TextMessage   = ws.TextMessage
	BinaryMessage = ws.BinaryMessage
	PingMessage   = ws.PingMessage
	PongMessage   = ws.PongMessage
	CloseMessage  = ws.CloseMessage
)

type Options struct {
	HandshakeTimeout  time.Duration
	ReadBufferSize    int
	WriteBufferSize   int
	Subprotocols      []string
	CheckOrigin       func(*fasthttp.RequestCtx) bool
	EnableCompression bool
	ReadLimit         int64
	PingInterval      time.Duration
	PongWait          time.Duration
	WriteWait         time.Duration
	DisablePing       bool
}

type Upgrader struct {
	upgrader ws.FastHTTPUpgrader
	opts     Options
}

type Client struct {
	conn         *ws.Conn
	writeMu      sync.Mutex
	writeWait    time.Duration
	pingInterval time.Duration
}

func New(opts ...Options) *Upgrader {
	o := defaultOptions()
	if len(opts) > 0 {
		apply(&o, opts[0])
	}

	return &Upgrader{
		opts: o,
		upgrader: ws.FastHTTPUpgrader{
			HandshakeTimeout:  o.HandshakeTimeout,
			ReadBufferSize:    o.ReadBufferSize,
			WriteBufferSize:   o.WriteBufferSize,
			Subprotocols:      o.Subprotocols,
			CheckOrigin:       o.CheckOrigin,
			EnableCompression: o.EnableCompression,
			Error: func(ctx *fasthttp.RequestCtx, status int, reason error) {
				ctx.SetStatusCode(status)
				ctx.SetContentType("text/plain")
				ctx.SetBodyString(reason.Error())
			},
		},
	}
}

func (u *Upgrader) Handler(fn func(*Client)) core.Handler {
	if fn == nil {
		panic("websocket handler cannot be nil")
	}

	return func(c *core.Context) {
		var wg sync.WaitGroup

		err := u.upgrader.Upgrade(c.Ctx, func(conn *ws.Conn) {
			client := &Client{
				conn:         conn,
				writeWait:    u.opts.WriteWait,
				pingInterval: u.opts.PingInterval,
			}
			if u.opts.ReadLimit > 0 {
				conn.SetReadLimit(u.opts.ReadLimit)
			}

			ctx, cancel := context.WithCancel(context.Background())

			if !u.opts.DisablePing && u.opts.PingInterval > 0 {
				if u.opts.PongWait > 0 {
					_ = conn.SetReadDeadline(time.Now().Add(u.opts.PongWait))
					conn.SetPongHandler(func(string) error {
						return conn.SetReadDeadline(time.Now().Add(u.opts.PongWait))
					})
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					client.pingLoop(ctx)
				}()
			}

			defer func() {
				cancel()
				_ = client.Close()
				wg.Wait()
			}()
			fn(client)
		})
		if err != nil {
			return
		}
	}
}

func defaultOptions() Options {
	return Options{
		HandshakeTimeout: 5 * time.Second,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		ReadLimit:        1 << 20,
		PingInterval:     30 * time.Second,
		PongWait:         60 * time.Second,
		WriteWait:        10 * time.Second,
	}
}

func apply(dst *Options, src Options) {
	if src.HandshakeTimeout > 0 {
		dst.HandshakeTimeout = src.HandshakeTimeout
	}
	if src.ReadBufferSize > 0 {
		dst.ReadBufferSize = src.ReadBufferSize
	}
	if src.WriteBufferSize > 0 {
		dst.WriteBufferSize = src.WriteBufferSize
	}
	if len(src.Subprotocols) > 0 {
		dst.Subprotocols = src.Subprotocols
	}
	if src.CheckOrigin != nil {
		dst.CheckOrigin = src.CheckOrigin
	}
	if src.EnableCompression {
		dst.EnableCompression = true
	}
	if src.ReadLimit != 0 {
		dst.ReadLimit = src.ReadLimit
	}
	if src.PingInterval != 0 {
		dst.PingInterval = src.PingInterval
	}
	if src.PongWait != 0 {
		dst.PongWait = src.PongWait
	}
	if src.WriteWait != 0 {
		dst.WriteWait = src.WriteWait
	}
	if src.DisablePing {
		dst.DisablePing = true
	}
}

func (c *Client) ReadMessage() (int, []byte, error) {
	return c.conn.ReadMessage()
}

func (c *Client) ReadJSON(v any) error {
	return c.conn.ReadJSON(v)
}

func (c *Client) WriteMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeWait > 0 {
		if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeWait)); err != nil {
			return err
		}
	}
	return c.conn.WriteMessage(messageType, data)
}

func (c *Client) WriteJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeWait > 0 {
		if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeWait)); err != nil {
			return err
		}
	}
	return c.conn.WriteJSON(v)
}

func (c *Client) WriteText(data string) error {
	return c.WriteMessage(TextMessage, []byte(data))
}

func (c *Client) WriteBinary(data []byte) error {
	return c.WriteMessage(BinaryMessage, data)
}

func (c *Client) Ping(data string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteControl(PingMessage, []byte(data), time.Now().Add(c.writeWait))
}

func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) CloseWith(code int, text string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteControl(ws.CloseMessage, ws.FormatCloseMessage(code, text), time.Now().Add(c.writeWait))
}

func (c *Client) SetReadLimit(limit int64) {
	c.conn.SetReadLimit(limit)
}

func (c *Client) SetCloseHandler(fn func(code int, text string) error) {
	c.conn.SetCloseHandler(fn)
}

func (c *Client) SetPongHandler(fn func(string) error) {
	c.conn.SetPongHandler(fn)
}

func (c *Client) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *Client) RemoteAddr() string {
	if c.conn == nil {
		return ""
	}
	return c.conn.RemoteAddr().String()
}

// Raw returns the underlying connection for features not covered by Client.
// The caller must not write concurrently with Client methods.
func (c *Client) Raw() *ws.Conn {
	return c.conn
}

func (c *Client) pingLoop(ctx context.Context) {
	if c.pingInterval <= 0 {
		return
	}

	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Ping(""); err != nil {
				_ = c.Close()
				return
			}
		}
	}
}
