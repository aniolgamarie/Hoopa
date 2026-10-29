// Command fixture exchanges calibration tokens on local IPv4 TCP connections.
// Its observations never establish verified identity or BPF association.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"hoopa/experiments/sourceidentity/acceptedwitness"
)

type options struct {
	role        string
	generation  uint64
	port, count int
	token       string
	allow       bool
}

func parseOptions(args []string) (options, error) {
	var o options
	var generation string
	flags := flag.NewFlagSet("sourceidentity-fixture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.role, "role", "", "serve or client")
	flags.StringVar(&generation, "generation", "", "nonzero uint64 generation")
	flags.IntVar(&o.port, "port", 0, "loopback TCP port")
	flags.IntVar(&o.count, "count", 0, "server connection count, 1..16")
	flags.StringVar(&o.token, "token", "", "client token: 32 lowercase hexadecimal characters")
	flags.BoolVar(&o.allow, "allow-loopback", false, "allow bounded loopback socket operations")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 {
		return o, errors.New("unexpected positional arguments")
	}
	if !o.allow {
		return o, errors.New("--allow-loopback is required before socket operations")
	}
	var err error
	o.generation, err = strconv.ParseUint(generation, 10, 64)
	if err != nil || o.generation == 0 {
		return o, errors.New("--generation requires a nonzero decimal uint64")
	}
	if o.port < 1 || o.port > 65535 {
		return o, errors.New("--port must be 1..65535")
	}
	supplied := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { supplied[f.Name] = true })
	switch o.role {
	case "serve":
		if o.count < 1 || o.count > 16 || supplied["token"] {
			return o, errors.New("serve requires --count 1..16 and accepts no --token")
		}
	case "client":
		if !validToken(o.token) || supplied["count"] {
			return o, errors.New("client requires --token (32 lowercase hex characters) and accepts no --count")
		}
	default:
		return o, errors.New("--role must be serve or client")
	}
	return o, nil
}

func validToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// The sender half-closes its write side after exactly one token frame. Checking
// EOF after the 33-byte frame rejects trailing bytes, including separate packets.
func readFrame(reader io.Reader) (string, error) {
	var frame [33]byte
	if _, err := io.ReadFull(reader, frame[:]); err != nil {
		return "", fmt.Errorf("token frame: %w", err)
	}
	if frame[32] != '\n' || !validToken(string(frame[:32])) {
		return "", errors.New("invalid token frame")
	}
	var extra [1]byte
	n, err := io.ReadFull(reader, extra[:])
	if n != 0 || err != io.EOF {
		return "", errors.New("token frame has trailing bytes or no EOF")
	}
	return string(frame[:32]), nil
}

func writeFrame(writer io.Writer, token string) error {
	if !validToken(token) {
		return errors.New("invalid token")
	}
	frame := []byte(token + "\n")
	for len(frame) != 0 {
		n, err := writer.Write(frame)
		if n < 0 || n > len(frame) {
			return errors.New("invalid write count")
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

type socketObservation struct {
	SocketCookie   string `json:"socket_cookie"`
	NetNSCookie    string `json:"netns_cookie"`
	LocalEndpoint  string `json:"local_endpoint"`
	RemoteEndpoint string `json:"remote_endpoint"`
}
type processObservation struct {
	PID        int    `json:"pid"`
	StartTicks string `json:"start_ticks"`
	PIDNSInode string `json:"pidns_inode"`
}
type record struct {
	Schema             string `json:"schema"`
	Event              string `json:"event"`
	Role               string `json:"role"`
	Token              string `json:"token"`
	Generation         string `json:"generation"`
	IdentityCapability string `json:"identity_capability"`
	*socketObservation
	*processObservation
	Witness *acceptedwitness.Snapshot `json:"witness,omitempty"`
}

func connectionRecord(o options, token string) record {
	return record{Schema: "sourceidentity-fixture-v1", Event: "connection", Role: o.role,
		Token: token, Generation: strconv.FormatUint(o.generation, 10), IdentityCapability: "unverified"}
}

type stream interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
}
type clientStream interface {
	stream
	CloseWrite() error
}
type witnessStream interface {
	stream
	Snapshot() (acceptedwitness.Snapshot, error)
}
type witnessListener interface {
	Accept() (witnessStream, error)
	SetDeadline(time.Time) error
	Close() error
}
type runtimeDeps struct {
	now     func() time.Time
	dial    func(int, time.Time) (clientStream, error)
	listen  func(int, uint64) (witnessListener, error)
	capture func(clientStream) (socketObservation, error)
	process func() (processObservation, error)
}

func connectionDeadline(now, total time.Time) time.Time {
	deadline := now.Add(3 * time.Second)
	if deadline.After(total) {
		return total
	}
	return deadline
}
func execute(o options, output io.Writer, deps runtimeDeps) (err error) {
	// Keep this boundary safe for internal callers as well as the CLI.
	if !o.allow {
		return errors.New("--allow-loopback is required before socket operations")
	}
	total := deps.now().Add(10 * time.Second)
	encoder := json.NewEncoder(output)
	if o.role == "client" {
		conn, err := deps.dial(o.port, total)
		if err != nil {
			return err
		}
		result, err := clientExchange(o, conn, total, deps)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	listener, err := deps.listen(o.port, o.generation)
	if err != nil {
		return err
	}
	listenerOwned := true
	defer func() {
		if listenerOwned {
			err = errors.Join(err, listener.Close())
		}
	}()
	if err := listener.SetDeadline(total); err != nil {
		return err
	}
	ready := struct {
		Schema     string `json:"schema"`
		Event      string `json:"event"`
		Generation string `json:"generation"`
		Port       int    `json:"port"`
	}{"sourceidentity-fixture-v1", "listening", strconv.FormatUint(o.generation, 10), o.port}
	if err := encoder.Encode(ready); err != nil {
		return err
	}
	// At most 16 records are retained until the entire server run and resource
	// cleanup succeed. A later failed connection must not publish a partial run.
	records := make([]record, 0, o.count)
	for i := 0; i < o.count; i++ {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		result, err := serverExchange(o, conn, connectionDeadline(deps.now(), total))
		if err != nil {
			return err
		}
		records = append(records, result)
	}
	listenerOwned = false // Close is attempted exactly once, including errors.
	if err := listener.Close(); err != nil {
		return err
	}
	for _, result := range records {
		if err := encoder.Encode(result); err != nil {
			return err
		}
	}
	return nil
}
func clientExchange(o options, conn clientStream, total time.Time, deps runtimeDeps) (result record, err error) {
	defer func() { err = errors.Join(err, conn.Close()) }()
	if err := conn.SetDeadline(connectionDeadline(deps.now(), total)); err != nil {
		return record{}, err
	}
	socket, err := deps.capture(conn)
	if err != nil {
		return record{}, err
	}
	process, err := deps.process()
	if err != nil {
		return record{}, err
	}
	if err := writeFrame(conn, o.token); err != nil {
		return record{}, err
	}
	if err := conn.CloseWrite(); err != nil {
		return record{}, err
	}
	token, err := readFrame(conn)
	if err != nil {
		return record{}, err
	}
	if token != o.token {
		return record{}, errors.New("echo token mismatch")
	}
	result = connectionRecord(o, token)
	result.socketObservation = &socket
	result.processObservation = &process
	return result, nil
}
func serverExchange(o options, conn witnessStream, deadline time.Time) (result record, err error) {
	defer func() { err = errors.Join(err, conn.Close()) }()
	if err := conn.SetDeadline(deadline); err != nil {
		return record{}, err
	}
	witness, err := conn.Snapshot()
	if err != nil {
		return record{}, err
	}
	token, err := readFrame(conn)
	if err != nil {
		return record{}, err
	}
	if err := writeFrame(conn, token); err != nil {
		return record{}, err
	}
	result = connectionRecord(o, token)
	result.Witness = &witness
	return result, nil
}

// /proc/<pid>/stat comm is parenthesized and may itself contain spaces and ')'.
func parseStartTicks(raw []byte, pid int) (string, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return "", errors.New("invalid bounded proc stat")
	}
	text := string(raw)
	open := strings.IndexByte(text, '(')
	close := strings.LastIndexByte(text, ')')
	if open < 2 || close <= open || text[open-1] != ' ' || close+2 >= len(text) || text[close+1] != ' ' {
		return "", errors.New("invalid proc stat comm")
	}
	actual, err := strconv.Atoi(strings.TrimSpace(text[:open]))
	if err != nil || actual != pid || pid <= 0 {
		return "", errors.New("proc stat pid mismatch")
	}
	fields := strings.Fields(text[close+2:]) // starts with field 3 (state)
	if len(fields) < 20 || len(fields[0]) != 1 {
		return "", errors.New("truncated proc stat")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return "", errors.New("invalid proc start ticks")
	}
	return strconv.FormatUint(ticks, 10), nil
}

func main() {
	o, err := parseOptions(os.Args[1:])
	if err == nil {
		err = runFixture(o, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
