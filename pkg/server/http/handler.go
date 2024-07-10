package http

import (
	"errors"
	"net/http"

	"github.com/rancher/apiserver/pkg/apierror"
	"github.com/rancher/wrangler/pkg/schemas/validation"

	"github.com/harvester/harvester/pkg/util"
)

var (
	EmptyResponseBody = struct{}{}

	// Extended status code for rancher/wrangler/pkg/schemas/validation
	BadRequest       = validation.ErrorCode{Code: "BadRequest", Status: 400}
	FailedDependency = validation.ErrorCode{Code: "FailedDependency", Status: 424}
)

type HarvesterServerHandler interface {
	Do(_ http.ResponseWriter, r *http.Request) (interface{}, error)
}

type harvesterServerHandler struct {
	httpHandler HarvesterServerHandler
}

func NewHandler(httpHandler HarvesterServerHandler) http.Handler {
	return &harvesterServerHandler{
		httpHandler: httpHandler,
	}
}

func (handler *harvesterServerHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	resp, err := handler.httpHandler.Do(rw, req)
	if err != nil {
		status := http.StatusInternalServerError
		var e *apierror.APIError
		if errors.As(err, &e) {
			status = e.Code.Status
		}
		rw.WriteHeader(status)
		_, _ = rw.Write([]byte(err.Error()))
		return
	}

	if resp == nil {
		rw.WriteHeader(http.StatusNoContent)
		return
	}

	if resp == EmptyResponseBody {
		util.ResponseOK(rw)
		return
	}

	util.ResponseOKWithBody(rw, resp)
}
