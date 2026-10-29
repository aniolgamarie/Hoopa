//go:build linux && amd64

package platform

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hoopa/internal/p1testhook"
)

var ErrControllerUnverified = errors.New("CONTROLLER_OWNER_UNVERIFIED")

func controllerEndpoint(address string) (*net.TCPAddr, error) {
	host, port, err := net.SplitHostPort(address)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || host != "127.0.0.1" || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return nil, ErrControllerUnverified
	}
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: n}, nil
}

func controllerAddress(address net.Addr) (*net.TCPAddr, error) {
	a, ok := address.(*net.TCPAddr)
	if !ok || a == nil || a.Zone != "" || !a.IP.Equal(net.IPv4(127, 0, 0, 1)) || a.Port < 1 || a.Port > 65535 {
		return nil, ErrControllerUnverified
	}
	return a, nil
}

func procTCPAddress(a *net.TCPAddr) string {
	return fmt.Sprintf("%08X:%04X", binary.LittleEndian.Uint32(a.IP.To4()), a.Port)
}

// procTCPInode selects the server end of this exact connected tuple. The table
// is namespace-wide; a match is NOT yet evidence that the target owns the FD.
// Missing/zero inode means accept may not have installed an FD yet, never proof.
func procTCPInode(raw []byte, server, client *net.TCPAddr) (string, error) {
	if len(raw) == 0 || len(raw) > 1<<20 || raw[len(raw)-1] != '\n' || strings.ContainsRune(string(raw), 0) {
		return "", ErrControllerUnverified
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	header := strings.Fields(lines[0])
	if len(header) < 4 || header[0] != "sl" || header[1] != "local_address" || header[2] != "rem_address" || header[3] != "st" {
		return "", ErrControllerUnverified
	}
	wantedLocal, wantedRemote := procTCPAddress(server), procTCPAddress(client)
	inode, matches := "", 0
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return "", ErrControllerUnverified
		}
		for _, address := range fields[1:3] {
			if len(address) != 13 || address[8] != ':' {
				return "", ErrControllerUnverified
			}
			ip, e := strconv.ParseUint(address[:8], 16, 32)
			port, p := strconv.ParseUint(address[9:], 16, 16)
			if e != nil || p != nil || fmt.Sprintf("%08X:%04X", ip, port) != address {
				return "", ErrControllerUnverified
			}
		}
		if fields[1] != wantedLocal || fields[2] != wantedRemote {
			continue
		}
		matches++
		if matches != 1 || fields[3] != "01" {
			return "", ErrControllerUnverified
		}
		n, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != fields[9] {
			return "", ErrControllerUnverified
		}
		if n != 0 {
			inode = fields[9]
		}
	}
	return inode, nil
}

func processOwnsSocket(ctx context.Context, pid int, inode string) (bool, error) {
	path := "/proc/" + strconv.Itoa(pid) + "/fd"
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, err
	}
	names, err := f.Readdirnames(4097)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err = errors.Join(err, f.Close()); err != nil || len(names) > 4096 {
		return false, errors.Join(ErrControllerUnverified, err)
	}
	found := false
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		n, err := strconv.ParseUint(name, 10, 31)
		if err != nil || strconv.FormatUint(n, 10) != name {
			return false, ErrControllerUnverified
		}
		link, err := os.Readlink(path + "/" + name)
		if err != nil {
			// A disappearing FD is inconclusive too. Do not skip permission or
			// lifetime errors and silently claim a complete process observation.
			return false, err
		}
		if link == "socket:["+inode+"]" {
			found = true
		}
	}
	return found, nil
}

// DialOwnedTCP returns the SAME connection whose accepted server FD was found
// in this process, between complete revalidations of the original pidfd identity.
// It sends no bytes, resolves no hostname and uses no proxy. Authentication and
// expected running-config/status validation remain the controller's responsibility.
// Every new connection needs its own proof; this is not a reusable port permit.
// The returned connection belongs to the caller and needs its own I/O deadline.
// The caller must retain its request context when using an HTTP Transport, whose
// dial context may otherwise outlive the request. Same-UID hostile FD transfers
// or exec races are outside the ADR013 boundary.
func (h *ProcessHandle) DialOwnedTCP(ctx context.Context, address string) (connection *net.TCPConn, result error) {
	if h == nil || ctx == nil {
		return nil, ErrControllerUnverified
	}
	stage := "endpoint"
	if p1testhook.Enabled {
		began := time.Now()
		// Registered before lock/timeout defers so the observer runs only after
		// the original proof has failed and its handle mutex has been released.
		defer func() {
			if result == nil {
				return
			}
			var network net.Error
			_ = p1testhook.Record("platform", "controller-proof-failed", "", "", "", false, map[string]any{
				"stage": stage, "pid": h.Identity().PID, "elapsed_ns": time.Since(began).Nanoseconds(),
				"deadline_exceeded": errors.Is(result, context.DeadlineExceeded), "canceled": errors.Is(result, context.Canceled),
				"network_timeout": errors.As(result, &network) && network.Timeout(), "not_exist": errors.Is(result, os.ErrNotExist),
				"process_unverified": errors.Is(result, ErrProcessUnverified), "controller_unverified": errors.Is(result, ErrControllerUnverified),
			})
		}()
	}
	expected, err := controllerEndpoint(address)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	h.mu.Lock()
	defer h.mu.Unlock()
	stage = "process-before"
	if err = h.verify(ctx); err != nil {
		return nil, errors.Join(ErrControllerUnverified, err)
	}
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	stage = "dial"
	c, err := dialer.DialContext(ctx, "tcp4", address)
	if err != nil {
		return nil, errors.Join(ErrControllerUnverified, err)
	}
	accepted := false
	defer func() {
		if !accepted {
			result = errors.Join(result, c.Close())
		}
	}()
	local, e := controllerAddress(c.LocalAddr())
	stage = "tuple"
	remote, err := controllerAddress(c.RemoteAddr())
	if e != nil || err != nil || !remote.IP.Equal(expected.IP) || remote.Port != expected.Port {
		return nil, ErrControllerUnverified
	}
	proof, cancelProof := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancelProof()
	for {
		stage = "proof-budget"
		if err = proof.Err(); err != nil {
			return nil, errors.Join(ErrControllerUnverified, err)
		}
		stage = "tcp-table"
		raw, err := readProc(fmt.Sprintf("/proc/%d/net/tcp", h.identity.PID), 1<<20)
		if err != nil {
			return nil, errors.Join(ErrControllerUnverified, err)
		}
		stage = "tuple-inode"
		inode, err := procTCPInode(raw, remote, local)
		if err != nil {
			return nil, err
		}
		found := false
		if inode != "" {
			stage = "owned-fd-scan"
			found, err = processOwnsSocket(proof, h.identity.PID, inode)
			if err != nil {
				return nil, errors.Join(ErrControllerUnverified, err)
			}
		}
		if found {
			stage = "process-after"
			if err = proof.Err(); err == nil {
				err = h.verify(ctx)
			}
			if err != nil {
				return nil, errors.Join(ErrControllerUnverified, err)
			}
			accepted = true
			return c.(*net.TCPConn), nil // tcp4 always uses a native TCPConn.
		}
		timer := time.NewTimer(2 * time.Millisecond)
		stage = "accept-wait"
		select {
		case <-proof.Done():
			timer.Stop()
			return nil, errors.Join(ErrControllerUnverified, proof.Err())
		case <-timer.C:
		}
	}
}
