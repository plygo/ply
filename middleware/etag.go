package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/plygo/ply/core"
)

func CacheControl(value string) core.Handler {
	return func(c *core.Context) {
		if value != "" {
			c.Ctx.Response.Header.Set("Cache-Control", value)
		}
		c.Next()
	}
}

func ETag() core.Handler {
	return func(c *core.Context) {
		c.Next()

		method := string(c.Ctx.Method())
		if method != "GET" && method != "HEAD" {
			return
		}

		resp := &c.Ctx.Response
		if resp.StatusCode() != 200 || resp.BodyStream() != nil {
			return
		}
		if len(resp.Header.Peek("ETag")) > 0 {
			return
		}

		body := resp.Body()
		if len(body) == 0 {
			return
		}

		sum := sha256.Sum256(body)
		tag := `"` + hex.EncodeToString(sum[:16]) + `"`
		resp.Header.Set("ETag", tag)

		if etagMatches(string(c.Ctx.Request.Header.Peek("If-None-Match")), tag) {
			resp.SetStatusCode(304)
			resp.ResetBody()
		}
	}
}

func etagMatches(header, tag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == tag {
			return true
		}
	}
	return false
}
