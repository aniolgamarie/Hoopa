//go:build linux && amd64

// Package service provides transport primitives, not service authorization.
package service

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
	"unicode/utf8"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

var ErrWire = errors.New("invalid or unavailable service wire")

const wireTimeout = 15 * time.Second

// ReadRequest consumes exactly one frame and its sender's write EOF. Caller
// must have authenticated this connection and owns it after this call returns.
// This validates only the request envelope, not operation schema/authorization.
func ReadRequest(ctx context.Context, conn *net.UnixConn) (raw []byte, err error) {
	err = wireIO(ctx, conn, func() error {
		var e error
		raw, e = readFrame(conn, control.MaxRequestBytes)
		if e != nil {
			return e
		}
		_, e = control.ParseRequest(raw)
		return e
	})
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// WriteResponse validates before sending anything, then sends one frame and
// CloseWrite. It never closes the full connection on success. Original JSON
// bytes are retained, including durable Result's exact serialized representation.
func WriteResponse(ctx context.Context, conn *net.UnixConn, raw []byte) error {
	if _, err := responseID(raw); err != nil {
		return ErrWire
	}
	return wireIO(ctx, conn, func() error { return writeFrame(conn, raw) })
}

// Exchange is single-use: one request, CloseWrite, one response with matching
// request identity and EOF. It never dials, retries, writes storage or admits work.
func Exchange(ctx context.Context, conn *net.UnixConn, raw []byte) (response []byte, err error) {
	if len(raw) == 0 || len(raw) > control.MaxRequestBytes {
		return nil, ErrWire
	}
	request, e := control.ParseRequest(raw)
	if e != nil {
		return nil, ErrWire
	}
	err = wireIO(ctx, conn, func() error {
		if e := writeFrame(conn, raw); e != nil {
			return e
		}
		var e error
		response, e = readFrame(conn, control.MaxResultBytes)
		if e != nil {
			return e
		}
		id, e := responseID(response)
		if e != nil || id == nil || *id != request.ID() {
			return ErrWire
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// One absolute deadline spans all IO, including EOF. AfterFunc completion is
// fenced before restoring deadlines/returning: no late callback may close a
// descriptor already handed back to the caller. Cancellation can close it.
func wireIO(ctx context.Context, conn *net.UnixConn, work func() error) (err error) {
	if ctx == nil || conn == nil {
		return ErrWire
	}
	if e := ctx.Err(); e != nil {
		return errors.Join(ErrWire, e)
	}
	deadline := time.Now().Add(wireTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if e := conn.SetDeadline(deadline); e != nil {
		return ErrWire
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(finished) })
	defer func() {
		if !stop() {
			<-finished
		}
		if e := ctx.Err(); e != nil {
			err = errors.Join(ErrWire, e)
			return
		}
		reset := conn.SetDeadline(time.Time{})
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				err = errors.Join(ErrWire, context.DeadlineExceeded)
			} else {
				err = ErrWire
			}
		} else if reset != nil {
			err = ErrWire
		}
	}()
	return work()
}
func readFrame(conn *net.UnixConn, limit int) ([]byte, error) {
	var header [4]byte
	if _, e := io.ReadFull(conn, header[:]); e != nil {
		return nil, e
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || uint64(size) > uint64(limit) {
		return nil, ErrWire
	}
	raw := make([]byte, int(size))
	if _, e := io.ReadFull(conn, raw); e != nil {
		return nil, e
	}
	var extra [1]byte
	n, e := conn.Read(extra[:])
	if n != 0 {
		return nil, ErrWire
	}
	if e != io.EOF {
		if e != nil {
			return nil, e
		}
		return nil, ErrWire
	}
	return raw, nil
}
func writeFrame(conn *net.UnixConn, raw []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
	for _, part := range [][]byte{header[:], raw} {
		for len(part) > 0 {
			n, e := conn.Write(part)
			if e != nil {
				return e
			}
			if n == 0 {
				return ErrWire
			}
			part = part[n:]
		}
	}
	return conn.CloseWrite()
}
func wireObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var obj map[string]json.RawMessage
	e := json.Unmarshal(raw, &obj)
	return obj, e == nil && obj != nil
}
func wireFields(obj map[string]json.RawMessage, required []string, optional ...string) bool {
	if obj == nil {
		return false
	}
	allowed := map[string]bool{}
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			return false
		}
		allowed[key] = true
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range obj {
		if !allowed[key] {
			return false
		}
	}
	return true
}
func wireString(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil || s == "" {
		return "", false
	}
	return s, true
}
func wireBool(raw json.RawMessage) (bool, bool) {
	if string(raw) == "true" {
		return true, true
	}
	if string(raw) == "false" {
		return false, true
	}
	return false, false
}

// responseID enforces api-v1.schema.json Response/Error/Warning exactly at the
// envelope level. A data object is opaque; its business semantics remain the
// handler's responsibility. CanonicalJSON supplies strict duplicate/Unicode
// parsing, but original whitespace/key order are accepted and never rewritten.
func responseID(raw []byte) (*string, error) {
	if len(raw) == 0 || len(raw) > control.MaxResultBytes {
		return nil, ErrWire
	}
	canonical, e := config.CanonicalJSON(raw)
	if e != nil || len(canonical) > control.MaxResultBytes {
		return nil, ErrWire
	}
	obj, ok := wireObject(canonical)
	if !ok || !wireFields(obj, []string{"api_version", "request_id", "ok", "data", "error", "warnings"}) || string(obj["api_version"]) != `"v1"` {
		return nil, ErrWire
	}
	success, ok := wireBool(obj["ok"])
	if !ok {
		return nil, ErrWire
	}
	var id *string
	if string(obj["request_id"]) != "null" {
		s, ok := wireString(obj["request_id"])
		if !ok || utf8.RuneCountInString(s) > 128 {
			return nil, ErrWire
		}
		id = &s
	} else if success {
		return nil, ErrWire
	}
	if success {
		if _, ok := wireObject(obj["data"]); !ok || string(obj["error"]) != "null" {
			return nil, ErrWire
		}
	} else {
		if string(obj["data"]) != "null" || !wireError(obj["error"]) {
			return nil, ErrWire
		}
	}
	if len(obj["warnings"]) == 0 || obj["warnings"][0] != '[' {
		return nil, ErrWire
	}
	var warnings []json.RawMessage
	if json.Unmarshal(obj["warnings"], &warnings) != nil || warnings == nil {
		return nil, ErrWire
	}
	for _, raw := range warnings {
		w, ok := wireObject(raw)
		if !ok || !wireFields(w, []string{"code", "message"}) {
			return nil, ErrWire
		}
		for _, field := range []string{"code", "message"} {
			if _, ok := wireString(w[field]); !ok {
				return nil, ErrWire
			}
		}
	}
	return id, nil
}
func wireError(raw json.RawMessage) bool {
	obj, ok := wireObject(raw)
	if !ok || !wireFields(obj, []string{"code", "message", "retryable"}, "details") {
		return false
	}
	code, ok := wireString(obj["code"])
	if !ok {
		return false
	}
	switch code {
	case "SUBSCRIPTION_UPDATE_FAILED":
		return control.ValidateSourceUpdateFailure(raw)
	case "INVALID_ARGUMENT", "UNSUPPORTED_VERSION", "CAPABILITY_UNSUPPORTED", "CONFLICT", "REQUEST_ID_REUSED", "NOT_RUNNING", "DEPENDENCY_UNAVAILABLE", "READ_UNAVAILABLE", "SNAPSHOT_EXPIRED", "OPERATION_NOT_FOUND", "OPERATION_CAPACITY_EXHAUSTED", "APPLY_FAILED", "RECOVERY_REQUIRED", "LEDGER_UNAVAILABLE", "INTEGRITY_ERROR", "AUTHORIZATION_DENIED", "CANCELLED":
	default:
		return false
	}
	if _, ok := wireString(obj["message"]); !ok {
		return false
	}
	if _, ok := wireBool(obj["retryable"]); !ok {
		return false
	}
	if raw, exists := obj["details"]; exists {
		details, ok := wireObject(raw)
		if !ok || !wireFields(details, nil, "reason", "current_revision", "expected_revision", "operation_id", "field") {
			return false
		}
		for key, raw := range details {
			s, ok := wireString(raw)
			if !ok {
				return false
			}
			if key == "current_revision" || key == "expected_revision" || key == "operation_id" {
				if _, e := config.ParseID(s); e != nil {
					return false
				}
			}
		}
	}
	return true
}
