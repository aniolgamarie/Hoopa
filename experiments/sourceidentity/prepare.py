#!/usr/bin/env python3
"""Prepare an offline observation object; never load or attach a BPF program."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import selectors
import shutil
import stat
import struct
import subprocess
import sys
import tempfile
import time


MAX_ARTIFACT = 64 * 1024 * 1024
MAX_SOURCE = 1024 * 1024
MAX_LOG = 8 * 1024 * 1024
STEP_SECONDS = 30
REQUIRED_SECTIONS = {"cgroup/connect4", ".maps", ".BTF", ".BTF.ext", "license"}
SOURCE = Path(__file__).resolve().parent / "bpf" / "connect_snapshot.bpf.c"
LIFECYCLE_SOURCE = SOURCE.parent / "lifecycle_snapshot.bpf.c"
GATED_SOURCE = SOURCE.parent / "gated_snapshot.bpf.c"
NAMESPACED_SOURCE = SOURCE.parent / "namespaced_snapshot.bpf.c"
ONLINE_SOURCE = SOURCE.parent / "online_snapshot.bpf.c"
PROFILES = ("capture", "lifecycle", "gated", "namespaced", "online")


class PreparationError(Exception):
    pass


def digest(path):
    result = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(65536), b""):
            result.update(block)
    return result.hexdigest()


def private_file(path, mode="wb"):
    return os.fdopen(os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), mode)


def input_file(value, label, executable=False):
    path = Path(value)
    if not path.is_absolute():
        raise PreparationError("{}: absolute path required".format(label))
    try:
        path = path.resolve(strict=True)
        if not stat.S_ISREG(path.stat().st_mode):
            raise PreparationError("{}: regular file required".format(label))
        with open(path, "rb"):
            pass
    except OSError:
        raise PreparationError("{}: readable file required".format(label))
    if executable and not os.access(str(path), os.X_OK):
        raise PreparationError("{}: executable file required".format(label))
    return path


def snapshot_input(path, target, limit, label):
    """Copy the resolved regular input once, without following a replaced symlink."""
    descriptor = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as source, private_file(target) as output:
        if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):
            raise PreparationError(label + ": regular file required")
        copied = 0
        while True:
            block = source.read(min(65536, limit - copied + 1))
            if not block:
                break
            copied += len(block)
            if copied > limit:
                raise PreparationError(label + ": size limit exceeded")
            output.write(block)
    return target


def check_elf(path, profile="capture"):
    """Read a bounded ELF64 section table; extended numbering is unsupported."""
    if path.stat().st_size > MAX_ARTIFACT:
        raise PreparationError("object: size limit exceeded")
    with open(path, "rb") as stream:
        data = stream.read(MAX_ARTIFACT + 1)
    if len(data) > MAX_ARTIFACT:
        raise PreparationError("object: size limit exceeded")
    if len(data) < 64 or data[:7] != b"\x7fELF\x02\x01\x01":
        raise PreparationError("object: ELF64 little-endian required")
    header = struct.unpack_from("<16sHHIQQQIHHHHHH", data)
    if header[1:4] != (1, 247, 1) or header[8] != 64:
        raise PreparationError("object: BPF relocatable ELF required")
    offset, entry_size, count, names_index = header[6], header[11], header[12], header[13]
    if entry_size != 64 or not count or names_index == 0 or names_index >= count:
        raise PreparationError("object: invalid section table")
    if offset < 64 or offset + count * entry_size > len(data):
        raise PreparationError("object: section table out of bounds")
    sections = [struct.unpack_from("<IIQQQQIIQQ", data, offset + i * 64) for i in range(count)]
    names = sections[names_index]
    if names[1] != 3 or names[4] + names[5] > len(data):
        raise PreparationError("object: invalid section name table")
    strings = data[names[4]:names[4] + names[5]]
    programs = {"cgroup/connect4"}
    if profile in ("lifecycle", "gated", "namespaced", "online"):
        programs.add("sockops")
    if profile in ("gated", "namespaced", "online"):
        programs.add("tc")
    required = REQUIRED_SECTIONS | programs
    found = set()
    for section in sections:
        if section[1] != 8 and section[4] + section[5] > len(data):
            raise PreparationError("object: section out of bounds")
        start = section[0]
        end = strings.find(b"\0", start)
        if start >= len(strings) or end < 0:
            raise PreparationError("object: invalid section name")
        try:
            name = strings[start:end].decode("ascii")
            found.add(name)
        except UnicodeDecodeError:
            raise PreparationError("object: invalid section name")
        if name in required and section[5] == 0:
            raise PreparationError("object: empty required section: " + name)
        if name in programs and (section[1] != 1 or section[5] % 8):
            raise PreparationError("object: invalid " + name + " instruction section")
    missing = required - found
    if missing:
        raise PreparationError("object: missing sections: " + ", ".join(sorted(missing)))
    return sorted(found)


def run_step(directory, manifest, name, argv, stdout_limit=MAX_LOG):
    step = {"name": name, "argv": [str(arg) for arg in argv], "exit_code": None}
    manifest["steps"].append(step)
    paths = [directory / (name + suffix) for suffix in (".stdout.log", ".stderr.log")]
    step["stdout"], step["stderr"] = map(str, paths)
    with private_file(paths[0]) as out, private_file(paths[1]) as err:
        process = None
        try:
            process = subprocess.Popen(step["argv"], stdin=subprocess.DEVNULL,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            deadline = time.monotonic() + STEP_SECONDS
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ, (out, stdout_limit))
                selector.register(process.stderr, selectors.EVENT_READ, (err, MAX_LOG))
                sizes = {out: 0, err: 0}
                while selector.get_map():
                    if time.monotonic() >= deadline:
                        raise PreparationError(name + ": deadline exceeded")
                    for key, _ in selector.select(min(0.1, max(0, deadline - time.monotonic()))):
                        block = os.read(key.fd, 65536)
                        if not block:
                            selector.unregister(key.fileobj)
                            continue
                        stream, limit = key.data
                        remaining = limit - sizes[stream]
                        stream.write(block[:remaining])
                        sizes[stream] += len(block)
                        if sizes[stream] > limit:
                            raise PreparationError(name + ": output limit exceeded")
                process.wait(timeout=max(0.001, deadline - time.monotonic()))
            if process.returncode:
                raise PreparationError(name + ": command failed")
            step["status"] = "passed"
        except (OSError, subprocess.TimeoutExpired) as exc:
            step["status"] = "failed"
            raise PreparationError(name + ": execution failed") from exc
        finally:
            if process is not None:
                if process.poll() is None:
                    process.kill()
                process.wait()
                process.stdout.close()
                process.stderr.close()
                step["exit_code"] = process.returncode
            step.setdefault("status", "failed")
    return paths[0]


def prepare(args):
    profile = getattr(args, "profile", "capture")
    if profile not in PROFILES:
        raise PreparationError("profile: capture, lifecycle, gated, namespaced or online required")
    parent = Path(args.output_parent)
    if not parent.is_absolute() or not parent.is_dir():
        raise PreparationError("output-parent: absolute existing directory required")
    prefix = "hoopa-sourceidentity-" + (profile + "-" if profile != "capture" else "")
    directory = Path(tempfile.mkdtemp(prefix=prefix, dir=str(parent)))
    manifest = {"schema": "sourceidentity-offline-prepare-v1", "status": "failed",
                "output_directory": str(directory), "kernel": platform.release(),
                "system": platform.system(), "machine": platform.machine(), "steps": [],
                "runtime": "unverified", "verifier": "unverified", "association": "unverified",
                "profile": profile, "sources": [], "inputs": {}, "artifacts": {}}
    try:
        if manifest["system"] != "Linux" or manifest["machine"] != "x86_64":
            raise PreparationError("platform: Linux/x86_64 required")
        clang = input_file(args.clang, "clang", True)
        bpftool = input_file(args.bpftool, "bpftool", True)
        btf = input_file(args.btf, "btf")
        source_paths = {"capture": (SOURCE,), "lifecycle": (LIFECYCLE_SOURCE, SOURCE),
                        "gated": (GATED_SOURCE, LIFECYCLE_SOURCE, SOURCE),
                        "namespaced": (NAMESPACED_SOURCE, GATED_SOURCE, LIFECYCLE_SOURCE, SOURCE),
                        "online": (ONLINE_SOURCE, SOURCE)}[profile]
        sources = [input_file(str(path), "source: " + path.name) for path in source_paths]
        for name, path in (("clang", clang), ("bpftool", bpftool)):
            manifest["inputs"][name] = {"path": str(path), "sha256": digest(path)}
        snapshot = snapshot_input(btf, directory / "kernel.btf", MAX_ARTIFACT, "btf")
        manifest["inputs"]["btf"] = {"original_path": str(btf), "path": str(snapshot),
                                     "sha256": digest(snapshot)}
        for path, fixed_path in zip(sources, source_paths):
            snapshot = snapshot_input(path, directory / fixed_path.name, MAX_SOURCE, "source")
            manifest["sources"].append({"original_path": str(path), "path": str(snapshot),
                                        "sha256": digest(snapshot)})
        manifest["inputs"]["source"] = manifest["sources"][0]
        btf = Path(manifest["inputs"]["btf"]["path"])
        source = Path(manifest["inputs"]["source"]["path"])
        for name, tool, flag in (("clang-version", clang, "--version"), ("bpftool-version", bpftool, "version")):
            version_log = run_step(directory, manifest, name, [tool, flag])
            manifest["inputs"][name.split("-")[0]]["version"] = version_log.read_text(errors="replace")
        header_log = run_step(directory, manifest, "btf-dump",
                              [bpftool, "btf", "dump", "file", btf, "format", "c"], MAX_ARTIFACT)
        header = directory / "vmlinux.h"
        with private_file(header) as output, open(header_log, "rb") as source_stream:
            shutil.copyfileobj(source_stream, output)
        obj = source.with_suffix(".o")
        run_step(directory, manifest, "compile", [clang, "-g", "-O2", "-target", "bpfel",
                 "-D__TARGET_ARCH_x86", "-I" + str(directory), "-c", source, "-o", obj])
        if not obj.is_file() or obj.is_symlink():
            raise PreparationError("object: regular output file required")
        obj.chmod(0o600)
        manifest["sections"] = check_elf(obj, profile)
        manifest["status"] = "prepared"
    except (PreparationError, OSError) as exc:
        manifest["error"] = str(exc)
    finally:
        for name in ("clang", "bpftool"):
            tool = manifest["inputs"].get(name)
            if tool is None:
                continue
            try:
                tool["final_sha256"] = digest(tool["path"])
                tool["unchanged"] = tool["sha256"] == tool["final_sha256"]
            except OSError:
                tool["final_sha256"] = None
                tool["unchanged"] = False
            if not tool["unchanged"]:
                manifest["status"] = "failed"
                previous = manifest.get("error")
                manifest["error"] = ((previous + "; ") if previous else "") + name + ": tool changed during preparation"
        for path in directory.iterdir():
            if path.is_file() and not path.is_symlink():
                path.chmod(0o600)
                manifest["artifacts"][path.name] = {"sha256": digest(path), "bytes": path.stat().st_size}
        manifest_path = directory / "manifest.json"
        with private_file(manifest_path, "w") as stream:
            json.dump(manifest, stream, indent=2, sort_keys=True)
            stream.write("\n")
    print(json.dumps({"status": manifest["status"], "manifest": str(manifest_path),
                      "output_directory": str(directory)}))
    if "error" in manifest:
        print("prepare: " + manifest["error"], file=sys.stderr)
        return 1
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("clang", "bpftool", "btf"):
        parser.add_argument("--" + flag, required=True)
    parser.add_argument("--output-parent", default="/tmp")
    parser.add_argument("--profile", choices=PROFILES, default="capture")
    args = parser.parse_args()
    try:
        return prepare(args)
    except (PreparationError, OSError) as exc:
        print("prepare: " + str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
