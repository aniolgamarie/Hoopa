#!/usr/bin/env python3
"""Inspect a pinned observation ELF with libbpf; never load, attach, or pin it."""

import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import stat
import struct
import sys

sys.dont_write_bytecode = True
from observations import expected_maps

MAX_INPUT = 64 * 1024 * 1024
MAX_ITEMS = 16
PROGRAMS = {
    "scoped-executable-online": {"hoopa_connect": "cgroup/connect4", "hoopa_lifecycle": "sockops", "hoopa_stop": "tc",
        "call_enter": "raw_tp/sys_enter", "call_exit": "raw_tp/sys_exit", "capture_exe": "lsm/socket_connect"},
    "executable-online": {"hoopa_connect": "cgroup/connect4", "hoopa_lifecycle": "sockops", "hoopa_stop": "tc",
                          "call_enter": "raw_tp/sys_enter", "call_exit": "raw_tp/sys_exit",
                          "capture_exe": "lsm/socket_connect"},
    "capture": {"hoopa_connect": "cgroup/connect4"},
    "lifecycle": {"hoopa_connect": "cgroup/connect4", "hoopa_lifecycle": "sockops"},
    "namespaced": {"hoopa_connect": "cgroup/connect4", "hoopa_lifecycle": "sockops", "hoopa_stop": "tc"},
    "gated": {"hoopa_connect": "cgroup/connect4", "hoopa_lifecycle": "sockops", "hoopa_stop": "tc"},
    "online": {"hoopa_connect": "cgroup/connect4", "hoopa_lifecycle": "sockops", "hoopa_stop": "tc"},
}


class InspectionError(ValueError):
    pass


def pinned_input(value, expected_sha256):
    if not re.fullmatch(r"[0-9a-f]{64}", expected_sha256):
        raise InspectionError("expected SHA256 must be 64 lowercase hex digits")
    path = Path(value)
    if not path.is_absolute():
        raise InspectionError("absolute input path required")
    path = path.resolve(strict=True)
    if not stat.S_ISREG(path.stat().st_mode):
        raise InspectionError("regular input file required")
    # Recheck after open, and avoid blocking if a regular file is replaced by
    # a FIFO between stat and open. Regular files ignore O_NONBLOCK.
    fd = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise InspectionError("regular input file required")
        data = stream.read(MAX_INPUT + 1)
    if len(data) > MAX_INPUT:
        raise InspectionError("input exceeds 64 MiB")
    if hashlib.sha256(data).hexdigest() != expected_sha256:
        raise InspectionError("input SHA256 mismatch")
    return path, data


class Libbpf:
    """Only the offline open/read/close API is bound. No load API is exposed."""

    def __init__(self, path):
        lib = ctypes.CDLL(str(path), use_errno=True)
        self.library = lib
        ptr = ctypes.c_void_p

        def bind(names, args, result):
            for name in names:
                function = getattr(lib, name, None)
                if function is not None:
                    function.argtypes, function.restype = args, result
                    return function, name
            raise InspectionError("libbpf missing symbol: " + "/".join(names))

        self.open, _ = bind(["bpf_object__open_mem"], [ptr, ctypes.c_size_t, ptr], ptr)
        self.error, _ = bind(["libbpf_get_error"], [ptr], ctypes.c_long)
        self.close, _ = bind(["bpf_object__close"], [ptr], None)
        self.next_program, program_iterator = bind(
            ["bpf_object__next_program", "bpf_program__next"], [ptr, ptr], ptr)
        self.next_map, map_iterator = bind(
            ["bpf_object__next_map", "bpf_map__next"], [ptr, ptr], ptr)
        self.iterator_api = {"program": program_iterator, "map": map_iterator}
        self.program_fields = {}
        for key, names, result in [
            ("name", ["bpf_program__name"], ctypes.c_char_p),
            ("section", ["bpf_program__section_name"], ctypes.c_char_p),
            ("observed_type", ["bpf_program__type", "bpf_program__get_type"], ctypes.c_int),
            ("observed_expected_attach_type", ["bpf_program__expected_attach_type",
                                               "bpf_program__get_expected_attach_type"], ctypes.c_int),
        ]:
            self.program_fields[key], _ = bind(names, [ptr], result)
        self.map_name, _ = bind(["bpf_map__name"], [ptr], ctypes.c_char_p)
        self.map_fields = {}
        for key in ("type", "key_size", "value_size", "max_entries", "map_flags"):
            result_type = ctypes.c_int if key == "type" else ctypes.c_uint32
            self.map_fields[key], _ = bind(["bpf_map__" + key], [ptr], result_type)

    @staticmethod
    def name(value):
        if not value or len(value) > 128 or any(c < 32 or c > 126 for c in value):
            raise InspectionError("libbpf returned an invalid metadata name")
        return value.decode("ascii")

    def iterate(self, obj, kind):
        function = self.next_program if kind == "program" else self.next_map
        modern = self.iterator_api[kind].startswith("bpf_object__")
        previous = None
        seen = set()
        while True:
            current = function(obj, previous) if modern else function(previous, obj)
            if not current:
                return
            if current in seen or len(seen) >= MAX_ITEMS:
                raise InspectionError("libbpf iterator repeated or exceeded bound")
            seen.add(current)
            yield current
            previous = current

    def inspect(self, data):
        # This buffer must outlive the libbpf object, including all getters/close.
        buffer = ctypes.create_string_buffer(data)
        obj = self.open(buffer, len(data), None)
        error = self.error(obj)
        if not obj or error:
            raise InspectionError("libbpf open failed: {}".format(error or ctypes.get_errno()))
        try:
            programs = []
            for program in self.iterate(obj, "program"):
                entry = {key: getter(program) for key, getter in self.program_fields.items()}
                entry["name"], entry["section"] = self.name(entry["name"]), self.name(entry["section"])
                programs.append(entry)
            maps = {}
            for item in self.iterate(obj, "map"):
                name = self.name(self.map_name(item))
                if name in maps:
                    raise InspectionError("duplicate map name")
                maps[name] = {key: getter(item) for key, getter in self.map_fields.items()}
            return {"programs": programs, "maps": maps, "iterator_api": self.iterator_api}
        finally:
            self.close(obj)


def inspect_object(profile, object_path, object_sha256, libbpf_path, libbpf_sha256):
    if profile not in PROGRAMS:
        raise InspectionError("unsupported profile")
    if platform.system() != "Linux" or platform.machine() != "x86_64":
        raise InspectionError("Linux/x86_64 required")
    path, data = pinned_input(object_path, object_sha256)
    if len(data) < 64 or data[:7] != b"\x7fELF\x02\x01\x01":
        raise InspectionError("ELF64 little-endian object required")
    if struct.unpack_from("<HHI", data, 16) != (1, 247, 1):
        raise InspectionError("BPF relocatable object required")
    library, _ = pinned_input(libbpf_path, libbpf_sha256)
    try:
        result = Libbpf(library).inspect(data)
    finally:
        # Detect incidental replacement; this is not adversarial same-UID isolation.
        pinned_input(library, libbpf_sha256)
    declarations = {p["name"]: p["section"] for p in result["programs"]}
    if len(declarations) != len(result["programs"]) or declarations != PROGRAMS[profile]:
        raise InspectionError("program names/sections do not match profile")
    if result["maps"] != expected_maps(profile):
        raise InspectionError("map ABI metadata does not match profile")
    result.update({
        "schema": "sourceidentity-object-inspection-v1", "status": "inspected",
        "profile": profile, "object": {"path": str(path), "sha256": object_sha256, "bytes": len(data)},
        "libbpf": {"path": str(library), "sha256": libbpf_sha256},
        "runtime": "unverified", "verifier": "unverified", "association": "unverified",
        "program_type_verification": "unverified",
        "scope": "offline ELF declarations; no load, attach, pin or kernel map access",
    })
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=sorted(PROGRAMS), required=True)
    for name in ("object", "object-sha256", "libbpf", "libbpf-sha256"):
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args()
    try:
        result = inspect_object(args.profile, args.object, args.object_sha256,
                                args.libbpf, args.libbpf_sha256)
    except (InspectionError, OSError) as exc:
        result = {"schema": "sourceidentity-object-inspection-v1", "status": "failed",
                  "error": str(exc), "runtime": "unverified", "verifier": "unverified",
                  "association": "unverified"}
        print(json.dumps(result, sort_keys=True))
        return 1
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
