#!/usr/bin/env python3
"""Finite guest-only TCP admission experiment, never a production proxy.

One process owns the original sockets and both BPF links. No witness supplied by
the client, observer marker, or client token can authorize application I/O.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import socket
import struct
import sys
import time

from native_loader import NativeBackend
from observations import decode_config, decode_entry


class AdmissionError(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise AdmissionError(message)


class AdmissionOwner:
    """Single-threaded owner. No live FD transfer or parallel shutdown API."""
    def __init__(self, backend, config_raw, root_id, emit, cancelled=lambda: False):
        self.backend = backend
        self.config_raw = config_raw
        self.config = decode_config(config_raw)
        self.root_id = root_id
        self.emit = emit
        self.cancelled = cancelled
        # Reserve ownership slots before creating FDs. There must be no list
        # allocation between receiving a link FD and registering its cleanup.
        self.links = [["hoopa_connect", None], ["hoopa_lifecycle", None]]
        self.state = "new"
        self.records = []

    def start(self, root_fd):
        require(self.state == "new", "owner already started")
        self.backend.open_object()
        self.backend.load_object()
        objects = self.backend.verify_loaded()
        self.backend.initialize_config(self.config_raw)
        self.backend.freeze_config(self.config_raw)
        for entry in self.links:
            name = entry[0]
            require(not self.cancelled(), "cancelled during attachment")
            fd = self.backend.create_link(name, root_fd)
            # Register immediately; signal handlers only set a boolean.
            entry[1] = fd
            self.backend.verify_link(fd, name, self.root_id)
        self.state = "ready"
        return objects

    def revoke(self):
        if self.state != "closed":
            self.state = "revoked"

    def close(self):
        self.revoke()
        result = {"writer_stop": None, "faults_hex": None, "errors": []}
        if any(fd is not None for _, fd in self.links):
            try:
                result["writer_stop"] = self.backend.stop_writers()
                result["faults_hex"] = self.backend._lookup("capture_faults", bytes(4)).hex()
            except Exception as exc:
                result["errors"].append("stop: " + str(exc))
        for name, fd in reversed(self.links):
            if fd is None or fd not in self.backend.owned_links:
                continue  # only the explicit negative injection can remove one
            try:
                self.backend.close_link(fd)
            except Exception as exc:
                result["errors"].append("close " + name + ": " + str(exc))
        self.links.clear()
        try:
            self.backend.close_object()
        except Exception as exc:
            result["errors"].append("close object: " + str(exc))
        self.state = "closed"
        return result

    def _admit(self, conn, injected_config=None):
        require(self.state == "ready" and not self.cancelled(), "admission revoked")
        require(len(self.links) == 2 and len(self.backend.owned_links) == 2,
                "admission links missing")
        links = [self.backend.verify_link(fd, name, self.root_id) for name, fd in self.links]
        require(conn.family == socket.AF_INET and
                conn.getsockopt(socket.SOL_SOCKET, socket.SO_TYPE) == socket.SOCK_STREAM,
                "original socket must be IPv4 TCP")
        cookie = struct.unpack("=Q", conn.getsockopt(socket.SOL_SOCKET, 57, 8))[0]
        netns = struct.unpack("=Q", conn.getsockopt(socket.SOL_SOCKET, 71, 8))[0]
        local, peer = conn.getsockname(), conn.getpeername()
        require(cookie != 0 and netns == self.config.netns and
                local == ("127.0.0.1", self.config.destination_port) and
                peer[0] == "127.0.0.1" and 1 <= peer[1] <= 65535,
                "original socket namespace or endpoints mismatch")
        raw = self.backend.online_lookup(str(cookie), injected_config or self.config_raw)
        binding = decode_entry("online_bindings", struct.pack("<Q", cookie),
                               raw["binding"], self.config, profile="online")
        require(binding["client_port"] == peer[1] and
                binding["server_port"] == local[1] and
                binding["client_cookie"] != str(cookie), "original socket binding mismatch")
        connector = decode_entry("connect_records", struct.pack("<Q", int(binding["client_cookie"])),
                                 raw["connect"], self.config, profile="online")
        require(not self.cancelled(), "cancelled before admission grant")
        return {"binding": binding, "connector": connector, "link_checks": links,
                "raw": {key: value.hex() for key, value in raw.items()},
                "witness": {"accepted_cookie": str(cookie), "netns_cookie": str(netns),
                            "local_endpoint": list(local), "remote_endpoint": list(peer)}}

    def serve_one(self, listener, inject="none"):
        # The only accepted connection source is this owner's listener.
        conn, _ = listener.accept()
        record = {"schema": "sourceidentity-online-connection-v1", "status": "rejected",
                  "identity_capability": "unverified", "accepted_monotonic_ns": str(time.monotonic_ns()),
                  "grant_monotonic_ns": None, "first_read_monotonic_ns": None,
                  "first_write_monotonic_ns": None, "application_read_bytes": 0,
                  "application_write_bytes": 0, "token": None, "identity": None,
                  "injection": inject, "errors": []}
        try:
            conn.settimeout(3)
            injected_config = None
            # Deliberate test faults after actual accept and before Q. They do
            # not exist on the normal owner/shutdown path.
            if inject == "stop-before-query":
                record["injected_stop"] = self.backend.stop_writers()
            elif inject in ("close-connect-before-query", "close-lifecycle-before-query"):
                index = 0 if inject == "close-connect-before-query" else 1
                self.backend.close_link(self.links[index][1])
            elif inject == "wrong-config-before-query":
                generation = self.config.generation ^ 1
                if generation == 0:
                    generation = 2
                injected_config = struct.pack("<QQQII", generation, self.config.cgroup,
                                              self.config.netns, self.config.destination_port, 0)
            else:
                require(inject == "none", "unknown injection")
            record["identity"] = self._admit(conn, injected_config)
            record["grant_monotonic_ns"] = str(time.monotonic_ns())
            # All client-provided content is read strictly AFTER this grant.
            record["status"] = "admitted"
            frame = bytearray()
            while len(frame) < 34:
                if record["first_read_monotonic_ns"] is None:
                    record["first_read_monotonic_ns"] = str(time.monotonic_ns())
                part = conn.recv(34 - len(frame))
                record["application_read_bytes"] += len(part)
                if not part:
                    break
                frame.extend(part)
            require(re.fullmatch(b"[0-9a-f]{32}\n", bytes(frame)) is not None,
                    "invalid token frame or missing EOF")
            record["token"] = bytes(frame[:-1]).decode("ascii")
            pending = bytes(frame)
            while pending:
                if record["first_write_monotonic_ns"] is None:
                    record["first_write_monotonic_ns"] = str(time.monotonic_ns())
                count = conn.send(pending)
                require(0 < count <= len(pending), "invalid application write count")
                record["application_write_bytes"] += count
                pending = pending[count:]
            conn.shutdown(socket.SHUT_WR)
            record["status"] = "exchanged"
        except Exception as exc:
            record["errors"].append(str(exc))
            if record["grant_monotonic_ns"] is None:
                self.revoke()
            else:
                record["status"] = "admitted_exchange_failed"
        finally:
            try:
                conn.close()
            except Exception as exc:
                record["errors"].append("close accepted socket: " + str(exc))
                self.revoke()
            record["finished_monotonic_ns"] = str(time.monotonic_ns())
            self.records.append(record)
            self.emit(record)
        return record


def pinned(path, expected, limit):
    require(Path(path).is_absolute() and re.fullmatch(r"[0-9a-f]{64}", expected),
            "absolute pinned path and sha256 required")
    with open(path, "rb") as stream:
        data = stream.read(limit + 1)
    require(len(data) <= limit and hashlib.sha256(data).hexdigest() == expected,
            "pinned input size/hash mismatch")
    return data


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--allow-bpf-load", action="store_true")
    parser.add_argument("--object", required=True)
    parser.add_argument("--object-sha256", required=True)
    parser.add_argument("--libbpf", required=True)
    parser.add_argument("--libbpf-sha256", required=True)
    parser.add_argument("--scope", required=True)
    parser.add_argument("--generation", type=int, required=True)
    parser.add_argument("--netns-cookie", type=int, required=True)
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--connections", type=int, default=2)
    parser.add_argument("--seconds", type=float, default=8)
    parser.add_argument("--output", required=True)
    parser.add_argument("--inject", choices=("none", "stop-before-query", "close-connect-before-query",
                        "close-lifecycle-before-query", "wrong-config-before-query"), default="none")
    args = parser.parse_args(argv)
    if not args.allow_bpf_load:
        parser.error("--allow-bpf-load is required before any input or socket operation")
    require(os.geteuid() == 0 and "hoopa_lab=t132" in Path("/proc/cmdline").read_text().split(),
            "dedicated privileged guest required; never run on host")
    require(1 <= args.connections <= 8 and 0 < args.seconds <= 15, "invalid finite run bound")
    require(0 < args.generation < 2**64 and 0 < args.netns_cookie < 2**64 and
            0 < args.port <= 65535, "invalid registration")
    root = Path("/sys/fs/cgroup")
    scope = Path(args.scope)
    require(scope.is_absolute() and scope.parent == root and scope.name.startswith("hoopa-online-"),
            "dedicated direct guest cgroup leaf required")
    require(not scope.is_symlink() and (scope / "cgroup.type").read_text().strip() == "domain" and
            not (scope / "cgroup.procs").read_text().strip(), "scope must be empty domain leaf")
    require(Path("/proc/self/cgroup").read_text().strip() == "0::/", "start outside leaf at guest root")
    mounts = [line for line in Path("/proc/self/mountinfo").read_text().splitlines()
              if line.split()[4] == str(root)]
    require(len(mounts) == 1 and mounts[0].split(" - ")[1].split()[0] == "cgroup2", "cgroup2 root required")
    for namespace in ("pid", "net", "user", "time", "time_for_children"):
        require(os.stat("/proc/self/ns/" + namespace).st_ino == os.stat("/proc/1/ns/" + namespace).st_ino,
                "initial guest namespace required: " + namespace)
    obj = pinned(args.object, args.object_sha256, 64 * 1024 * 1024)
    pinned(args.libbpf, args.libbpf_sha256, 32 * 1024 * 1024)
    output = Path(args.output)
    require(output.is_absolute(), "absolute fresh output directory required")
    output.mkdir(mode=0o700)
    def save(name, value):
        fd = os.open(str(output / name), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, sort_keys=True, indent=2)
            stream.write("\n")
    stopped = [False]
    def stop_requested(signum, frame):
        stopped[0] = True
    signal.signal(signal.SIGTERM, stop_requested)
    signal.signal(signal.SIGINT, stop_requested)
    config_raw = struct.pack("<QQQII", args.generation, scope.stat().st_ino,
                             args.netns_cookie, args.port, 0)
    save("input.json", {"schema": "sourceidentity-online-input-v1", "argv": vars(args),
                        "pid": os.getpid(), "config_hex": config_raw.hex(),
                        "root_cgroup_id": str(root.stat().st_ino), "cgroup_mount": mounts[0]})
    events_fd = os.open(str(output / "connections.jsonl"), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    owner = None
    listener = None
    root_fd = None
    result = {"schema": "sourceidentity-online-session-v1", "status": "failed", "errors": [],
              "identity_capability": "unverified", "cleanup": None, "accepted": 0}
    with os.fdopen(events_fd, "w", buffering=1) as events:
        try:
            backend = NativeBackend(args.libbpf, obj, profile="online")
            owner = AdmissionOwner(backend, config_raw, root.stat().st_ino,
                                   lambda value: events.write(json.dumps(value, sort_keys=True) + "\n"),
                                   cancelled=lambda: stopped[0])
            root_fd = os.open(str(root), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
            result["kernel_objects"] = owner.start(root_fd)
            require(not (scope / "cgroup.procs").read_text().strip(), "scope changed during preparation")
            (scope / "cgroup.procs").write_text(str(os.getpid()))
            require(Path("/proc/self/cgroup").read_text().strip() == "0::/" + scope.name,
                    "server placement mismatch")
            listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            listener.bind(("127.0.0.1", args.port))
            listener.listen(8)
            listener.settimeout(0.1)
            require(not stopped[0], "cancelled before ready")
            save("ready.json", {"schema": "sourceidentity-online-ready-v1", "pid": os.getpid(),
                                "generation": str(args.generation), "port": args.port,
                                "source_cgroup_id": str(scope.stat().st_ino),
                                "root_cgroup_id": str(root.stat().st_ino),
                                "monotonic_ns": str(time.monotonic_ns()), "identity_capability": "unverified"})
            deadline = time.monotonic() + args.seconds
            while len(owner.records) < args.connections and time.monotonic() < deadline and not stopped[0]:
                try:
                    owner.serve_one(listener, inject=args.inject)
                except socket.timeout:
                    continue
                if owner.state != "ready":
                    break
            result["status"] = "bounded_session_finished"
            result["accepted"] = len(owner.records)
            result["cancelled"] = stopped[0]
        except Exception as exc:
            result["errors"].append(str(exc))
        finally:
            if owner is not None:
                owner.revoke()
            if listener is not None:
                try:
                    listener.close()
                except Exception as exc:
                    result["errors"].append("listener close: " + str(exc))
            if owner is not None:
                result["cleanup"] = owner.close()
                result["errors"].extend(result["cleanup"]["errors"])
            if root_fd is not None:
                try:
                    os.close(root_fd)
                except Exception as exc:
                    result["errors"].append("root cgroup close: " + str(exc))
            if result["errors"]:
                result["status"] = "failed"
            save("result.json", result)
    return 1 if result["status"] == "failed" else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
