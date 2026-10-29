//go:build linux && amd64

// Package sourceidentity probes source ownership at a held, original TCP entry.
// It does not forward traffic or turn a downstream proxy socket into its caller.
package sourceidentity

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var (
	ErrUnknown    = errors.New("source ownership unavailable")
	ErrShared     = errors.New("socket shared by processes")
	ErrVisibility = errors.New("process owner set not fully visible")
)

type Identity struct {
	PID         int
	Name        string
	StartTicks  string
	BootID      string
	NetNS       string
	SocketInode string
}

func procAddress(addr netip.AddrPort) (string, error) {
	if !addr.Addr().Is4() || !addr.Addr().IsLoopback() || addr.Port() == 0 {
		return "", ErrUnknown
	}
	b := addr.Addr().As4()
	return fmt.Sprintf("%02X%02X%02X%02X:%04X", b[3], b[2], b[1], b[0], addr.Port()), nil
}

func socketInode(source, entry netip.AddrPort) (string, error) {
	local, err := procAddress(source)
	if err != nil {
		return "", err
	}
	remote, err := procAddress(entry)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile("/proc/self/net/tcp")
	if err != nil {
		return "", ErrUnknown
	}
	found := ""
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[1] != local || f[2] != remote || f[3] != "01" {
			continue
		}
		uid, err := strconv.Atoi(f[7])
		if err != nil || uid != os.Geteuid() || f[9] == "0" || found != "" {
			return "", ErrUnknown
		}
		if _, err := strconv.ParseUint(f[9], 10, 64); err != nil {
			return "", ErrUnknown
		}
		found = f[9]
	}
	if found == "" {
		return "", ErrUnknown
	}
	return found, nil
}

func processIdentity(pid int) (Identity, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	raw, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return Identity{}, ErrUnknown
	}
	text := string(raw)
	left, right := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if left < 1 || right <= left || strings.TrimSpace(text[:left]) != strconv.Itoa(pid) {
		return Identity{}, ErrUnknown
	}
	f := strings.Fields(text[right+1:])
	if len(f) < 20 || f[0] == "Z" {
		return Identity{}, ErrUnknown
	}
	if _, err := strconv.ParseUint(f[19], 10, 64); err != nil {
		return Identity{}, ErrUnknown
	}
	ns, err := os.Readlink(filepath.Join(dir, "ns/net"))
	if err != nil {
		return Identity{}, ErrUnknown
	}
	return Identity{PID: pid, Name: text[left+1 : right], StartTicks: f[19], NetNS: ns}, nil
}

func owns(pid int, inode string) (bool, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	fds, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, fd := range fds {
		v, err := os.Readlink(filepath.Join(dir, fd.Name()))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if v == "socket:["+inode+"]" {
			return true, nil
		}
	}
	return false, nil
}

// Resolve is deliberately limited to same-user, same-network-namespace IPv4
// loopback sockets that remain ESTABLISHED while an entry holds admission.
// /proc enumeration is not an atomic kernel ownership oracle: FD transfer races,
// invisible processes and platform variants still need separate qualification.
func Resolve(source, entry netip.AddrPort) (Identity, error) {
	inode, err := socketInode(source, entry)
	if err != nil {
		return Identity{}, fmt.Errorf("source_socket: %w", err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return Identity{}, fmt.Errorf("boot_identity: %w", ErrUnknown)
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return Identity{}, fmt.Errorf("network_namespace: %w", ErrUnknown)
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return Identity{}, fmt.Errorf("process_listing: %w", ErrUnknown)
	}
	var owner Identity
	inspected := 0
	for _, p := range processes {
		pid, err := strconv.Atoi(p.Name())
		if err != nil || pid <= 0 {
			continue
		}
		info, err := os.Stat(filepath.Join("/proc", p.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Identity{}, fmt.Errorf("process_stat: %w", ErrUnknown)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return Identity{}, fmt.Errorf("process_uid: %w", ErrUnknown)
		}
		if stat.Uid != uint32(os.Geteuid()) {
			continue
		}
		inspected++
		if inspected > 4096 {
			return Identity{}, fmt.Errorf("process_limit: %w", ErrUnknown)
		}
		match, err := owns(pid, inode)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return Identity{}, fmt.Errorf("%w: %w", ErrUnknown, ErrVisibility)
			}
			return Identity{}, fmt.Errorf("descriptor_listing: %w", ErrUnknown)
		}
		if !match {
			continue
		}
		// Unrelated zombies have no descriptors and need no live identity.
		// Bookend a second descriptor check for each actual candidate owner.
		before, err := processIdentity(pid)
		if err != nil {
			return Identity{}, fmt.Errorf("candidate_identity: %w", ErrUnknown)
		}
		if match, err := owns(pid, inode); err != nil || !match {
			return Identity{}, fmt.Errorf("candidate_descriptor: %w", ErrUnknown)
		}
		after, err := processIdentity(pid)
		if err != nil || before != after || after.NetNS != ns {
			return Identity{}, fmt.Errorf("candidate_changed: %w", ErrUnknown)
		}
		if owner.PID != 0 {
			return Identity{}, ErrShared
		}
		owner = after
	}
	if owner.PID == 0 {
		return Identity{}, fmt.Errorf("no_owner: %w", ErrUnknown)
	}
	if current, err := socketInode(source, entry); err != nil || current != inode {
		return Identity{}, fmt.Errorf("socket_changed: %w", ErrUnknown)
	}
	if match, err := owns(owner.PID, inode); err != nil || !match {
		return Identity{}, fmt.Errorf("owner_descriptor_changed: %w", ErrUnknown)
	}
	if current, err := processIdentity(owner.PID); err != nil || current != owner {
		return Identity{}, fmt.Errorf("owner_identity_changed: %w", ErrUnknown)
	}
	owner.BootID = strings.TrimSpace(string(boot))
	owner.SocketInode = inode
	return owner, nil
}
