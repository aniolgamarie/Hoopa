#!/usr/bin/env python3
"""Finite guest-only client/oracle; never resolver input or identity authority.

Leader comm samples are non-atomic observations. Rename/exec consistency and
process paths remain unknown; start_ticks are proc USER_HZ ticks, not kernel ns.
"""
import argparse
import array
import json
import os
from pathlib import Path
import re
import signal
import socket
import struct
import sys
import threading
import time

TIMEOUT = 3.0
REPORT_LIMIT = 4096


class ClientError(ValueError):
    def __init__(self, stage, detail):
        self.stage = stage
        super().__init__(str(detail))


def require(condition, stage, detail):
    if not condition:
        raise ClientError(stage, detail)


def start_ticks(raw, pid):
    """Field 22: the comm between parentheses may contain spaces and ')'."""
    require(isinstance(raw, str) and len(raw) <= 4096, "proc", "invalid stat input")
    begin, end = raw.find("("), raw.rfind(")")
    require(begin > 0 and end > begin and raw[:begin].strip() == str(pid)
            and raw[end + 1:].startswith(" "), "proc", "invalid stat PID/comm")
    fields = raw[end + 1:].split()
    require(len(fields) >= 20 and re.fullmatch(r"[0-9]+", fields[19]),
            "proc", "missing or invalid stat field 22")
    value = int(fields[19])
    require(value < 2**64, "proc", "stat start ticks overflow")
    return str(value)


def leader_comm(pid):
    try:
        with open("/proc/{}/task/{}/comm".format(pid, pid), "rb") as stream:
            raw = stream.read(65)
        if len(raw) > 64:
            return None
        return (raw[:-1] if raw.endswith(b"\n") else raw).decode("utf-8")
    except (OSError, UnicodeError):
        return None


def socket_identity(conn):
    require(conn.family == socket.AF_INET
            and conn.getsockopt(socket.SOL_SOCKET, socket.SO_TYPE) == socket.SOCK_STREAM,
            "socket-metadata", "IPv4 TCP socket required")
    cookie = struct.unpack("=Q", conn.getsockopt(socket.SOL_SOCKET, 57, 8))[0]
    netns = struct.unpack("=Q", conn.getsockopt(socket.SOL_SOCKET, 71, 8))[0]
    local, remote = conn.getsockname(), conn.getpeername()
    require(cookie != 0 and netns != 0 and local[0] == remote[0] == "127.0.0.1"
            and 1 <= local[1] <= 65535 and 1 <= remote[1] <= 65535,
            "socket-metadata", "invalid cookie/namespace/loopback endpoints")
    return dict(socket_cookie=str(cookie), netns_cookie=str(netns),
                local_endpoint=list(local), remote_endpoint=list(remote))


def capture(conn, args, before):
    pid = os.getpid()
    with open("/proc/self/stat", "r") as stream:
        ticks = start_ticks(stream.read(4097), pid)
    inode = os.stat("/proc/self/ns/pid").st_ino
    require(0 < inode < 2**64, "proc", "invalid PID namespace inode")
    result = dict(schema="sourceidentity-online-client-v1", mode=args.mode,
                  token=args.token, generation=args.generation, pid=pid, tgid=pid,
                  connect_tid=threading.get_native_id(), start_ticks=ticks,
                  pidns_inode=str(inode), leader_comm_before=before,
                  leader_comm_connect=leader_comm(pid), leader_comm_after=None,
                  name_snapshot="unverified_nonatomic", process_path="unknown",
                  identity_capability="unverified")
    result.update(socket_identity(conn))
    require(result["remote_endpoint"] == ["127.0.0.1", args.port],
            "socket-metadata", "unexpected remote endpoint")
    return result


def exchange(conn, token):
    """Send 33 bytes + write EOF, require the same 33 bytes and peer EOF."""
    frame = token.encode("ascii") + b"\n"
    require(re.fullmatch(b"[0-9a-f]{32}\n", frame), "exchange", "invalid token frame")
    deadline = time.monotonic() + TIMEOUT

    def remaining():
        left = deadline - time.monotonic()
        require(left > 0, "exchange", "frame deadline exceeded")
        conn.settimeout(left)

    pending = frame
    while pending:
        remaining()
        count = conn.send(pending)
        require(type(count) is int and 0 < count <= len(pending), "exchange", "invalid send count")
        pending = pending[count:]
    remaining()
    conn.shutdown(socket.SHUT_WR)
    echoed = bytearray()
    while True:
        remaining()
        part = conn.recv(34 - len(echoed))
        if not part:
            break
        echoed.extend(part)
        require(len(echoed) <= 33, "exchange", "extra echo data")
    require(bytes(echoed) == frame, "exchange", "truncated or incorrect echo")
    return dict(writer_pid=os.getpid(), echo_bytes=33, echo_token=token)


def close_tcp(conn, reuse):
    errors = []
    if reuse:
        try:
            conn.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
        except OSError as exc:
            errors.append("linger: " + str(exc))
    try:
        conn.close()
    except OSError as exc:
        errors.append("close: " + str(exc))
    require(not errors, "tcp-cleanup", "; ".join(errors))


def receive_socket(channel, expected):
    """Own every received descriptor immediately; reject/close extras and truncation."""
    fds = []
    conn = None
    try:
        payload, ancillary, flags, _ = channel.recvmsg(1, socket.CMSG_SPACE(2 * array.array("i").itemsize),
                                                      socket.MSG_CMSG_CLOEXEC)
        other = False
        malformed = False
        for level, kind, data in ancillary:
            if (level, kind) != (socket.SOL_SOCKET, socket.SCM_RIGHTS):
                other = True
                continue
            values = array.array("i")
            usable = len(data) - len(data) % values.itemsize
            values.frombytes(data[:usable])
            fds.extend(values)
            malformed = malformed or usable != len(data)
        require(payload == b"F" and not (flags & (socket.MSG_CTRUNC | socket.MSG_TRUNC))
                and not other and not malformed and len(fds) == 1,
                "scm-receive", "one complete SCM_RIGHTS descriptor required")
        require(not os.get_inheritable(fds[0]), "scm-receive", "received FD lacks CLOEXEC")
        conn = socket.socket(fileno=fds[0])
        fds.clear()  # The socket object now owns the single FD.
        require(socket_identity(conn) == expected, "scm-receive", "transferred socket identity mismatch")
        return conn
    except BaseException as primary:
        errors = []
        if conn is not None:
            try:
                conn.close()
            except OSError as exc:
                errors.append(str(exc))
        for fd in fds:
            try:
                os.close(fd)
            except OSError as exc:
                errors.append(str(exc))
        if errors:
            raise ClientError("scm-cleanup", str(primary) + "; " + "; ".join(errors)) from primary
        raise


class OwnedChild:
    """Only an unreaped child returned by this invocation's fork may be signalled."""
    def __init__(self):
        self.pid = None
        self.owned = False
        self.status = None

    def poll(self):
        if not self.owned:
            return True
        try:
            pid, status = os.waitpid(self.pid, os.WNOHANG)
        except ChildProcessError as exc:
            self.owned = False  # Lost wait ownership; never signal that numeric PID.
            raise ClientError("child-wait", "owned child no longer waitable") from exc
        if pid == self.pid:
            self.owned = False
            self.status = status
            return True
        require(pid == 0, "child-wait", "unexpected waitpid result")
        return False

    def wait(self, timeout, clock=time.monotonic, sleep=time.sleep):
        deadline = clock() + timeout
        while not self.poll():
            require(clock() < deadline, "child-wait", "owned child deadline exceeded")
            sleep(0.01)
        return self.status

    def cleanup(self, clock=time.monotonic, sleep=time.sleep):
        if self.poll():
            return
        try:
            os.kill(self.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        self.wait(2, clock, sleep)


def close_fd(fds, index):
    fd, fds[index] = fds[index], None
    if fd is not None:
        os.close(fd)  # Never retry a close, including EINTR.


def child_exchange(conn, args, expected, fds, channels):
    """Child-only branch; never return into the parent's resource cleanup."""
    result, code = None, 1
    errors = []
    try:
        close_fd(fds, 0)
        if args.mode == "scm-writer":
            channels[0].close()
            conn.close()  # Close inherited TCP FD before receiving SCM_RIGHTS.
            conn = None
            conn = receive_socket(channels[1], expected)
            channels[1].close()
        require(socket_identity(conn) == expected, "child-metadata", "writer socket identity mismatch")
        result = exchange(conn, args.token)
        code = 0
    except BaseException as exc:
        result = dict(writer_pid=os.getpid(), error=str(exc)[:300],
                      stage=getattr(exc, "stage", "child-exchange"))
    finally:
        if conn is not None:
            try:
                close_tcp(conn, bool(args.source_port))
            except Exception as exc:
                errors.append(str(exc))
        for channel in channels:
            try:
                channel.close()
            except OSError as exc:
                errors.append(str(exc))
        if errors:
            result = dict(writer_pid=os.getpid(), stage="child-cleanup", error="; ".join(errors)[:300])
            code = 1
        try:
            raw = json.dumps(result, sort_keys=True).encode("ascii") + b"\n"
            require(len(raw) <= REPORT_LIMIT, "child-report", "child report exceeds bound")
            require(os.write(fds[1], raw) == len(raw), "child-report", "short child report")
        except BaseException:
            code = 1
        try:
            close_fd(fds, 1)
        finally:
            os._exit(code)


def fork_exchange(conn, args, snapshot):
    fds, channels = [None, None], []
    child = OwnedChild()  # Allocate ownership state before fork.
    primary = None
    result = None
    try:
        fds[:] = os.pipe2(os.O_CLOEXEC)
        if args.mode == "scm-writer":
            channels = list(socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM))
            for channel in channels:
                channel.settimeout(TIMEOUT)
        pid = os.fork()
        if pid == 0:
            expected = {key: snapshot[key] for key in
                        ("socket_cookie", "netns_cookie", "local_endpoint", "remote_endpoint")}
            child_exchange(conn, args, expected, fds, channels)
            raise AssertionError("child must exit")
        child.pid, child.owned = pid, True
        close_fd(fds, 1)
        if channels:
            channels[1].close()
            sent = channels[0].sendmsg([b"F"], [(socket.SOL_SOCKET, socket.SCM_RIGHTS,
                                                array.array("i", [conn.fileno()]).tobytes())])
            require(sent == 1, "scm-send", "short descriptor control message")
            channels[0].close()
        status = child.wait(8)
        raw = os.read(fds[0], REPORT_LIMIT + 1)
        require(0 < len(raw) <= REPORT_LIMIT, "child-report", "missing or oversized child report")
        result = json.loads(raw.decode("ascii"))
        require(isinstance(result, dict) and result.get("writer_pid") == pid,
                "child-report", "writer PID mismatch")
        require(os.WIFEXITED(status) and os.WEXITSTATUS(status) == 0 and "error" not in result,
                "child-exchange", result.get("error", "owned child did not exit zero"))
        require(result.get("echo_bytes") == 33 and result.get("echo_token") == args.token,
                "child-report", "invalid echo confirmation")
    except BaseException as exc:
        primary = exc
    errors = []
    try:
        child.cleanup()
    except Exception as exc:
        errors.append("owned child cleanup: " + str(exc))
    for channel in channels:
        try:
            channel.close()
        except OSError as exc:
            errors.append(str(exc))
    for index in (0, 1):
        try:
            close_fd(fds, index)
        except OSError as exc:
            errors.append(str(exc))
    if errors:
        raise ClientError("fork-cleanup", (str(primary) + "; " if primary else "") + "; ".join(errors)) from primary
    if primary is not None:
        raise primary
    return result


def run_connected(args, emit):
    conn = None
    primary = None
    result = None
    stage = "connect"
    try:
        before = leader_comm(os.getpid())
        conn = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        conn.settimeout(TIMEOUT)
        conn.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        if args.source_port:
            conn.bind(("127.0.0.1", args.source_port))
        conn.connect(("127.0.0.1", args.port))
        stage = "capture"
        snapshot = capture(conn, args, before)
        emit(dict(snapshot, event="connected"))
        stage = args.mode + "-exchange"
        result = (fork_exchange(conn, args, snapshot) if args.mode in ("fork-writer", "scm-writer")
                  else exchange(conn, args.token))
        result = dict(snapshot, **result)
        result["leader_comm_after"] = leader_comm(snapshot["pid"])
    except BaseException as exc:
        primary = exc if isinstance(exc, ClientError) else ClientError(stage, str(exc))
    if conn is not None:
        try:
            close_tcp(conn, bool(args.source_port))
        except Exception as exc:
            primary = ClientError("tcp-cleanup", (str(primary) + "; " if primary else "") + str(exc))
    if primary is not None:
        raise primary
    emit(dict(result, event="exchanged"))
    return result


def run_thread(operation):
    results, errors = [], []
    def worker():
        try:
            results.append(operation())
        except BaseException as exc:
            errors.append(exc)
    thread = threading.Thread(target=worker, daemon=True)
    thread.start()
    thread.join(8)
    require(not thread.is_alive(), "thread-join", "native worker deadline exceeded")
    if errors:
        raise errors[0]
    require(len(results) == 1, "thread-join", "missing native worker result")
    return results[0]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--allow-loopback", action="store_true")
    parser.add_argument("--mode", choices=("thread", "fork-writer", "scm-writer", "plain"), required=True)
    parser.add_argument("--generation", required=True)
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--token", required=True)
    parser.add_argument("--source-port", type=int, default=0)
    args = parser.parse_args(argv)
    try:
        require(args.allow_loopback, "preflight", "--allow-loopback required before socket operations")
        require(re.fullmatch(r"[1-9][0-9]{0,19}", args.generation)
                and int(args.generation) < 2**64, "preflight", "canonical nonzero uint64 generation required")
        require(1 <= args.port <= 65535 and 0 <= args.source_port <= 65535,
                "preflight", "invalid port")
        require(re.fullmatch(r"[0-9a-f]{32}", args.token), "preflight", "32 lowercase hex token required")
        require(sys.platform == "linux" and "hoopa_lab=t132" in Path("/proc/cmdline").read_text().split(),
                "preflight", "dedicated Linux guest required; never run on host")
        def emit(value):
            print(json.dumps(value, sort_keys=True), flush=True)
        if args.mode == "thread":
            run_thread(lambda: run_connected(args, emit))
        else:
            run_connected(args, emit)
        return 0
    except Exception as exc:
        print("online-test-client: {}: {}".format(getattr(exc, "stage", "runtime"), exc), file=sys.stderr)
        return 2 if getattr(exc, "stage", None) == "preflight" else 1


if __name__ == "__main__":
    sys.exit(main())
