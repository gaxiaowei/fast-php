package fastcgi

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type client struct {
	conn *conn
	ids  idPool
}

func (c *client) writeRequest(reqID uint16, req *Request) (err error) {
	defer func() {
		if err != nil {
			//end request
			_ = c.conn.writeAbortRequest(reqID)
		}
	}()

	if err = c.conn.writeBeginRequest(reqID, req.Role, req.KeepConn); err != nil {
		return
	}

	if err = c.conn.writePairs(typeParams, reqID, req.Params); err != nil {
		return
	}

	stdinWriter := newWriter(c.conn, typeStdin, reqID)
	if req.Stdin != nil {
		defer func() {
			_ = req.Stdin.Close()
		}()

		p := make([]byte, 1024)
		var count int

		for {
			count, err = req.Stdin.Read(p)

			if err == io.EOF {
				err = nil
			} else if err != nil {
				_ = stdinWriter.Close()
				return
			}

			if count == 0 {
				break
			}

			_, err = stdinWriter.Write(p[:count])

			if err != nil {
				_ = stdinWriter.Close()
				return
			}
		}
	}

	if err = stdinWriter.Close(); err != nil {
		return err
	}

	return nil
}

func (c *client) readResponse(ctx context.Context, resp *ResponsePipe, req *Request) (err error) {
	var rec serviceRecord
	done := make(chan int)

	go func() {
		readLoop:

		for {
			if err := rec.read(c.conn.rwc); err != nil {
				break
			}

			switch rec.h.Type {
				case typeStdout:
					resp.stdOutWriter.Write(rec.body())

				case typeStderr:
					resp.stdErrWriter.Write(rec.body())

				case typeEndRequest:
					break readLoop

				default:
					err := fmt.Sprintf("unexpected type %#v in readLoop", rec.h.Type)
					resp.stdErrWriter.Write([]byte(err))
			}
		}

		close(done)
	}()

	select {
		case <-ctx.Done():
			err = fmt.Errorf("gofast: timeout or canceled")
		case <-done:
			//do nothing and end the function
	}

	return
}

func (c *client) Do(req *Request) (resp *ResponsePipe, err error) {
	if c.conn == nil {
		err = fmt.Errorf("client connection has been closed")

		return nil, err
	}

	reqID := c.ids.Alloc()
	resp = NewResponsePipe()
	rwError, allDone := make(chan error), make(chan int)

	//if there is a raw request, use the context deadline
	var ctx context.Context
	if req.Raw != nil {
		ctx = req.Raw.Context()
	} else {
		ctx = context.TODO()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		wg.Wait()
		close(allDone)
	}()

	go func() {
		if err := c.writeRequest(reqID, req); err != nil {
			rwError <- err
		}

		wg.Done()
	}()

	go func() {
		if err := c.readResponse(ctx, resp, req); err != nil {
			rwError <- err
		}

		wg.Done()
	}()

	go func() {
		loop:
			for {
				select {
					case err := <-rwError:
						resp.stdErrWriter.Write([]byte(err.Error()))
						continue
					case <-allDone:
						break loop
				}
			}

			c.ids.Release(reqID)
			resp.Close()
			close(rwError)
	}()

	return
}

func (c *client) Close() (err error) {
	if c.conn == nil {
		return
	}

	err = c.conn.Close()
	c.conn = nil

	return err
}

type ResponsePipe struct {
	stdOutReader io.Reader
	stdOutWriter io.WriteCloser
	stdErrReader io.Reader
	stdErrWriter io.WriteCloser
}

func NewResponsePipe() (p *ResponsePipe) {
	p = new(ResponsePipe)
	p.stdOutReader, p.stdOutWriter = io.Pipe()
	p.stdErrReader, p.stdErrWriter = io.Pipe()

	return
}

func (pipes *ResponsePipe) Close() {
	_ = pipes.stdOutWriter.Close()
	_ = pipes.stdErrWriter.Close()
}

func (pipes *ResponsePipe) WriteTo(rw http.ResponseWriter, ew io.Writer) (err error) {
	chErr := make(chan error, 2)
	defer close(chErr)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		chErr <- pipes.writeResponse(rw)
		wg.Done()
	}()

	go func() {
		chErr <- pipes.writeError(ew)
		wg.Done()
	}()

	wg.Wait()

	for i := 0; i < 2; i++ {
		if err = <-chErr; err != nil {
			return
		}
	}

	return
}

func (pipes *ResponsePipe) writeError(w io.Writer) (err error) {
	_, err = io.Copy(w, pipes.stdErrReader)
	if err != nil {
		err = fmt.Errorf("gofast: copy error: %v", err.Error())
	}

	return
}

func (pipes *ResponsePipe) writeResponse(w http.ResponseWriter) (err error) {
	lineBody := bufio.NewReaderSize(pipes.stdOutReader, 1024)
	headers := make(http.Header)
	statusCode := 0
	headerLines := 0
	sawBlankLine := false

	for {
		var line []byte
		var isPrefix bool

		line, isPrefix, err = lineBody.ReadLine()
		if isPrefix {
			w.WriteHeader(http.StatusInternalServerError)
			err = fmt.Errorf("gofast: long header line from subprocess")
			return
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			err = fmt.Errorf("gofast: error reading headers: %v", err)
			return
		}

		if len(line) == 0 {
			sawBlankLine = true
			break
		}

		headerLines++
		parts := strings.SplitN(string(line), ":", 2)
		if len(parts) < 2 {
			err = fmt.Errorf("gofast: bogus header line: %s", string(line))
			return
		}

		header, val := parts[0], parts[1]
		header = strings.TrimSpace(header)
		val = strings.TrimSpace(val)

		switch {
			case header == "Status":
				if len(val) < 3 {
					err = fmt.Errorf("gofast: bogus status (short): %q", val)
					return
				}

				var code int
				code, err = strconv.Atoi(val[0:3])

				if err != nil {
					err = fmt.Errorf("gofast: bogus status: %q\nline was %q", val, line)
					return
				}

				statusCode = code
			default:
				headers.Add(header, val)
		}
	}

	if headerLines == 0 || !sawBlankLine {
		w.WriteHeader(http.StatusInternalServerError)
		err = fmt.Errorf("gofast: no headers")
		return
	}

	if loc := headers.Get("Location"); loc != "" {
		if statusCode == 0 {
			statusCode = http.StatusFound
		}
	}

	if statusCode == 0 && headers.Get("Content-Type") == "" {
		w.WriteHeader(http.StatusInternalServerError)
		err = fmt.Errorf("gofast: missing required Content-Type in headers")
		return
	}

	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	for k, vv := range headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(statusCode)
	_, err = io.Copy(w, lineBody)

	if err != nil {
		err = fmt.Errorf("gofast: copy error: %v", err)
	}

	return
}
