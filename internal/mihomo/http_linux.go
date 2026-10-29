//go:build linux && amd64

package mihomo

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type controllerSession struct {
	ctx     context.Context
	conn    *net.TCPConn
	reader  *bufio.Reader
	address string
	secret  string
}

func beginControllerSession(ctx context.Context, conn *net.TCPConn, address, secret string) (*controllerSession, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	deadline, _ := ctx.Deadline()
	if ctx.Err() != nil || conn.SetDeadline(deadline) != nil {
		cancel()
		return nil, nil, ErrController
	}
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()); close(cancelled) })
	cleanup := func() {
		if !stop() {
			<-cancelled
		}
		cancel()
	}
	return &controllerSession{ctx: ctx, conn: conn, reader: bufio.NewReaderSize(conn, 8192), address: address, secret: secret}, cleanup, nil
}

func (s *controllerSession) read(path string) ([]byte, error) {
	if path != "/hoopa/v1/status" && path != "/hoopa/v1/snapshot" && path != "/hoopa/v2/status" && path != "/hoopa/v2/snapshot" || s.ctx.Err() != nil {
		return nil, ErrController
	}
	request := &http.Request{Method: "GET", URL: &url.URL{Scheme: "http", Host: s.address, Path: path}, Host: s.address, Header: http.Header{"Authorization": {"Bearer " + s.secret}}}
	if request.Write(s.conn) != nil {
		return nil, ErrController
	}
	return s.response(request, http.StatusOK)
}

// Read raw headers with an 8 KiB aggregate bound before net/http parses them;
// ReadResponse alone does not enforce a finite header size. Reuse the original
// buffered stream so overreads never authorize or dial a second connection.
func (s *controllerSession) response(request *http.Request, expectedCode int) ([]byte, error) {
	raw, _, err := s.responseCode(request, expectedCode)
	return raw, err
}

func (s *controllerSession) responseCode(request *http.Request, allowedCodes ...int) ([]byte, int, error) {
	header := make([]byte, 0, 1024)
	for {
		line, err := s.reader.ReadSlice('\n')
		if err != nil || len(header)+len(line) > 8192 || len(line) < 2 || line[len(line)-2] != '\r' {
			return nil, 0, ErrController
		}
		header = append(header, line...)
		if len(line) == 2 {
			break
		}
	}
	// One body is read from the exact same stream; no redirects or retries exist.
	reader := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(header), s.reader), 1)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, 0, ErrController
	}
	defer response.Body.Close()
	allowed := false
	for _, code := range allowedCodes {
		allowed = allowed || response.StatusCode == code
	}
	contentTypes := response.Header.Values("Content-Type")
	if !allowed || response.ProtoMajor != 1 || response.ProtoMinor != 1 || response.Close || response.ContentLength <= 0 || response.ContentLength > 65536 || len(response.TransferEncoding) != 0 || len(response.Trailer) != 0 || len(response.Header.Values("Trailer")) != 0 || len(contentTypes) != 1 || contentTypes[0] != "application/json" || len(response.Header.Values("Content-Encoding")) != 0 {
		return nil, 0, ErrController
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || int64(len(raw)) != response.ContentLength || s.ctx.Err() != nil || reader.Buffered() != 0 || s.reader.Buffered() != 0 {
		return nil, 0, ErrController
	}
	return raw, response.StatusCode, nil
}
