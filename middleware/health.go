package middleware

import (
	"github.com/plygo/ply/core"
)

func Health() core.Handler {
	return func(c *core.Context) {
		c.Ctx.Response.Header.Set("Cache-Control", "no-store")
		c.JSON(200, map[string]string{"status": "ok"})
	}
}

func Readiness(checks ...func() error) core.Handler {
	return func(c *core.Context) {
		c.Ctx.Response.Header.Set("Cache-Control", "no-store")
		for _, check := range checks {
			if check == nil {
				continue
			}
			if err := check(); err != nil {
				c.JSON(503, map[string]string{"status": "unavailable"})
				return
			}
		}
		c.JSON(200, map[string]string{"status": "ok"})
	}
}
