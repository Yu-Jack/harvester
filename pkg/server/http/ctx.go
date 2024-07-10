package http

import "net/http"

type Ctx struct {
	body       interface{}
	statusCode int
	rw         http.ResponseWriter
	req        *http.Request
}

func newDefaultHarvesterServerCtx(rw http.ResponseWriter, req *http.Request) *Ctx {
	return &Ctx{
		statusCode: http.StatusNoContent,
		rw:         rw,
		req:        req,
	}
}

func (ctx *Ctx) SetBody(body interface{}) {
	ctx.body = body
	ctx.statusCode = http.StatusOK
}

func (ctx *Ctx) SetStatus(statusCode int) { ctx.statusCode = statusCode }
func (ctx *Ctx) Req() *http.Request       { return ctx.req }
func (ctx *Ctx) RespWriter() http.ResponseWriter {
	return ctx.rw
}
