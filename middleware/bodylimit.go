package middleware

import (
	"github.com/plygo/ply/core"
)

func BodyLimit(maxBytes int64) core.Handler {
	if maxBytes <= 0 {
		panic("body limit must be positive")
	}

	return func(c *core.Context) {
		if int64(c.Ctx.Request.Header.ContentLength()) > maxBytes ||
			int64(len(c.Ctx.Request.Body())) > maxBytes {
			c.Text(413, "request body too large")
			c.Abort()
			return
		}
		c.Next()
	}
}
