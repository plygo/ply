package middleware

import (
	"strconv"
	"time"

	"github.com/plygo/ply/core"
)

type SecurityHeadersOptions struct {
	ContentSecurityPolicy   string
	FrameOptions            string
	ReferrerPolicy          string
	PermissionsPolicy       string
	CrossOriginOpenerPolicy string
	HSTSMaxAge              time.Duration
	HSTSIncludeSubdomains   bool
	HSTSPreload             bool
}

func SecurityHeaders(opts ...SecurityHeadersOptions) core.Handler {
	o := SecurityHeadersOptions{
		FrameOptions:      "DENY",
		ReferrerPolicy:    "strict-origin-when-cross-origin",
		PermissionsPolicy: "camera=(), microphone=(), geolocation=()",
	}
	if len(opts) > 0 {
		if opts[0].ContentSecurityPolicy != "" {
			o.ContentSecurityPolicy = opts[0].ContentSecurityPolicy
		}
		if opts[0].FrameOptions != "" {
			o.FrameOptions = opts[0].FrameOptions
		}
		if opts[0].ReferrerPolicy != "" {
			o.ReferrerPolicy = opts[0].ReferrerPolicy
		}
		if opts[0].PermissionsPolicy != "" {
			o.PermissionsPolicy = opts[0].PermissionsPolicy
		}
		if opts[0].CrossOriginOpenerPolicy != "" {
			o.CrossOriginOpenerPolicy = opts[0].CrossOriginOpenerPolicy
		}
		o.HSTSMaxAge = opts[0].HSTSMaxAge
		o.HSTSIncludeSubdomains = opts[0].HSTSIncludeSubdomains
		o.HSTSPreload = opts[0].HSTSPreload
	}

	return func(c *core.Context) {
		h := &c.Ctx.Response.Header
		h.Set("X-Content-Type-Options", "nosniff")
		if o.FrameOptions != "" {
			h.Set("X-Frame-Options", o.FrameOptions)
		}
		if o.ReferrerPolicy != "" {
			h.Set("Referrer-Policy", o.ReferrerPolicy)
		}
		if o.PermissionsPolicy != "" {
			h.Set("Permissions-Policy", o.PermissionsPolicy)
		}
		if o.ContentSecurityPolicy != "" {
			h.Set("Content-Security-Policy", o.ContentSecurityPolicy)
		}
		if o.CrossOriginOpenerPolicy != "" {
			h.Set("Cross-Origin-Opener-Policy", o.CrossOriginOpenerPolicy)
		}
		if o.HSTSMaxAge > 0 {
			v := "max-age=" + strconv.FormatInt(int64(o.HSTSMaxAge.Seconds()), 10)
			if o.HSTSIncludeSubdomains {
				v += "; includeSubDomains"
			}
			if o.HSTSPreload {
				v += "; preload"
			}
			h.Set("Strict-Transport-Security", v)
		}
		c.Next()
	}
}
