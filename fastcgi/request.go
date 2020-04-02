package fastcgi

import (
	"io"
	"net/http"
)

//Request hold information of a standard
type Request struct {
	Raw      *http.Request
	Role     uint16
	Params   map[string]string
	Stdin    io.ReadCloser
	Data     io.ReadCloser
	KeepConn uint8
}

type commonParams map[string]string
type OptionRequest func(req *Request)


func NewRequest(request *http.Request, reqConfig ...OptionRequest) *Request {
	req := &Request{
		Raw:    request,
		Role:   RoleResponder,
		Params: make(map[string]string),
		KeepConn: uint8(1),
	}

	//if no http request, return here
	if request == nil {
		return nil
	}

	for _, fn := range reqConfig {
		fn(req)
	}

	//pass body (io.ReadCloser) to stdio
	req.Stdin = request.Body

	return req
}

func buildParams() commonParams {
	params := make(commonParams)
}
