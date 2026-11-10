package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/plygo/ply/core"
)

type SlogLoggerOptions struct {
	Level     slog.Level
	SkipPaths []string
}

func SlogLogger(logger *slog.Logger, opts ...SlogLoggerOptions) core.Handler {
	if logger == nil {
		panic("slog logger cannot be nil")
	}

	level := slog.LevelInfo
	skip := make(map[string]struct{})
	if len(opts) > 0 {
		level = opts[0].Level
		for _, path := range opts[0].SkipPaths {
			skip[path] = struct{}{}
		}
	}

	return func(c *core.Context) {
		path := string(c.Ctx.Path())
		if _, ok := skip[path]; ok {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		status := c.Ctx.Response.StatusCode()
		if status == 0 {
			status = 200
		}

		attrs := []slog.Attr{
			slog.String("method", string(c.Ctx.Method())),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Duration("duration", time.Since(start)),
			slog.String("remote_ip", c.Ctx.RemoteIP().String()),
		}
		if id, ok := c.GetSession("request_id").(string); ok && id != "" {
			attrs = append(attrs, slog.String("request_id", id))
		}

		logger.LogAttrs(context.Background(), level, "request", attrs...)
	}
}
