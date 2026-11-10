package route

import (
	"github.com/plygo/ply"
	"github.com/plygo/ply/core"
	"github.com/plygo/ply/examples/controller"
)

func NewRouter(router *ply.App, controller *controller.HandlerController) {
	router.Get("/:name", controller.GetHandler)
	router.Post("/hello", func(ctx *core.Context) {
		ctx.Text(200, "Hello World")
	})
}
