#!/usr/bin/env python3
"""Finite fixture driver, launched externally into the target cgroup AFTER attach.

Never loads BPF, creates/moves cgroups, or manages an existing process. The only
children are one fixed fixture server and two fixed fixture clients. Explicit
--allow-fixture-processes is needed even in an already prepared isolated lab.
"""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import signal
import struct
import sys
import tempfile
import time

from calibration import TOKEN, exact_hex, parse_json, read_input, require, u64
from fixture_processes import ChildSet
from inspect_object import pinned_input
from observations import decode_config
from run_observer import cgroup2_fd, read_at, write_json


def validate_inputs(reg, source, marker, source_raw, now):
    require(reg["schema"] == "sourceidentity-calibration-registration-v1", "registration schema mismatch")
    tokens = reg["tokens"]
    require(isinstance(tokens, list) and len(tokens) == 2 and
            all(isinstance(t, str) and TOKEN.fullmatch(t) for t in tokens) and len(set(tokens)) == 2,
            "exactly two different registered tokens required")
    require(source["schema"] == "sourceidentity-observer-input-v1" and source["profile"] == "namespaced",
            "namespaced observer required")
    config = decode_config(exact_hex(source["config_hex"], 32))
    require(type(reg["port"]) is int and
            (config.generation, config.cgroup, config.netns, config.destination_port) ==
            (u64(reg["generation"]), u64(reg["cgroup"]), u64(reg["netns_cookie"]), reg["port"]),
            "registration/config mismatch")
    require(u64(source["cgroup"]["inode"]) == config.cgroup, "cgroup inode/config mismatch")
    require(marker["event"] == "attached_observer" and marker["generation"] == reg["generation"] and
            isinstance(source["boot_id"], str) and bool(source["boot_id"]) and
            marker["boot_id"] == source["boot_id"], "attached generation/boot mismatch")
    require(marker["input_sha256"] == hashlib.sha256(source_raw).hexdigest(), "marker input digest mismatch")
    duration = marker["duration_seconds"]
    require(type(duration) in (int, float) and math.isfinite(duration) and 0 < duration <= 60 and
            type(source["duration_seconds"]) in (int, float) and
            source["duration_seconds"] == duration, "invalid or conflicting duration")
    observed = u64(marker["observed_monotonic_ns"])
    deadline = observed + int(duration * 1000000000)
    require(observed <= now and deadline < 2**64 and deadline - now >= 14000000000,
            "marker is in the future or less than 14 seconds remain")
    u64(source["observer_pidns_inode"]); u64(source["observer_timens_inode"])
    return deadline


class Scope:
    """Read-only samples, not a lock on membership or proof of hook continuity."""
    def __init__(self, source):
        self.source, self.fd = source, None
        self.pid = os.getpid()
        self.pidns = str(os.stat("/proc/self/ns/pid").st_ino)
        self.timens = str(os.stat("/proc/self/ns/time").st_ino)
        with open("/proc/sys/kernel/random/boot_id", "rb") as stream:
            boot = stream.read(128).decode("ascii").strip()
        require(boot == source["boot_id"] and self.pidns == source["observer_pidns_inode"] and
                self.timens == source["observer_timens_inode"], "driver boot/PID/time namespace mismatch")
        path = Path(source["cgroup"]["path"])
        require(path.is_absolute(), "absolute cgroup path required")
        self.fd = os.open(str(path), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            info = os.fstat(self.fd)
            require(cgroup2_fd(self.fd) and str(info.st_ino) == source["cgroup"]["inode"] and
                    str(info.st_dev) == source["cgroup"]["device"], "target cgroup identity mismatch")
            parent = os.open("..", os.O_RDONLY | os.O_DIRECTORY, dir_fd=self.fd)
            try:
                require(cgroup2_fd(parent) and os.fstat(parent).st_ino != info.st_ino,
                        "cgroup mount root is not an allowed target")
            finally:
                os.close(parent)
            self.check({self.pid})
        except BaseException as primary:
            try:
                self.close()
            except OSError as cleanup:
                raise ValueError(str(primary) + "; scope close failed: " + str(cleanup)) from primary
            raise

    def check(self, allowed):
        require(str(os.stat("/proc/self/ns/pid").st_ino) == self.pidns and
                str(os.stat("/proc/self/ns/time").st_ino) == self.timens and
                str(os.stat("/proc/self/ns/time_for_children").st_ino) == self.timens,
                "PID/time namespace changed or children would inherit another clock")
        require(read_at(self.fd, "cgroup.type").strip() == b"domain", "domain cgroup required")
        members_raw = read_at(self.fd, "cgroup.procs").split()
        require(all(p.isdigit() for p in members_raw), "invalid cgroup member sample")
        members = {int(p) for p in members_raw}
        require(self.pid in members and members <= allowed, "driver placement or cgroup members changed")
        with os.scandir(self.fd) as entries:
            require(not any(e.is_dir(follow_symlinks=False) for e in entries), "target is not a leaf")

    def close(self):
        if self.fd is not None:
            fd, self.fd = self.fd, None
            os.close(fd)


def fixture_argv(binary, role, generation, port, token=None):
    args = [str(binary), "--role", role, "--generation", generation, "--port", str(port)]
    args.extend(["--count", "2"] if role == "serve" else ["--token", token])
    return args + ["--allow-loopback"]


def json_lines(raw):
    lines = raw.decode("utf-8").splitlines()
    require(lines and all(lines), "empty fixture JSONL")
    return [parse_json(line) for line in lines]


def listening(raw, generation, port):
    if b"\n" not in raw:
        return False
    row = parse_json(raw.split(b"\n", 1)[0].decode("utf-8"))
    require(row["schema"] == "sourceidentity-fixture-v1" and row["event"] == "listening" and
            row["generation"] == generation and type(row["port"]) is int and row["port"] == port,
            "invalid listening marker")
    return True


def validate_outputs(views, generation, port, tokens):
    require(set(views) == {"server", "client1", "client2"}, "missing owned child")
    require(len({v["pid"] for v in views.values()}) == 3, "duplicate owned child PID")
    for view in views.values():
        require(type(view["exit_code"]) is int and view["exit_code"] == 0 and
                view["stdout_eof"] and view["stderr_eof"] and not view["truncated"],
                "child failed, output incomplete or truncated")
    server = json_lines(views["server"]["stdout"])
    require(len(server) == 3 and listening(views["server"]["stdout"], generation, port), "server output incomplete")
    require({r["token"] for r in server[1:]} == set(tokens), "server token set mismatch")
    for row in server[1:]:
        require(row["schema"] == "sourceidentity-fixture-v1" and row["event"] == "connection" and
                row["role"] == "serve" and row["generation"] == generation and
                row["identity_capability"] == "unverified", "invalid server connection record")
    for i, label in enumerate(("client1", "client2")):
        rows = json_lines(views[label]["stdout"])
        require(len(rows) == 1, "client output must contain one record")
        row = rows[0]
        require(row["schema"] == "sourceidentity-fixture-v1" and row["event"] == "connection" and
                row["role"] == "client" and row["generation"] == generation and
                row["token"] == tokens[i] and row["identity_capability"] == "unverified" and
                type(row["pid"]) is int and row["pid"] == views[label]["pid"], "client/owned PID record mismatch")


def drive(group, binary, generation, port, tokens, deadline, guard=lambda: None,
          clock=time.monotonic_ns, cancelled=lambda: False):
    result = dict(status="failed", errors=[], events=[], children={})
    stage = "preflight"
    local_deadline = min(deadline, clock() + 12000000000)
    def check():
        require(not cancelled(), "fixture execution cancelled")
        require(clock() < local_deadline, "fixture execution deadline exceeded")
        guard()
        require(clock() < local_deadline, "fixture execution deadline exceeded during scope sample")
    def event(name):
        result["events"].append(dict(event=name, observed_monotonic_ns=str(clock())))
    def client_window():
        now = clock()
        require(local_deadline - now >= 10000000000 and deadline - now >= 12000000000,
                "insufficient client execution/cleanup reserve")
    try:
        check(); event("preflight_complete")
        stage = "start_server"
        # Pinning/copying inputs and even a scope sample can consume the reserve
        # validated by main. Recheck immediately before starting any traffic.
        require(deadline - clock() >= 14000000000, "less than 14 seconds remain before server spawn")
        group.spawn("server", fixture_argv(binary, "serve", generation, port)); event("server_started")
        require(deadline - clock() >= 12000000000, "insufficient reserve after server spawn")
        clients_started = False
        for _ in range(1000):
            stage = "observe_children"
            check(); group.pump(0.05)
            views = group.views()
            require(all(v["exit_code"] in (None, 0) and not v["truncated"] for v in views.values()),
                    "child failed or output truncated")
            if not clients_started:
                server = views["server"]
                require(server["exit_code"] is None, "server exited before clients started")
                if listening(server["stdout"], generation, port):
                    event("listening_observed")
                    for i, token in enumerate(tokens):
                        check(); label = "client" + str(i + 1); stage = "start_" + label
                        client_window()
                        group.spawn(label, fixture_argv(binary, "client", generation, port, token))
                        event(label + "_started")
                        client_window()
                    clients_started = True
            if clients_started and all(v["exit_code"] is not None and v["stdout_eof"] and v["stderr_eof"]
                                       for v in group.views().values()):
                check(); validate_outputs(group.views(), generation, port, tokens)
                event("children_completed"); result["status"] = "completed_unverified"
                break
        else:
            raise ValueError("fixture observation iteration bound exceeded")
    except (Exception, KeyboardInterrupt) as exc:
        result["errors"].append(dict(stage=stage, message=str(exc)))
    finally:
        try:
            result["errors"].extend(dict(stage="cleanup", message=e) for e in group.close())
        except (Exception, KeyboardInterrupt) as exc:
            result["errors"].append(dict(stage="cleanup", message=str(exc)))
        result["children"] = group.views()
        result["finished_monotonic_ns"] = str(clock())
        if int(result["finished_monotonic_ns"]) >= deadline:
            result["errors"].append(dict(stage="finish", message="observer window expired"))
    if result["errors"]:
        result["status"] = "failed"
    return result


def save_asset(directory, name, raw, mode=0o600):
    fd = os.open(str(directory / name), os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    with os.fdopen(fd, "wb") as stream:
        stream.write(raw)
    return dict(name=name, sha256=hashlib.sha256(raw).hexdigest(), bytes=len(raw))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--allow-fixture-processes", action="store_true")
    for name in ("registration", "observer-directory", "fixture", "fixture-sha256", "output-parent"):
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args(argv)
    if not args.allow_fixture_processes:
        print(json.dumps(dict(status="refused", reason="--allow-fixture-processes required")))
        return 2
    directory, scope, group = None, None, None
    handlers, cancelled = {}, [False]
    result = dict(schema="sourceidentity-fixture-run-v1", status="failed", errors=[], children={}, events=[],
                  identity_capability="unverified", association="unverified", completeness="unverified",
                  scope_isolation="unverified")
    def stop(signum, frame): cancelled[0] = True
    try:
        require(platform.system() == "Linux" and platform.machine() == "x86_64", "Linux/amd64 required")
        observer, parent = Path(args.observer_directory), Path(args.output_parent)
        require(observer.is_absolute() and parent.is_absolute() and parent.is_dir(), "absolute existing paths required")
        parent = parent.resolve(strict=True)
        raw_reg, _ = read_input(args.registration)
        raw_source, _ = read_input(observer / "input.json")
        raw_marker, _ = read_input(observer / "attached.json")
        reg, source, marker = map(parse_json, (raw_reg, raw_source, raw_marker))
        started = time.monotonic_ns()
        deadline = validate_inputs(reg, source, marker, raw_source.encode(), started)
        require(not (observer / "result.json").exists(), "observer has already stopped")
        scope = Scope(source)
        _, binary = pinned_input(args.fixture, args.fixture_sha256)
        require(len(binary) >= 64 and binary[:7] == b"\x7fELF\x02\x01\x01" and
                struct.unpack_from("<H", binary, 16)[0] in (2, 3) and
                struct.unpack_from("<H", binary, 18)[0] == 62, "Linux/amd64 ELF fixture required")
        directory = Path(tempfile.mkdtemp(prefix="hoopa-fixture-run-", dir=str(parent)))
        result.update(generation=reg["generation"], boot_id=source["boot_id"], controller_pid=scope.pid,
                      pidns_inode=scope.pidns, timens_inode=scope.timens,
                      started_monotonic_ns=str(started), deadline_monotonic_ns=str(deadline))
        artifacts = result["artifacts"] = {}
        for key, name, raw in (("registration", "registration.json", raw_reg.encode()),
                               ("observer_input", "observer-input.json", raw_source.encode()),
                               ("attached", "attached.json", raw_marker.encode()), ("fixture", "fixture", binary)):
            artifacts[key] = save_asset(directory, name, raw, 0o700 if key == "fixture" else 0o600)
        for signum in (signal.SIGINT, signal.SIGTERM): handlers[signum] = signal.signal(signum, stop)
        group = ChildSet()
        def guard():
            require(not (observer / "result.json").exists(), "observer stopped during fixture run")
            scope.check({scope.pid} | {v["pid"] for v in group.views().values()})
        result.update(drive(group, directory / "fixture", reg["generation"], reg["port"], reg["tokens"],
                            deadline, guard=guard, cancelled=lambda: cancelled[0]))
        group = None  # drive performed bounded cleanup; snapshots remain in result.
        require(hashlib.sha256((directory / "fixture").read_bytes()).hexdigest() == args.fixture_sha256,
                "private fixture changed during run")
    except (Exception, KeyboardInterrupt) as exc:
        result["errors"].append(dict(stage="runner", message=str(exc))); result["status"] = "failed"
    finally:
        if group is not None:
            result["errors"].extend(dict(stage="cleanup", message=e) for e in group.close())
            result["children"] = group.views()
        if scope is not None:
            try: scope.close()
            except OSError as exc: result["errors"].append(dict(stage="scope_close", message=str(exc)))
        for signum, handler in handlers.items(): signal.signal(signum, handler)
    if result["errors"]: result["status"] = "failed"
    if directory is not None:
        try:
            for label, child in result["children"].items():
                for stream, suffix in (("stdout", ".stdout.jsonl"), ("stderr", ".stderr.log")):
                    child[stream] = save_asset(directory, label + suffix, child[stream])
            write_json(directory / "run.json", result)
        except OSError as exc:
            result["status"] = "failed"; result["errors"].append(dict(stage="write_result", message=str(exc)))
    print(json.dumps(dict(status=result["status"], directory=str(directory) if directory else None,
                          errors=result["errors"])), flush=True)
    return 0 if result["status"] == "completed_unverified" else 1


if __name__ == "__main__": sys.exit(main())
