"""Finite lifecycle libbpf backend. Construction never loads or attaches BPF.

Snapshots are bounded observations without a quiescence or completeness guarantee;
neither a zero fault count nor closing resources proves collection completeness.
"""
import ctypes
import errno
import fcntl
import hashlib
import os
import re
import struct
import time

from inspect_object import Libbpf, InspectionError, PROGRAMS
from observations import expected_maps, decode_gate, decode_config, decode_entry, ObservationError, decode_identity_scope


class NativeError(ValueError):
    def __init__(self, message, ret=None, errno_value=None):
        self.ret = ret
        self.errno = errno_value
        super().__init__(message + (" (ret={}, errno={})".format(ret, errno_value)
                                   if ret is not None else ""))


PROGRAM_TYPES = {"hoopa_connect": (18, 10), "hoopa_lifecycle": (13, 3)}


EXECUTABLE_PROGRAM_TYPES = dict(PROGRAM_TYPES, hoopa_stop=(3, 0), call_enter=(17, 0),
                                call_exit=(17, 0), capture_exe=(29, 27))
EXECUTABLE_MAP_REFS = {
    "hoopa_connect": {"capture_config", "capture_gate", "capture_faults", "exe_state", "calls", "exe_records", "connect_records"},
    "hoopa_lifecycle": {"capture_gate", "capture_config", "capture_faults", "connect_records", "online_tuples", "online_bindings"},
    "hoopa_stop": {"capture_gate"},
    "call_enter": {"capture_config", "exe_state", "calls"},
    "call_exit": {"capture_config", "exe_state", "calls"},
    "capture_exe": {"capture_config", "exe_state", "calls", "exe_records"},
}
EXECUTABLE_PROFILES = ("executable-online", "scoped-executable-online")
SCOPED_MAP_REFS = {name:set(refs) for name,refs in EXECUTABLE_MAP_REFS.items()}
SCOPED_MAP_REFS["hoopa_connect"].add("identity_scope")
SCOPED_MAP_REFS["capture_exe"].update(("identity_scope", "capture_faults"))
TARGET_BTF_BYTES = 6018902
TARGET_BTF_SHA256 = "1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9"


class TestRunOptions(ctypes.Structure):
    """libbpf bpf_test_run_opts; sz permits older libraries' zero-tail checks."""
    _fields_ = [("sz", ctypes.c_size_t), ("data_in", ctypes.c_void_p),
                ("data_out", ctypes.c_void_p), ("data_size_in", ctypes.c_uint32),
                ("data_size_out", ctypes.c_uint32), ("ctx_in", ctypes.c_void_p),
                ("ctx_out", ctypes.c_void_p), ("ctx_size_in", ctypes.c_uint32),
                ("ctx_size_out", ctypes.c_uint32), ("retval", ctypes.c_uint32),
                ("repeat", ctypes.c_int), ("duration", ctypes.c_uint32),
                ("flags", ctypes.c_uint32), ("cpu", ctypes.c_uint32),
                ("batch_size", ctypes.c_uint32)]


class NativeBackend:
    def __init__(self, library_path, object_bytes, profile="lifecycle"):
        if profile not in ("lifecycle", "gated", "namespaced", "online", "executable-online", "scoped-executable-online"):
            raise NativeError("unsupported native profile")
        self.profile = profile
        self.program_types = dict(PROGRAM_TYPES)
        if profile in ("gated", "namespaced", "online", "executable-online", "scoped-executable-online"):
            self.program_types["hoopa_stop"] = (3, 0)  # SCHED_CLS, never attached
        if profile in EXECUTABLE_PROFILES:
            self.program_types = dict(EXECUTABLE_PROGRAM_TYPES)
        self.identity_scope_raw = None
        self.scoped_capture_raw = None
        self.scope_frozen = False
        self.link_pointers = {}
        self.program_targets = {}
        self.target_btf_verified = False
        self.target_btf_object_id = None
        self.obj = None
        self.buffer = None
        self.data = object_bytes
        self.maps = {}
        self.programs = {}
        self.map_fds = {}
        self.program_fds = {}
        self.map_ids = {}
        self.program_ids = {}
        self.owned_links = set()
        self.loaded = False
        self.verified = False
        self.validated = False
        try:
            self.offline = Libbpf(library_path)
            lib = self.offline.library
            p, i, u = ctypes.c_void_p, ctypes.c_int, ctypes.c_uint32
            signatures = {
                "bpf_object__load": ([p], i),
                "bpf_object__find_map_by_name": ([p, ctypes.c_char_p], p),
                "bpf_object__find_program_by_name": ([p, ctypes.c_char_p], p),
                "bpf_map__fd": ([p], i), "bpf_program__fd": ([p], i),
                # libbpf v0.6.0 libbpf.h declares both setters void.
                "bpf_program__set_type": ([p, i], None),
                "bpf_program__set_expected_attach_type": ([p, i], None),
                "bpf_map_update_elem": ([i, p, p, ctypes.c_uint64], i),
                "bpf_map_lookup_elem": ([i, p, p], i),
                "bpf_map_get_next_key": ([i, p, p], i),
                "bpf_map_freeze": ([i], i),
                "bpf_link_create": ([i, i, i, p], i),
                "bpf_obj_get_info_by_fd": ([i, p, ctypes.POINTER(u)], i),
            }
            if profile in ("gated", "namespaced", "online", "executable-online", "scoped-executable-online"):
                signatures.update({
                    "bpf_map_lookup_elem_flags": ([i, p, p, ctypes.c_uint64], i),
                    "bpf_prog_test_run_opts": ([i, ctypes.POINTER(TestRunOptions)], i),
                })
            if profile in EXECUTABLE_PROFILES:
                signatures.update({
                    "bpf_program__attach": ([p], p),
                    "bpf_link__fd": ([p], i),
                    "bpf_link__destroy": ([p], i),
                    "bpf_btf_get_fd_by_id": ([u], i),
                })
            self.api = {}
            for name, (args, result) in signatures.items():
                function = getattr(lib, name)
                function.argtypes, function.restype = args, result
                self.api[name] = function
        except (InspectionError, AttributeError, OSError) as exc:
            raise NativeError(str(exc)) from exc

    def _require(self, condition, message):
        if not condition:
            raise NativeError(message)

    @staticmethod
    def _errno(ret):
        return (ctypes.get_errno() or errno.EPERM) if ret == -1 else (-ret if ret < 0 else 0)

    def _call(self, name, *args, allow=None):
        ctypes.set_errno(0)
        ret = self.api[name](*args)
        if ret < 0 and self._errno(ret) != allow:
            raise NativeError(name + " failed", ret, self._errno(ret))
        if ret > 0 and name not in ("bpf_map__fd", "bpf_program__fd", "bpf_link_create", "bpf_link__fd", "bpf_btf_get_fd_by_id"):
            raise NativeError(name + " returned unexpected positive status", ret, 0)
        return ret

    def _pointer(self, value, label):
        saved_errno = ctypes.get_errno()
        error = self.offline.error(value)
        if not value or error:
            raise NativeError(label + " returned invalid pointer", error or -1,
                              -error if error < 0 else saved_errno)
        return value

    def open_object(self):
        self._require(self.obj is None, "object already open")
        self.buffer = ctypes.create_string_buffer(self.data)
        ctypes.set_errno(0)
        obj = self.offline.open(self.buffer, len(self.data), None)
        # Capture errno before libbpf_get_error can change it.
        saved_errno = ctypes.get_errno()
        error = self.offline.error(obj)
        if not obj or error:
            self.buffer = None
            raise NativeError("bpf_object__open_mem failed", error or -1,
                              -error if error < 0 else saved_errno)
        self.obj = obj
        try:
            declarations = {}
            for program in self.offline.iterate(obj, "program"):
                name = self.offline.name(self.offline.program_fields["name"](program))
                section = self.offline.name(self.offline.program_fields["section"](program))
                self._require(name not in declarations, "duplicate program name")
                declarations[name] = section
                self.programs[name] = program
            self._require(declarations == PROGRAMS[self.profile], "program names/sections mismatch")
            metadata = {}
            for item in self.offline.iterate(obj, "map"):
                name = self.offline.name(self.offline.map_name(item))
                self._require(name not in metadata, "duplicate map name")
                metadata[name] = {key: getter(item) for key, getter in self.offline.map_fields.items()}
                self.maps[name] = item
            self._require(metadata == expected_maps(self.profile), "map ABI metadata mismatch")
            for name, pointer in self.maps.items():
                found = self._pointer(self.api["bpf_object__find_map_by_name"](obj, name.encode()), "find map")
                self._require(found == pointer, "map lookup pointer mismatch")
            for name, pointer in self.programs.items():
                found = self._pointer(self.api["bpf_object__find_program_by_name"](obj, name.encode()), "find program")
                self._require(found == pointer, "program lookup pointer mismatch")
            self.validated = True
        except InspectionError as exc:
            raise NativeError(str(exc)) from exc

    def load_object(self):
        self._require(self.obj is not None and self.validated and not self.loaded, "object not open or already loaded")
        self._require(set(self.programs) == set(self.program_types) and
                      set(self.maps) == set(expected_maps(self.profile)), "object not validated")
        for name, (kind, attach) in self.program_types.items():
            pointer = self.programs[name]
            self.api["bpf_program__set_type"](pointer, kind)
            self.api["bpf_program__set_expected_attach_type"](pointer, attach)
            self._require(self.offline.program_fields["observed_type"](pointer) == kind and
                          self.offline.program_fields["observed_expected_attach_type"](pointer) == attach,
                          "program type setters did not take effect")
        self._call("bpf_object__load", self.obj)
        self.loaded = True
        for name, pointer in self.maps.items():
            self.map_fds[name] = self._call("bpf_map__fd", pointer)
        for name, pointer in self.programs.items():
            self.program_fds[name] = self._call("bpf_program__fd", pointer)

    def _info(self, fd, minimum, buffer=None):
        buffer = buffer if buffer is not None else ctypes.create_string_buffer(256)
        length = ctypes.c_uint32(len(buffer))
        self._call("bpf_obj_get_info_by_fd", fd, buffer, ctypes.byref(length))
        self._require(minimum <= length.value <= len(buffer), "short or oversized kernel info")
        return buffer

    def verify_loaded(self):
        self._require(self.loaded, "object not loaded")
        maps, programs, access_modes = {}, {}, {}
        for name, meta in expected_maps(self.profile).items():
            raw = self._info(self.map_fds[name], 40).raw
            kind, ident, key, value, maximum, flags = struct.unpack_from("<6I", raw)
            actual = dict(type=kind, key_size=key, value_size=value,
                          max_entries=maximum, map_flags=flags)
            observed_name = raw[24:40].split(b"\0", 1)[0]
            # BPF_F_RDONLY/WRONLY describe this fd, not the map object.
            # The kernel removes these bits from bpf_map_info.map_flags.
            kernel_meta = dict(meta, map_flags=meta["map_flags"] & ~(8 | 16))
            self._require(actual == kernel_meta and observed_name == name.encode()[:15],
                          "kernel map metadata mismatch: {} expected={} actual={} name_hex={}".format(
                              name, kernel_meta, actual, observed_name.hex()))
            requested_access = meta["map_flags"] & (8 | 16)
            self._require(requested_access != (8 | 16), "invalid map descriptor flags")
            expected_access = (os.O_RDONLY if requested_access == 8 else
                               os.O_WRONLY if requested_access == 16 else os.O_RDWR)
            observed_access = self._fd_access(self.map_fds[name])
            self._require(observed_access == expected_access,
                          "map descriptor access mismatch: {} expected={} actual={}".format(
                              name, expected_access, observed_access))
            access_modes[name] = dict(requested_flags=meta["map_flags"], kernel_flags=flags,
                                      access_mode=observed_access)
            self._require(ident != 0 and ident not in maps.values(), "zero or duplicate map ID")
            maps[name] = ident
        for name, (kind, _) in self.program_types.items():
            fd = self.program_fds[name]
            first = self._info(fd, 228 if self.profile in EXECUTABLE_PROFILES else 80).raw
            actual, ident = struct.unpack_from("<II", first)
            count, = struct.unpack_from("<I", first, 52)
            self._require(actual == kind and ident != 0 and ident not in [p["id"] for p in programs.values()] and
                          first[64:80].split(b"\0", 1)[0] == name.encode()[:15], "kernel program metadata mismatch")
            self._require(count <= 16, "program map count exceeds bound")
            ids = (ctypes.c_uint32 * count)()
            buffer = ctypes.create_string_buffer(256)
            struct.pack_into("<IQ", buffer, 52, count, ctypes.addressof(ids))
            raw = self._info(fd, 228 if self.profile in EXECUTABLE_PROFILES else 80, buffer).raw
            second_count, = struct.unpack_from("<I", raw, 52)
            self._require(second_count <= count, "program map count grew")
            self._require(raw[:8] == first[:8] and raw[64:80] == first[64:80], "program info changed")
            actual_ids = list(ids)[:second_count]
            names = set(maps) if name == "hoopa_lifecycle" else {"capture_config", "connect_records", "capture_faults"}
            if self.profile in ("gated", "namespaced", "online", "executable-online", "scoped-executable-online"):
                names.add("capture_gate")
                if name == "hoopa_stop":
                    names = {"capture_gate"}
            if self.profile in EXECUTABLE_PROFILES:
                names = (SCOPED_MAP_REFS if self.profile == "scoped-executable-online" else EXECUTABLE_MAP_REFS)[name]
            self._require(len(actual_ids) == len(names) and set(actual_ids) == {maps[n] for n in names}, "program map IDs mismatch")
            if self.profile in EXECUTABLE_PROFILES:
                if name == "capture_exe":
                    self._require(len(raw) >= 228, "short LSM program info")
                    obj_id, func_id = struct.unpack_from("<II", raw, 220)
                    self._require(obj_id > 0 and func_id == 39733, "LSM program target mismatch")
                    self.program_targets[name] = (obj_id, func_id)
                    self._verify_target_btf(obj_id)
            programs[name] = dict(id=ident, type=kind, map_ids=actual_ids)
        # The returned evidence must survive close_object clearing live state.
        self.map_ids = dict(maps)
        self.program_ids = {name: entry["id"] for name, entry in programs.items()}
        self.verified = True
        return dict(maps=maps, programs=programs, map_access=access_modes)

    def _verify_target_btf(self, object_id):
        if self.target_btf_verified:
            self._require(object_id == self.target_btf_object_id, "target BTF object changed")
            return
        fd = self._call("bpf_btf_get_fd_by_id", object_id)
        try:
            data = ctypes.create_string_buffer(TARGET_BTF_BYTES)
            info = ctypes.create_string_buffer(32)
            struct.pack_into("<QII", info, 0, ctypes.addressof(data), TARGET_BTF_BYTES, 0)
            raw = self._info(fd, 32, info).raw
            size, ident = struct.unpack_from("<II", raw, 8)
            self._require(size == TARGET_BTF_BYTES and ident == object_id and
                          hashlib.sha256(data.raw).hexdigest() == TARGET_BTF_SHA256,
                          "LSM target BTF identity/bytes mismatch")
        finally:
            os.close(fd)  # the temporary BTF descriptor is closed exactly once
        self.target_btf_verified = True
        self.target_btf_object_id = object_id

    def _create_program_link(self, name):
        self._require(self.profile != "scoped-executable-online" or self.scope_frozen,"scope must be frozen before program attach")
        self._require(self.verified and name in self.programs, "unknown or unverified program")
        pointer = self._pointer(self.api["bpf_program__attach"](self.programs[name]), "attach program")
        fd, registered = None, False
        try:
            fd = self._call("bpf_link__fd", pointer)
            self._require(fd not in self.owned_links, "duplicate owned link FD")
            self.link_pointers[fd] = pointer
            registered = True
            self.owned_links.add(fd)
        except BaseException:
            if registered:
                self.link_pointers.pop(fd)
                self.owned_links.discard(fd)
            self._call("bpf_link__destroy", pointer)
            raise
        return fd

    def _verify_program_link(self, fd, name):
        buffer = ctypes.create_string_buffer(256)
        target = None
        if name in ("call_enter", "call_exit"):
            target = ctypes.create_string_buffer(32)
            struct.pack_into("<QI", buffer, 16, ctypes.addressof(target), 32)
        raw = self._info(fd, 28, buffer).raw
        kind, ident, program = struct.unpack_from("<III", raw)
        self._require(ident > 0 and program == self.program_ids[name], "program link identity mismatch")
        if target is not None:
            expected = b"sys_enter" if name == "call_enter" else b"sys_exit"
            length, = struct.unpack_from("<I", raw, 24)
            self._require(kind == 1 and length == len(expected)+1 and
                          target.raw[:length] == expected+b"\0", "raw tracepoint target mismatch")
            return dict(id=ident, program_id=program, tp_name=expected.decode("ascii"))
        attach, obj_id, func_id = struct.unpack_from("<III", raw, 16)
        self._require(kind == 2 and attach == 27 and self.target_btf_verified and
                      (obj_id, func_id) == self.program_targets[name] and func_id == 39733,
                      "LSM link target mismatch")
        return dict(id=ident, program_id=program, attach_type=attach,
                    target_obj_id=obj_id, target_btf_id=func_id)

    @staticmethod
    def _fd_access(fd):
        return fcntl.fcntl(fd, fcntl.F_GETFL) & os.O_ACCMODE

    def _lookup(self, name, key):
        out = ctypes.create_string_buffer(expected_maps(self.profile)[name]["value_size"])
        self._call("bpf_map_lookup_elem", self.map_fds[name], ctypes.create_string_buffer(key), out)
        return out.raw

    def _entries(self, name):
        meta = expected_maps(self.profile)[name]
        previous, result, seen = None, [], set()
        while True:
            key = ctypes.create_string_buffer(meta["key_size"])
            ret = self._call("bpf_map_get_next_key", self.map_fds[name], previous, key, allow=errno.ENOENT)
            if ret < 0:
                return result
            raw = key.raw
            self._require(raw not in seen and len(result) < meta["max_entries"], "map enumeration repeated or exceeded bound")
            seen.add(raw)
            result.append((raw, self._lookup(name, raw)))
            previous = key

    def set_identity_scope(self, raw24):
        self._require(self.profile == "scoped-executable-online" and self.obj is None and not self.loaded and self.identity_scope_raw is None,"scope registration must precede object opening")
        decode_identity_scope(raw24)
        self.identity_scope_raw = bytes(raw24)

    def read_frozen_identity_scope(self):
        """Read actual frozen map bytes for the scoped profile's exec oracle."""
        self._require(self.profile == "scoped-executable-online" and self.verified and self.scope_frozen,
                      "frozen verified scope required")
        raw = self._lookup("identity_scope", bytes(4))
        self._require(raw == self.identity_scope_raw, "exec scope readback mismatch")
        decode_identity_scope(raw)
        return bytes(raw)

    def initialize_config(self, raw32):
        self._require(self.verified and isinstance(raw32, bytes) and len(raw32) == 32, "invalid config or unverified object")
        if self.profile == "scoped-executable-online":
            self._require(self.identity_scope_raw is not None and decode_identity_scope(self.identity_scope_raw)["generation"] == decode_config(raw32).generation,"scope/config generation mismatch")
            self._require(self._lookup("identity_scope",bytes(4)) == bytes(24),"initial scope nonzero")
        hashmaps = (("connect_records", "online_tuples", "online_bindings") if self.profile in ("online", "executable-online", "scoped-executable-online")
                    else ("connect_records", "established_records", "close_records"))
        if self.profile in EXECUTABLE_PROFILES:
            hashmaps += ("exe_records",)
            self._require(self._lookup("exe_state", bytes(4)) == bytes(64), "initial executable state nonzero")
        for name in hashmaps:
            self._require(not self._entries(name), "observation map initially nonempty")
        self._require(self._lookup("capture_faults", b"\0" * 4) == b"\0" * 8, "initial faults nonzero")
        self._require(self._lookup("capture_config", b"\0" * 4) == b"\0" * 32, "initial config nonzero")
        if self.profile in ("gated", "namespaced", "online", "executable-online", "scoped-executable-online"):
            gate = self.read_gate()
            self._require(not gate["closed"] and gate["admitted"] == "0", "initial gate nonzero")
        self._call("bpf_map_update_elem", self.map_fds["capture_config"], ctypes.create_string_buffer(b"\0" * 4), ctypes.create_string_buffer(raw32), 0)
        self._require(self._lookup("capture_config", b"\0" * 4) == raw32, "config readback mismatch")
        if self.profile == "scoped-executable-online":
            self._call("bpf_map_update_elem",self.map_fds["identity_scope"],ctypes.create_string_buffer(bytes(4)),ctypes.create_string_buffer(self.identity_scope_raw),0)
            self._require(self._lookup("identity_scope",bytes(4)) == self.identity_scope_raw,"scope readback mismatch")
            self.scoped_capture_raw = bytes(raw32)

    def freeze_config(self, raw32):
        self._require(self.verified and isinstance(raw32, bytes) and len(raw32) == 32, "invalid config or unverified object")
        if self.profile == "scoped-executable-online":
            self._require(self.scoped_capture_raw == raw32 and self.identity_scope_raw is not None,"scope/config initialization required")
        fd = self.map_fds["capture_config"]
        self._call("bpf_map_freeze", fd)
        ret = self._call("bpf_map_update_elem", fd, ctypes.create_string_buffer(b"\0" * 4), ctypes.create_string_buffer(raw32), 0, allow=errno.EPERM)
        self._require(ret < 0, "frozen config still writable")
        self._require(self._lookup("capture_config", b"\0" * 4) == raw32, "frozen config readback mismatch")
        if self.profile == "scoped-executable-online":
            fd=self.map_fds["identity_scope"]
            self._call("bpf_map_freeze",fd)
            ret=self._call("bpf_map_update_elem",fd,ctypes.create_string_buffer(bytes(4)),ctypes.create_string_buffer(self.identity_scope_raw),0,allow=errno.EPERM)
            self._require(ret<0,"frozen scope still writable")
            self._require(self._lookup("identity_scope",bytes(4))==self.identity_scope_raw,"frozen scope readback mismatch")
            self.scope_frozen=True

    def create_link(self, program_name, cgroup_fd):
        self._require(self.profile != "scoped-executable-online" or self.scope_frozen,"both configurations must be frozen before any attach")
        if self.profile in EXECUTABLE_PROFILES and program_name in ("call_exit", "call_enter", "capture_exe"):
            return self._create_program_link(program_name)
        self._require(self.verified and program_name in PROGRAM_TYPES, "unknown or unverified program")
        fd = self._call("bpf_link_create", self.program_fds[program_name], cgroup_fd, PROGRAM_TYPES[program_name][1], None)
        try:
            self.owned_links.add(fd)
        except BaseException as exc:
            # If ownership bookkeeping fails, the caller has not received this
            # FD. Close it once here; do not depend on the controller's list.
            try:
                os.close(fd)
            except OSError as cleanup:
                raise NativeError("register link ownership failed: {!r}; close failed: {}".format(exc, cleanup),
                                  -1, cleanup.errno) from exc
            raise
        return fd

    def verify_link(self, linkfd, program_name, cgroup_id):
        self._require(linkfd in self.owned_links and program_name in self.program_ids, "unknown link or program")
        if self.profile in EXECUTABLE_PROFILES and program_name in ("call_exit", "call_enter", "capture_exe"):
            return self._verify_program_link(linkfd, program_name)
        raw = self._info(linkfd, 28).raw
        kind, ident, program = struct.unpack_from("<III", raw)
        cgroup, attach = struct.unpack_from("<QI", raw, 16)
        self._require(kind == 3 and ident != 0 and program == self.program_ids[program_name] and
                      cgroup == cgroup_id and attach == PROGRAM_TYPES[program_name][1], "kernel link metadata mismatch")
        return dict(id=ident, program_id=program, cgroup_id=str(cgroup), attach_type=attach)

    def close_link(self, fd):
        self._require(fd in self.owned_links, "link not owned")
        self.owned_links.remove(fd)  # close is never retried, including EINTR.
        if fd in self.link_pointers:
            pointer = self.link_pointers.pop(fd)
            self._call("bpf_link__destroy", pointer)
        else:
            try:
                os.close(fd)
            except OSError as exc:
                raise NativeError("close link failed", -1, exc.errno) from exc

    def snapshot(self):
        self._require(self.profile not in ("online", "executable-online", "scoped-executable-online"), "online snapshot is unsupported")
        self._require(self.verified, "object not verified")
        gate = self.read_gate() if self.profile in ("gated", "namespaced") else None
        if gate is not None:
            self._require(gate["closed"] and gate["active"] == "0", "gate is not drained")
        result = {name: self._entries(name) for name in expected_maps(self.profile)
                  if name not in ("capture_config", "capture_gate")}
        if gate is not None:
            self._require(self.read_gate() == gate, "gate changed during snapshot")
        return result

    def _read_gate_raw(self):
        self._require(self.verified and self.profile in ("gated", "namespaced", "online", "executable-online", "scoped-executable-online"), "no verified gate")
        out = ctypes.create_string_buffer(40)
        self._call("bpf_map_lookup_elem_flags", self.map_fds["capture_gate"],
                   ctypes.create_string_buffer(bytes(4)), out, 4)  # BPF_F_LOCK
        return out.raw

    def read_gate(self):
        return decode_gate(self._read_gate_raw())

    def online_lookup(self, accepted_cookie, config_raw):
        """Bounded raw observations only; caller owns link lifetime and strict decoding."""
        self._require(self.profile == "online" and self.loaded and self.verified,
                      "no verified online object")
        self._require(len(self.owned_links) == 2, "exactly two owned online links required")
        self._require(isinstance(accepted_cookie, str)
                      and re.fullmatch(r"[1-9][0-9]{0,19}", accepted_cookie)
                      and int(accepted_cookie) < 2**64, "invalid accepted cookie")
        try:
            config = decode_config(config_raw)
            self._require(self._lookup("capture_config", bytes(4)) == config_raw,
                          "online config readback mismatch")
            gate = self._read_gate_raw()
            self._require(not decode_gate(gate)["closed"], "online gate is closed")
            faults = self._lookup("capture_faults", bytes(4))
            self._require(faults == bytes(8), "online capture faults nonzero")
            key = struct.pack("<Q", int(accepted_cookie))
            binding = self._lookup("online_bindings", key)
            decoded = decode_entry("online_bindings", key, binding, config, profile="online")
            connect = self._lookup("connect_records", struct.pack("<Q", int(decoded["client_cookie"])))
            faults = self._lookup("capture_faults", bytes(4))
            self._require(faults == bytes(8), "online capture faults nonzero")
            gate = self._read_gate_raw()
            self._require(not decode_gate(gate)["closed"], "online gate is closed")
        except ObservationError as exc:
            raise NativeError(str(exc)) from exc
        return dict(binding=binding, connect=connect, gate=gate, faults=faults)

    def stop_writers(self, timeout=2.0, clock=time.monotonic, sleep=time.sleep):
        """Close admission with the shared lock; boundedly await admitted writers.

        This observes a source-level protocol candidate, not callback/RCU drain
        or identity completeness. TEST_RUN executes BPF and is opt-in via runner.
        The timeout/count bound applies to polling after TEST_RUN returns; it is
        not a hard timeout for that syscall, lock acquisition, or lookup calls.
        """
        self._require(self.verified and self.profile in ("gated", "namespaced", "online", "executable-online", "scoped-executable-online"), "no verified gate")
        self._require(0 < timeout <= 10, "invalid stop timeout")
        packet = ctypes.create_string_buffer(bytes(64))
        options = TestRunOptions()  # Zero every optional field, including padding.
        options.sz = ctypes.sizeof(options)
        options.data_in = ctypes.cast(packet, ctypes.c_void_p)
        options.data_size_in, options.repeat = 64, 1
        # No packet/context output is requested. The opts API is available in
        # both the old lab library and modern libbpf, which removed the old API.
        self._call("bpf_prog_test_run_opts", self.program_fds["hoopa_stop"], ctypes.byref(options))
        self._require(options.retval == 0 and options.data_size_out <= 64,
                      "stop program rejected or output oversized")
        deadline = clock() + timeout
        for attempt in range(1001):
            gate = self.read_gate()
            self._require(gate["closed"], "stop program did not close admission")
            if gate["active"] == "0":
                return {"status": "closed_and_drained_observed", "gate": gate,
                        "polls": attempt + 1, "runtime_validation": "unverified"}
            if clock() >= deadline:
                break
            sleep(min(0.01, max(0, deadline - clock())))
        raise NativeError("writer stop deadline exceeded")

    def close_object(self):
        obj, self.obj = self.obj, None
        try:
            if obj is not None:
                self.offline.close(obj)
        finally:
            self.buffer = None
            self.maps.clear()
            self.programs.clear()
            self.map_fds.clear()
            self.program_fds.clear()
            self.map_ids.clear()
            self.program_ids.clear()
            self.program_targets.clear()
            self.target_btf_verified = False
            self.target_btf_object_id = None
            self.identity_scope_raw = self.scoped_capture_raw = None
            self.scope_frozen = False
            self.loaded = self.verified = self.validated = False
