#!/usr/bin/env python3
"""Explicit, finite T132 observation runner for a separately prepared Linux lab.

This command can load BPF only with --allow-bpf-load. It never creates cgroups,
moves processes, launches clients, pins objects, or declares identity verified.
"""
import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import struct
import sys
import tempfile
import time

sys.dont_write_bytecode = True
from inspect_object import pinned_input
from loader_session import observe, error_record
from native_loader import NativeBackend

CGROUP2_SUPER_MAGIC = 0x63677270


def cgroup2_fd(fd):
    # This profile is Linux/x86_64 only: the first struct statfs field is a long.
    libc = ctypes.CDLL(None, use_errno=True)
    function = libc.fstatfs
    function.argtypes = [ctypes.c_int, ctypes.c_void_p]
    function.restype = ctypes.c_int
    buffer = ctypes.create_string_buffer(256)
    if function(fd, buffer) != 0:
        raise OSError(ctypes.get_errno(), "fstatfs failed")
    return struct.unpack_from("<q", buffer)[0] == CGROUP2_SUPER_MAGIC


def read_at(fd, name):
    item = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)
    with os.fdopen(item, "rb") as stream:
        value = stream.read(65537)
    if len(value) > 65536:
        raise ValueError("cgroup metadata exceeds bound")
    return value


def check_empty_leaf(fd):
    if read_at(fd, "cgroup.type").strip() != b"domain":
        raise ValueError("a domain cgroup is required")
    if read_at(fd, "cgroup.procs").strip():
        raise ValueError("cgroup must remain empty until attached_observer")
    with os.scandir(fd) as entries:
        for entry in entries:
            if entry.is_dir(follow_symlinks=False):
                raise ValueError("cgroup must have no child cgroups")


def open_target(value):
    path = Path(value)
    if not path.is_absolute():
        raise ValueError("absolute cgroup path required")
    path = path.resolve(strict=True)
    fd = os.open(str(path), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        if not cgroup2_fd(fd):
            raise ValueError("target is not cgroup v2")
        parent = os.open("..", os.O_RDONLY | os.O_DIRECTORY, dir_fd=fd)
        try:
            if not cgroup2_fd(parent) or os.fstat(parent).st_ino == os.fstat(fd).st_ino:
                raise ValueError("cgroup mount root is not an allowed target")
        finally:
            os.close(parent)
        check_empty_leaf(fd)
        return fd, path
    except BaseException:
        os.close(fd)
        raise


def json_bytes(value):
    return (json.dumps(value, indent=2, sort_keys=True, ensure_ascii=True) + "\n").encode()


def write_json(path, value):
    payload = json_bytes(value)
    fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(payload)


def parser():
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--allow-bpf-load", action="store_true")
    result.add_argument("--profile", choices=("lifecycle", "gated", "namespaced"), default="lifecycle")
    for name in ("object", "object-sha256", "libbpf", "libbpf-sha256",
                 "cgroup", "output-parent"):
        result.add_argument("--" + name, required=True)
    for name in ("generation", "netns-cookie", "destination-port"):
        result.add_argument("--" + name, required=True, type=int)
    result.add_argument("--duration", type=float, default=10.0)
    return result


def main(argv=None):
    args = parser().parse_args(argv)
    # Refuse before reading inputs or constructing a native backend.
    if not args.allow_bpf_load:
        print(json.dumps({"status": "refused", "reason": "--allow-bpf-load required"}))
        return 2
    directory, cgroup_fd, result = None, None, None
    old_handlers, interrupted = {}, [False]

    def record_failure(stage, exc):
        nonlocal result
        error = error_record(stage, exc)
        if result is None:
            result = {"schema": "sourceidentity-loader-session-v1", "status": "failed",
                      "errors": [], "diagnostic_snapshot": None,
                      "quiescence": "unverified", "completeness": "unverified",
                      "association": "unverified", "identity_capability": "unverified"}
        result["errors"].append(error)
        result["status"] = "failed"

    def stop(signum, frame):
        # Do not raise between a native FD allocation and ownership registration.
        # The controller checks this flag at safe boundaries; cleanup keeps going.
        interrupted[0] = True

    def wait(duration):
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            if interrupted[0]:
                raise InterruptedError("stop requested during observation")
            time.sleep(min(0.1, max(0, deadline - time.monotonic())))

    try:
        if platform.system() != "Linux" or platform.machine() != "x86_64":
            raise ValueError("Linux/x86_64 required")
        if not 0 < args.duration <= 60:
            raise ValueError("duration must be greater than 0 and at most 60 seconds")
        if not (0 < args.generation < 2**64 and 0 < args.netns_cookie < 2**64 and
                0 < args.destination_port <= 65535):
            raise ValueError("invalid generation, netns cookie or port")
        obj_path, data = pinned_input(args.object, args.object_sha256)
        if len(data) < 64 or data[:7] != b"\x7fELF\x02\x01\x01" or struct.unpack_from("<HHI", data, 16) != (1, 247, 1):
            raise ValueError("BPF ELF64 little-endian relocatable object required")
        library, _ = pinned_input(args.libbpf, args.libbpf_sha256)
        parent = Path(args.output_parent)
        if not parent.is_absolute() or not parent.is_dir():
            raise ValueError("existing absolute output parent required")
        directory = Path(tempfile.mkdtemp(prefix="hoopa-observer-", dir=str(parent)))
        cgroup_fd, cgroup_path = open_target(args.cgroup)
        cgstat = os.fstat(cgroup_fd)
        config = struct.pack("<QQQII", args.generation, cgstat.st_ino,
                             args.netns_cookie, args.destination_port, 0)
        # BTF is metadata for this real-run candidate, not a verifier guarantee.
        with open("/sys/kernel/btf/vmlinux", "rb") as stream:
            btf = stream.read(64 * 1024 * 1024 + 1)
        if len(btf) > 64 * 1024 * 1024:
            raise ValueError("BTF exceeds 64 MiB")
        manifest = {
            "schema": "sourceidentity-observer-input-v1",
            "profile": args.profile,
            "object": {"path": str(obj_path), "sha256": args.object_sha256},
            "libbpf": {"path": str(library), "sha256": args.libbpf_sha256},
            "kernel": platform.release(), "btf_sha256": hashlib.sha256(btf).hexdigest(),
            "boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
            "observer_pid": os.getpid(),
            "observer_netns_inode": str(os.stat("/proc/self/ns/net").st_ino),
            "observer_pidns_inode": str(os.stat("/proc/self/ns/pid").st_ino),
            "cgroup": {"path": str(cgroup_path), "inode": str(cgstat.st_ino),
                       "device": str(cgstat.st_dev)},
            "config_hex": config.hex(), "duration_seconds": args.duration,
            "netns_cookie_source": "operator_preregistered_unverified",
            "initial_pid_namespace": "unverified", "scope_isolation": "unverified",
            "quiescence": "unverified", "association": "unverified",
        }
        if args.profile == "namespaced":
            # Boot ID alone cannot make monotonic values comparable across
            # Linux time namespaces. The future fixture driver checks this.
            manifest["observer_timens_inode"] = str(os.stat("/proc/self/ns/time").st_ino)
        write_json(directory / "input.json", manifest)
        for signum in (signal.SIGINT, signal.SIGTERM):
            old_handlers[signum] = signal.signal(signum, stop)
        try:
            backend = NativeBackend(library, data, profile=args.profile) if args.profile != "lifecycle" else NativeBackend(library, data)
            def emit(event):
                marker = dict(event, boot_id=manifest["boot_id"],
                              observed_monotonic_ns=str(time.monotonic_ns()),
                              input_sha256=hashlib.sha256(json_bytes(manifest)).hexdigest())
                write_json(directory / "attached.json", marker)
                print(json.dumps(marker), flush=True)
            result = observe(backend, config, cgroup_fd, args.duration, emit,
                             wait=wait, check_target=lambda: check_empty_leaf(cgroup_fd),
                             cancelled=lambda: interrupted[0], gated=args.profile in ("gated", "namespaced"),
                             profile=args.profile)
        except (Exception, KeyboardInterrupt) as exc:
            record_failure("runner_session", exc)
        finally:
            # Detect incidental replacement. No adversarial same-UID guarantee.
            try:
                pinned_input(library, args.libbpf_sha256)
            except (Exception, KeyboardInterrupt) as exc:
                record_failure("library_recheck", exc)
    except (Exception, KeyboardInterrupt) as exc:
        record_failure("runner", exc)
    finally:
        if cgroup_fd is not None:
            try:
                os.close(cgroup_fd)
            except OSError as exc:
                if result is not None:
                    result["errors"].append(error_record("close_cgroup", exc))
                    result["status"] = "failed"
        for signum, handler in old_handlers.items():
            signal.signal(signum, handler)
    if directory is not None:
        try:
            write_json(directory / "result.json", result)
        except OSError as exc:
            result["errors"].append(error_record("write_result", exc))
            result["status"] = "failed"
    print(json.dumps({"status": result["status"], "directory": str(directory) if directory else None,
                      "errors": result["errors"]}), flush=True)
    return 0 if result["status"] == "observed_unverified" else 1


if __name__ == "__main__":
    sys.exit(main())
