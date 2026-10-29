#!/usr/bin/env python3
"""Finite guest-only proxy client/oracle; never core resolver input.

Names are ordered, non-atomic observations; paths and identity remain unverified.
No proxy environment variable selects an endpoint or authentication credential.
"""
import argparse
import base64
from contextlib import contextmanager
import ctypes
import errno
import json
import os
from pathlib import Path
import re
import socket
import stat
import sys
import threading
import time

import online_test_client as helper

TIMEOUT = 3.0
USERNAME = b"client"
PASSWORD = b"entry-" + b"p" * 32  # Controlled dummy fixture credentials; never printed.
SCHEMA = "sourceidentity-core-client-v1"
MODES = ("thread", "fork-writer", "scm-writer", "rename-after-connect", "exec-fd")
SCOPE = r"/sys/fs/cgroup/hoopa-online-[A-Za-z0-9_-]+"
UUID = r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"
require = helper.require
ClientError = helper.ClientError


def hostname(value):
    return (type(value) is str and 0 < len(value) <= 253 and
            all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label)
                for label in value.split(".")))


class Handshake:
    def __init__(self, conn):
        self.conn = conn
        self.deadline = time.monotonic() + TIMEOUT

    def remaining(self):
        left = self.deadline - time.monotonic()
        require(left > 0, "proxy-handshake", "handshake deadline exceeded")
        self.conn.settimeout(left)

    def send(self, data):
        while data:
            self.remaining()
            n = self.conn.send(data)
            require(type(n) is int and 0 < n <= len(data), "proxy-send", "invalid send count")
            data = data[n:]

    def exact(self, size):
        require(0 <= size <= 4096, "proxy-reply", "reply size exceeds bound")
        result = bytearray()
        while len(result) < size:
            self.remaining()
            part = self.conn.recv(size - len(result))
            require(type(part) is bytes and 0 < len(part) <= size - len(result),
                    "proxy-reply", "truncated or oversized proxy reply")
            result.extend(part)
        return bytes(result)


def proxy_handshake(conn, args):
    require(args.protocol in ("http", "socks5") and hostname(args.target) and
            type(args.target_port) is int and 1 <= args.target_port <= 65535,
            "proxy-registration", "invalid explicit protocol/target")
    io = Handshake(conn)
    host = args.target.encode("ascii")
    if args.protocol == "http":
        authority = host + b":" + str(args.target_port).encode("ascii")
        auth = base64.b64encode(USERNAME + b":" + PASSWORD)
        io.send(b"CONNECT " + authority + b" HTTP/1.1\r\nHost: " + authority +
                b"\r\nProxy-Authorization: Basic " + auth + b"\r\n\r\n")
        header = bytearray()
        while not header.endswith(b"\r\n\r\n"):
            require(len(header) < 4096, "http-reply", "HTTP header exceeds bound")
            header.extend(io.exact(1))
        lines = bytes(header[:-4]).split(b"\r\n")
        require(re.fullmatch(rb"HTTP/1\.[01] 200(?: [\x20-\x7e]*)?", lines[0]),
                "http-reply", "HTTP CONNECT did not return a valid 200 status")
        seen = set()
        for line in lines[1:]:
            key, sep, value = line.partition(b":")
            require(sep and re.fullmatch(rb"[!#$%&'*+.^_`|~0-9A-Za-z-]+", key) and
                    all(byte == 9 or 32 <= byte <= 126 for byte in value),
                    "http-reply", "invalid HTTP response header")
            key, value = key.lower(), value.strip()
            require(key not in seen and key != b"transfer-encoding" and
                    (key != b"content-length" or value == b"0"),
                    "http-reply", "duplicate or body-bearing CONNECT reply")
            seen.add(key)
    else:
        io.send(b"\x05\x01\x02")
        require(io.exact(2) == b"\x05\x02", "socks-method", "authenticated SOCKS5 method required")
        io.send(b"\x01" + bytes([len(USERNAME)]) + USERNAME + bytes([len(PASSWORD)]) + PASSWORD)
        require(io.exact(2) == b"\x01\x00", "socks-auth", "SOCKS5 fixture authentication rejected")
        io.send(b"\x05\x01\x00\x03" + bytes([len(host)]) + host + args.target_port.to_bytes(2, "big"))
        reply = io.exact(4)
        require(reply[:3] == b"\x05\x00\x00", "socks-connect", "SOCKS5 CONNECT rejected or malformed")
        if reply[3] == 1:
            io.exact(6)
        elif reply[3] == 4:
            io.exact(18)
        elif reply[3] == 3:
            size = io.exact(1)[0]
            require(0 < size <= 253, "socks-connect", "invalid bound domain length")
            bound = io.exact(size)
            require(hostname(bound.decode("ascii", "replace")), "socks-connect", "invalid bound domain")
            io.exact(2)
        else:
            raise ClientError("socks-connect", "unsupported bound address type")
    return dict(proxy_ready=True, protocol=args.protocol, target=args.target, target_port=args.target_port)


@contextmanager
def proxy_exchange(args, original_fd=None):
    """Only this controlled invocation substitutes the child's payload operation."""
    previous = helper.exchange
    previous_receive = helper.receive_socket
    scm = {}
    def receive(channel, expected):
        require(type(original_fd) is int and original_fd >= 3,
                "scm-oracle", "known original socket FD required")
        try:
            os.fstat(original_fd)
        except OSError as exc:
            require(exc.errno == errno.EBADF, "scm-oracle", "original FD state not closed")
        else:
            raise ClientError("scm-oracle", "inherited socket FD still open before receive")
        # In the pinned helper no descriptor-allocating operation intervenes
        # between inherited TCP close and this check; child has only one task.
        received = previous_receive(channel, expected)
        try:
            actual = helper.socket_identity(received)
            scm.update(scm_inherited_fd_closed_before_receive=True,
                       scm_original_fd=original_fd, scm_received_fd=received.fileno(),
                       scm_received_socket_cookie=actual["socket_cookie"],
                       scm_received_netns_cookie=actual["netns_cookie"],
                       scm_received_local_endpoint=actual["local_endpoint"],
                       scm_received_remote_endpoint=actual["remote_endpoint"])
        except BaseException:
            received.close()
            raise
        return received
    def exchange(conn, token):
        ready = proxy_handshake(conn, args)
        result = previous(conn, token)
        result.update(ready)
        if args.mode == "scm-writer":
            require(scm.get("scm_inherited_fd_closed_before_receive") is True,
                    "scm-oracle", "missing real transfer observation")
            result.update(scm)
        return result
    helper.exchange = exchange
    if args.mode == "scm-writer":
        helper.receive_socket = receive
    try:
        yield
    finally:
        helper.exchange = previous
        helper.receive_socket = previous_receive


def caller_comm():
    with open("/proc/self/task/{}/comm".format(threading.get_native_id()), "rb") as stream:
        raw = stream.read(65)
    require(0 < len(raw) <= 16 and raw.endswith(b"\n"), "capture", "bounded caller comm required")
    name = raw[:-1]
    require(len(name) <= 15, "capture", "caller comm exceeds TASK_COMM_LEN")
    try:
        text = name.decode("utf-8")
    except UnicodeError:
        text = None
    return dict(raw_hex=(name + bytes(16-len(name))).hex(), text=text, status="unverified")


def set_name(name):
    require(name in ("hoopa-before", "hoopa-after"), "rename", "unexpected controlled comm")
    library = ctypes.CDLL(None, use_errno=True)
    function = library.prctl
    function.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_ulong, ctypes.c_ulong, ctypes.c_ulong]
    function.restype = ctypes.c_int
    raw = ctypes.create_string_buffer(name.encode("ascii"), 16)
    require(function(15, ctypes.cast(raw, ctypes.c_void_p), 0, 0, 0) == 0,
            "rename", "PR_SET_NAME failed errno={}".format(ctypes.get_errno()))
    require(helper.leader_comm(os.getpid()) == name, "rename", "controlled leader comm mismatch")


def registration(args):
    return {name: getattr(args, name) for name in
            ("mode", "generation", "port", "protocol", "target", "target_port", "token", "scope")}


def snapshot(conn, args, before, boot_before, boot_after):
    result = helper.capture(conn, args, before)
    result.update(schema=SCHEMA, port=args.port, protocol=args.protocol, target=args.target,
                  target_port=args.target_port, scope=args.scope,
                  boot_id=Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
                  connect_boot_before_ns=str(boot_before), connect_boot_after_ns=str(boot_after),
                  caller_comm_connect=caller_comm())
    require(re.fullmatch(UUID, result["boot_id"]), "capture", "invalid boot ID")
    return result


def private_directory(path, empty):
    require(type(path) is str and Path(path).is_absolute() and str(Path(path)) == path
            and ".." not in Path(path).parts and path != "/", "oracle", "canonical absolute oracle directory required")
    info = Path(path).lstat()
    require(stat.S_ISDIR(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o700
            and info.st_uid == os.getuid(), "oracle", "owned private 0700 directory required")
    if empty:
        require(not list(Path(path).iterdir()), "oracle", "oracle directory must be empty")
    return Path(path)


def write_oracle(args, record, fd):
    directory = private_directory(args.oracle_output, True)
    data = dict(schema="sourceidentity-core-client-oracle-v1", registration=registration(args),
                snapshot=record, socket_fd=fd)
    raw = (json.dumps(data, sort_keys=True, separators=(",", ":")) + "\n").encode("ascii")
    require(len(raw) <= 8192, "oracle", "oracle exceeds bound")
    path = directory / "connector.json"
    descriptor = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        require(stream.write(raw) == len(raw), "oracle", "short oracle write")
        stream.flush()
    return str(path)


def no_extra_inheritance(fd):
    require(threading.active_count() == 1 and len(list(Path("/proc/self/task").iterdir())) == 1,
            "exec-preflight", "exec requires a single task")
    for name in os.listdir("/proc/self/fd"):
        require(name.isdecimal(), "exec-preflight", "invalid proc FD name")
        other = int(name)
        if other <= 2 or other == fd:
            continue
        try:
            inherited = os.get_inheritable(other)
        except OSError as exc:
            if exc.errno == errno.EBADF:
                continue  # The proc-list directory FD is already closed; never close/retry it.
            raise
        require(not inherited, "exec-preflight", "unrelated inheritable descriptor present")


def resume_argv(args, fd, oracle):
    result = [sys.executable, "-S", "-B", str(Path(__file__).resolve()), "--allow-loopback"]
    for name, value in registration(args).items():
        result.extend(("--" + name.replace("_", "-"), str(value)))
    result.extend(("--oracle-output", args.oracle_output, "--resume-oracle", oracle, "--resume-fd", str(fd)))
    return result


def exec_socket(conn, args, record):
    fd = conn.fileno()
    require(type(fd) is int and fd >= 3, "exec-preflight", "dedicated socket FD required")
    require(not os.get_inheritable(fd), "exec-preflight", "original socket must be CLOEXEC")
    oracle = write_oracle(args, record, fd)
    no_extra_inheritance(fd)
    require(Path(sys.executable).is_absolute(), "exec-preflight", "absolute Python executable required")
    primary = None
    try:
        os.set_inheritable(fd, True)
        os.execve(sys.executable, resume_argv(args, fd, oracle), dict(os.environ))
        raise ClientError("exec", "execve unexpectedly returned")
    except BaseException as exc:
        primary = exc
    try:
        os.set_inheritable(fd, False)
    except OSError as exc:
        raise ClientError("exec-cleanup", str(primary) + "; restore CLOEXEC: " + str(exc)) from primary
    raise primary


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "resume-oracle", "duplicate oracle key")
        result[key] = value
    return result


SNAPSHOT_FIELDS = frozenset(("schema", "mode", "token", "generation", "pid", "tgid", "connect_tid",
    "start_ticks", "pidns_inode", "leader_comm_before", "leader_comm_connect", "leader_comm_after",
    "name_snapshot", "process_path", "identity_capability", "socket_cookie", "netns_cookie",
    "local_endpoint", "remote_endpoint", "port", "protocol", "target", "target_port", "scope", "boot_id",
    "connect_boot_before_ns", "connect_boot_after_ns", "caller_comm_connect"))


def read_oracle(args):
    """No socket ownership or operations before complete record/PID validation."""
    directory = private_directory(args.oracle_output, False)
    path = directory / "connector.json"
    require(args.resume_oracle == str(path) and set(p.name for p in directory.iterdir()) == {path.name},
            "resume-oracle", "only the registered oracle is allowed")
    descriptor = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o600 and
                info.st_uid == os.getuid() and 0 < info.st_size <= 8192,
                "resume-oracle", "owned regular 0600 bounded oracle required")
        raw = stream.read(8193)
    require(len(raw) <= 8192, "resume-oracle", "oracle exceeds bound")
    value = json.loads(raw.decode("ascii"), object_pairs_hook=unique_object)
    expected_registration = registration(args)
    require(type(value) is dict and set(value) == {"schema", "registration", "snapshot", "socket_fd"} and
            value["schema"] == "sourceidentity-core-client-oracle-v1" and
            type(value["registration"]) is dict and set(value["registration"]) == set(expected_registration) and
            all(type(value["registration"][key]) is type(expected) and value["registration"][key] == expected
                for key, expected in expected_registration.items()), "resume-oracle", "oracle registration mismatch")
    record = value["snapshot"]
    require(type(record) is dict and set(record) == SNAPSHOT_FIELDS and record["schema"] == SCHEMA,
            "resume-oracle", "exact connector snapshot required")
    for key, expected in registration(args).items():
        require(type(record.get(key)) is type(expected) and record[key] == expected,
                "resume-oracle", "snapshot registration mismatch")
    for key in ("pid", "tgid", "connect_tid"):
        require(type(record[key]) is int and 0 < record[key] < 2**31, "resume-oracle", "invalid process coordinates")
    require(record["pid"] == record["tgid"] == record["connect_tid"] == os.getpid(),
            "resume-oracle", "exec changed original PID/TID")
    for key in ("socket_cookie", "netns_cookie", "pidns_inode"):
        require(type(record[key]) is str and re.fullmatch(r"[1-9][0-9]{0,19}", record[key]) and
                int(record[key]) < 2**64, "resume-oracle", "invalid socket/namespace coordinates")
    require(type(record["start_ticks"]) is str and re.fullmatch(r"0|[1-9][0-9]{0,19}", record["start_ticks"])
            and int(record["start_ticks"]) < 2**64, "resume-oracle", "invalid start ticks")
    require(record["start_ticks"] == helper.start_ticks(Path("/proc/self/stat").read_text(), os.getpid()) and
            record["pidns_inode"] == str(os.stat("/proc/self/ns/pid").st_ino) and
            record["boot_id"] == Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
            "resume-oracle", "exec changed startup/namespace/boot coordinates")
    require(re.fullmatch(UUID, record["boot_id"]) and
            record["leader_comm_before"] == record["leader_comm_connect"] == "hoopa-before" and
            record["leader_comm_after"] is None and record["identity_capability"] == "unverified" and
            record["name_snapshot"] == "unverified_nonatomic" and record["process_path"] == "unknown",
            "resume-oracle", "invalid snapshot capability/name fields")
    for key in ("connect_boot_before_ns", "connect_boot_after_ns"):
        require(type(record[key]) is str and re.fullmatch(r"[1-9][0-9]{0,19}", record[key]) and
                int(record[key]) < 2**64, "resume-oracle", "invalid BOOT timestamp")
    require(int(record["connect_boot_before_ns"]) <= int(record["connect_boot_after_ns"]),
            "resume-oracle", "reversed BOOT interval")
    comm = record["caller_comm_connect"]
    require(type(comm) is dict and set(comm) == {"raw_hex", "text", "status"} and
            comm["status"] == "unverified" and type(comm["raw_hex"]) is str and
            re.fullmatch(r"[0-9a-f]{32}", comm["raw_hex"]), "resume-oracle", "invalid caller comm")
    raw_comm = bytes.fromhex(comm["raw_hex"])
    require(raw_comm == b"hoopa-before" + bytes(4) and comm["text"] == "hoopa-before",
            "resume-oracle", "exec original caller name mismatch")
    local, remote = record["local_endpoint"], record["remote_endpoint"]
    require(type(local) is list and len(local) == 2 and local[0] == "127.0.0.1" and
            type(local[1]) is int and 1 <= local[1] <= 65535 and remote == ["127.0.0.1", args.port] and
            type(remote) is list and type(remote[1]) is int,
            "resume-oracle", "invalid original endpoints")
    require(type(value["socket_fd"]) is int and 3 <= value["socket_fd"] < 2**31 and
            value["socket_fd"] == args.resume_fd, "resume-oracle", "explicit FD does not match oracle")
    return record


def run_resume(args, emit):
    record = read_oracle(args)
    conn, raw_fd = None, args.resume_fd  # Ownership begins only after full validation above.
    primary, result = None, None
    try:
        no_extra_inheritance(raw_fd)
        os.set_inheritable(raw_fd, False)
        conn = socket.socket(socket.AF_INET, socket.SOCK_STREAM, fileno=raw_fd)
        raw_fd = None
        conn.settimeout(TIMEOUT)
        expected = {key: record[key] for key in
                    ("socket_cookie", "netns_cookie", "local_endpoint", "remote_endpoint")}
        actual_socket = helper.socket_identity(conn)
        require(actual_socket == expected, "resume-socket", "exec socket identity changed")
        resume_ticks = helper.start_ticks(Path("/proc/self/stat").read_text(), os.getpid())
        require(resume_ticks == record["start_ticks"], "resume-socket", "resume startup ticks changed")
        resume_comm = helper.leader_comm(os.getpid())
        with proxy_exchange(args):
            result = helper.exchange(conn, args.token)
        result = dict(record, **result)
        result.update(resume_pid=os.getpid(), resume_tid=threading.get_native_id(),
                      resume_start_ticks=resume_ticks, resume_socket_identity_matched=True,
                      resume_socket_cookie=actual_socket["socket_cookie"],
                      resume_netns_cookie=actual_socket["netns_cookie"],
                      resume_local_endpoint=actual_socket["local_endpoint"],
                      resume_remote_endpoint=actual_socket["remote_endpoint"])
        result["leader_comm_resume"] = resume_comm
        result["leader_comm_after"] = helper.leader_comm(record["pid"])
    except BaseException as exc:
        primary = exc
    try:
        if conn is not None:
            helper.close_tcp(conn, False)
        elif raw_fd is not None:
            os.close(raw_fd)
    except OSError as exc:
        primary = ClientError("resume-cleanup", (str(primary) + "; " if primary else "") + str(exc))
    except ClientError as exc:
        primary = ClientError("resume-cleanup", (str(primary) + "; " if primary else "") + str(exc))
    if primary is not None:
        raise primary
    emit(dict(result, event="exchanged"))
    return result


def run_connected(args, emit):
    conn, primary, result = None, None, None
    stage = "connect"
    try:
        if args.mode in ("rename-after-connect", "exec-fd"):
            set_name("hoopa-before")
        before = helper.leader_comm(os.getpid())
        conn = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        conn.settimeout(TIMEOUT)
        conn.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        boot_before = time.clock_gettime_ns(time.CLOCK_BOOTTIME)
        conn.connect(("127.0.0.1", args.port))
        boot_after = time.clock_gettime_ns(time.CLOCK_BOOTTIME)
        stage = "capture"
        record = snapshot(conn, args, before, boot_before, boot_after)
        emit(dict(record, event="connected"))
        if args.mode == "exec-fd":
            stage = "exec"
            exec_socket(conn, args, record)  # Success replaces the process; never returns.
        if args.oracle_output:
            write_oracle(args, record, conn.fileno())
        if args.mode == "rename-after-connect":
            set_name("hoopa-after")
        stage = args.mode + "-exchange"
        with proxy_exchange(args, conn.fileno()):
            result = (helper.fork_exchange(conn, args, record) if args.mode in ("fork-writer", "scm-writer")
                      else helper.exchange(conn, args.token))
        result = dict(record, **result)
        result["leader_comm_after"] = helper.leader_comm(record["pid"])
    except BaseException as exc:
        primary = exc if isinstance(exc, ClientError) else ClientError(stage, str(exc))
    if conn is not None:
        try:
            helper.close_tcp(conn, False)
        except Exception as exc:
            primary = ClientError("tcp-cleanup", (str(primary) + "; " if primary else "") + str(exc))
    if primary is not None:
        raise primary
    emit(dict(result, event="exchanged"))
    return result


def run_thread(operation):
    results, errors = [], []
    def worker():
        try: results.append(operation())
        except BaseException as exc: errors.append(exc)
    thread = threading.Thread(target=worker, daemon=True)
    thread.start()
    thread.join(12)  # connect <=3s, handshake <=3s, payload <=3s, cleanup slack.
    require(not thread.is_alive(), "thread-join", "native worker deadline exceeded")
    if errors: raise errors[0]
    require(len(results) == 1, "thread-join", "missing native worker result")
    return results[0]


def preflight(args):
    require(args.allow_loopback, "preflight", "--allow-loopback required before any inputs/socket")
    require(re.fullmatch(r"[1-9][0-9]{0,19}", args.generation) and int(args.generation) < 2**64 and
            re.fullmatch(r"[0-9a-f]{32}", args.token), "preflight", "canonical generation/token required")
    require(1 <= args.port <= 65535 and 1 <= args.target_port <= 65535 and hostname(args.target),
            "preflight", "explicit registered proxy/target required")
    require(re.fullmatch(SCOPE, args.scope), "preflight", "direct canonical guest source leaf required")
    require(sys.platform == "linux" and "hoopa_lab=t132" in Path("/proc/cmdline").read_text().split(),
            "preflight", "dedicated Linux guest required")
    scope = Path(args.scope)
    info = scope.lstat()
    require(stat.S_ISDIR(info.st_mode) and (scope/"cgroup.type").read_text().strip() == "domain",
            "preflight", "direct domain source leaf required")
    require((args.resume_fd is None) == (args.resume_oracle is None) and
            (args.resume_fd is None or args.mode == "exec-fd") and
            (args.mode != "exec-fd" or args.oracle_output), "preflight", "invalid exec resume registration")
    if args.oracle_output:
        private_directory(args.oracle_output, args.resume_fd is None)
    if args.resume_fd is None:
        require(Path("/proc/self/cgroup").read_text().strip() == "0::/", "preflight", "start at guest root")
        (scope/"cgroup.procs").write_text(str(os.getpid()))
    require(Path("/proc/self/cgroup").read_text().strip() == "0::/" + scope.name,
            "preflight", "client source placement mismatch")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--allow-loopback", action="store_true")
    parser.add_argument("--mode", choices=MODES, required=True)
    for name in ("generation", "token", "scope", "target"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--protocol", choices=("http", "socks5"), required=True)
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--target-port", type=int, required=True)
    parser.add_argument("--oracle-output")
    parser.add_argument("--resume-oracle", help=argparse.SUPPRESS)
    parser.add_argument("--resume-fd", type=int, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    args.source_port = 0  # Required by the unchanged bounded fork/SCM helper.
    try:
        preflight(args)
        def emit(value): print(json.dumps(value, sort_keys=True), flush=True)
        if args.resume_fd is not None:
            run_resume(args, emit)
        elif args.mode == "thread":
            run_thread(lambda: run_connected(args, emit))
        else:
            run_connected(args, emit)
        return 0
    except Exception as exc:
        print("core-test-client: {}: {}".format(getattr(exc, "stage", "runtime"), exc), file=sys.stderr)
        return 2 if getattr(exc, "stage", None) == "preflight" else 1


if __name__ == "__main__":
    sys.exit(main())
