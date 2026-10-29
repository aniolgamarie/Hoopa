"""Exercise the real ctypes backend exclusively through fake CDLL symbols."""
import ctypes
import errno
import fcntl
import os
import struct
import unittest
from unittest.mock import patch

from native_loader import NativeBackend, NativeError, PROGRAM_TYPES
from observations import expected_maps


class Function:
    def __init__(self, call):
        self.call = call
    def __call__(self, *args):
        return self.call(*args)


class Library:
    def __init__(self):
        self.calls = []
        self.overrides = {}
        self.metadata = expected_maps("lifecycle")
        self.maps = {name: 20 + i for i, name in enumerate(self.metadata)}
        self.programs = {name: 40 + i for i, name in enumerate(PROGRAM_TYPES)}
        self.types = dict(PROGRAM_TYPES)
        self.values = {name: {} for name in self.maps}
        self.values["capture_config"][bytes(4)] = bytes(32)
        self.values["capture_faults"][bytes(4)] = bytes(8)
        self.frozen = False
        self.open_result = 10
        self.link_result = 90
        self.short = None
        self.bad_info = None
        self.program_maps = None
        self.get_next_error = None
        self.repeat = False
        self.access_overrides = {}
        names = ["bpf_object__open_mem", "libbpf_get_error", "bpf_object__close",
                 "bpf_object__next_program", "bpf_object__next_map", "bpf_program__name",
                 "bpf_program__section_name", "bpf_program__type", "bpf_program__expected_attach_type",
                 "bpf_map__name", "bpf_object__load", "bpf_object__find_map_by_name",
                 "bpf_object__find_program_by_name", "bpf_map__fd", "bpf_program__fd",
                 "bpf_program__set_type", "bpf_program__set_expected_attach_type",
                 "bpf_map_update_elem", "bpf_map_lookup_elem", "bpf_map_get_next_key",
                 "bpf_map_freeze", "bpf_link_create", "bpf_obj_get_info_by_fd"]
        names += ["bpf_map__" + key for key in next(iter(self.metadata.values()))]
        for name in names:
            setattr(self, name, Function(lambda *args, name=name: self.invoke(name, *args)))

    def invoke(self, name, *args):
        self.calls.append((name, args))
        if name in self.overrides:
            return self.overrides[name](*args)
        if name == "bpf_object__open_mem": return self.open_result
        if name == "libbpf_get_error": return -errno.EINVAL if args[0] == 999 else 0
        if name == "bpf_object__close": return None
        if name in ("bpf_object__next_program", "bpf_object__next_map"):
            pointers = list((self.programs if "program" in name else self.maps).values())
            index = 0 if args[1] is None else pointers.index(args[1]) + 1
            return pointers[index] if index < len(pointers) else None
        if name.startswith("bpf_program__"):
            program = next(key for key, value in self.programs.items() if value == args[0])
            if name.endswith("__name"): return program.encode()
            if name.endswith("__section_name"): return b"sockops" if program == "hoopa_lifecycle" else b"cgroup/connect4"
            if name.endswith("__type"): return self.types[program][0]
            if name.endswith("__expected_attach_type"): return self.types[program][1]
            if name.endswith("__set_type"):
                self.types[program] = (args[1], self.types[program][1]); return None
            if name.endswith("__set_expected_attach_type"):
                self.types[program] = (self.types[program][0], args[1]); return None
            if name.endswith("__fd"): return args[0]
        if name.startswith("bpf_map__"):
            mapname = next(key for key, value in self.maps.items() if value == args[0])
            field = name.split("__")[1]
            if field == "name": return mapname.encode()
            if field == "fd": return args[0]
            return self.metadata[mapname][field]
        if name == "bpf_object__find_map_by_name": return self.maps.get(args[1].decode())
        if name == "bpf_object__find_program_by_name": return self.programs.get(args[1].decode())
        if name == "bpf_object__load": return 0
        if name == "bpf_link_create": return self.link_result
        if name == "bpf_map_freeze": self.frozen = True; return 0
        if name == "bpf_obj_get_info_by_fd": return self.info(*args)
        mapname = next(key for key, value in self.maps.items() if value == args[0])
        meta, entries = self.metadata[mapname], self.values[mapname]
        if name == "bpf_map_get_next_key":
            if self.get_next_error: return -self.get_next_error
            keys = list(entries)
            previous = ctypes.string_at(args[1], meta["key_size"]) if args[1] is not None else None
            index = 0 if previous is None or self.repeat else keys.index(previous) + 1
            if index >= len(keys): return -errno.ENOENT
            ctypes.memmove(args[2], keys[index], meta["key_size"]); return 0
        key = ctypes.string_at(args[1], meta["key_size"])
        if name == "bpf_map_update_elem":
            if self.frozen: ctypes.set_errno(errno.EPERM); return -1
            entries[key] = ctypes.string_at(args[2], meta["value_size"]); return 0
        if name == "bpf_map_lookup_elem":
            if key not in entries: return -errno.ENOENT
            ctypes.memmove(args[2], entries[key], meta["value_size"]); return 0
        raise AssertionError(name)

    def fd_access(self, fd):
        if fd in self.access_overrides:
            value = self.access_overrides[fd]
            if isinstance(value, Exception): raise value
            return value
        name = next(name for name, value in self.maps.items() if value == fd)
        flags = self.metadata[name]["map_flags"]
        return os.O_RDONLY if flags & 8 else os.O_WRONLY if flags & 16 else os.O_RDWR

    def info(self, fd, output, length):
        raw = bytearray(256)
        if fd in self.maps.values():
            name = next(name for name, value in self.maps.items() if value == fd)
            meta = self.metadata[name]
            struct.pack_into("<6I", raw, 0, meta["type"], fd + 100, meta["key_size"], meta["value_size"], meta["max_entries"], meta["map_flags"] & ~(8 | 16))
            raw[24:24 + len(name[:15])] = name[:15].encode()
        elif fd in self.programs.values():
            name = next(name for name, value in self.programs.items() if value == fd)
            ids = [pointer + 100 for mapname, pointer in self.maps.items() if name == "hoopa_lifecycle" or mapname in ("capture_config", "capture_faults", "connect_records")]
            if self.program_maps is not None: ids = self.program_maps
            capacity, pointer = struct.unpack_from("<IQ", output.raw, 52)
            if pointer:
                array = ctypes.cast(pointer, ctypes.POINTER(ctypes.c_uint32))
                for i, ident in enumerate(ids[:capacity]): array[i] = ident
            struct.pack_into("<II", raw, 0, PROGRAM_TYPES[name][0], fd + 100)
            struct.pack_into("<I", raw, 52, len(ids))
            raw[64:64 + len(name[:15])] = name[:15].encode()
        else:
            struct.pack_into("<III", raw, 0, 3, 200, self.programs["hoopa_connect"] + 100)
            struct.pack_into("<QI", raw, 16, 123, 10)
        if self.bad_info is not None: self.bad_info(fd, raw)
        ctypes.memmove(output, bytes(raw), 256)
        ctypes.cast(length, ctypes.POINTER(ctypes.c_uint32))[0] = self.short if self.short is not None else 256
        return 0


class BackendTests(unittest.TestCase):
    def setUp(self):
        self.lib = Library()
        self.patch = patch("inspect_object.ctypes.CDLL", return_value=self.lib)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        access = patch("native_loader.NativeBackend._fd_access", side_effect=self.lib.fd_access)
        access.start(); self.addCleanup(access.stop)
        self.backend = NativeBackend("fake", b"object")

    def ready(self):
        self.backend.open_object(); self.backend.load_object()
        return self.backend.verify_loaded()

    def test_signatures_and_constructor_are_offline(self):
        self.assertEqual(self.lib.calls, [])
        self.assertIsNone(self.lib.bpf_program__set_type.restype)
        self.assertEqual(self.lib.bpf_link_create.argtypes, [ctypes.c_int] * 3 + [ctypes.c_void_p])
        self.assertEqual(self.lib.bpf_map_update_elem.argtypes[-1], ctypes.c_uint64)
        self.assertEqual(self.lib.bpf_obj_get_info_by_fd.argtypes[-1], ctypes.POINTER(ctypes.c_uint32))

    def test_kernel_flags_and_descriptor_access_are_separate(self):
        result = self.ready()
        readonly = result["map_access"]["connect_records"]
        self.assertEqual(readonly, dict(requested_flags=8, kernel_flags=0, access_mode=os.O_RDONLY))
        self.assertEqual(result["map_access"]["capture_config"]["access_mode"], os.O_RDWR)
        for name, wrong in (("connect_records", os.O_RDWR), ("capture_config", os.O_RDONLY)):
            with self.subTest(name=name):
                self.lib.access_overrides[self.lib.maps[name]] = wrong
                with self.assertRaisesRegex(NativeError, "descriptor access mismatch"):
                    self.backend.verify_loaded()
                self.lib.access_overrides.clear()
        self.lib.access_overrides[self.lib.maps["connect_records"]] = OSError(errno.EBADF, "bad fd")
        with self.assertRaises(OSError): self.backend.verify_loaded()

    def test_success_shared_map_ids_config_freeze_snapshot(self):
        result = self.ready()
        self.assertEqual(set(result["programs"]["hoopa_lifecycle"]["map_ids"]), set(result["maps"].values()))
        raw = bytes(range(32))
        self.backend.initialize_config(raw); self.backend.freeze_config(raw)
        self.assertEqual(self.backend.snapshot()["capture_faults"], [(bytes(4), bytes(8))])
        self.assertNotIn("capture_config", self.backend.snapshot())
        self.backend.close_object(); self.backend.close_object()
        self.assertEqual(sum(name == "bpf_object__close" for name, _ in self.lib.calls), 1)
        self.assertIsNone(self.backend.buffer)

    def test_invalid_open_not_closed(self):
        for value in (None, 999):
            with self.subTest(value=value):
                self.lib.open_result = value
                with self.assertRaises(NativeError): self.backend.open_object()
                self.backend.close_object()
        self.assertFalse(any(name == "bpf_object__close" for name, _ in self.lib.calls))

    def test_invalid_metadata_retains_object_for_close(self):
        self.lib.metadata["capture_config"]["value_size"] = 31
        with self.assertRaises(NativeError): self.backend.open_object()
        self.assertEqual(self.backend.obj, 10)
        self.backend.close_object()
        self.assertTrue(any(name == "bpf_object__close" for name, _ in self.lib.calls))

    def test_initial_state_failures(self):
        self.ready()
        for name, key, value in [("capture_config", bytes(4), b"x" * 32), ("capture_faults", bytes(4), b"x" * 8), ("connect_records", bytes(8), bytes(112))]:
            with self.subTest(name=name):
                old = dict(self.lib.values[name]); self.lib.values[name][key] = value
                with self.assertRaises(NativeError): self.backend.initialize_config(bytes(32))
                self.lib.values[name] = old

    def test_readback_and_freeze_failures(self):
        self.ready()
        self.lib.overrides["bpf_map_update_elem"] = lambda *args: 0
        with self.assertRaisesRegex(NativeError, "readback"): self.backend.initialize_config(b"a" * 32)
        with self.assertRaisesRegex(NativeError, "writable"): self.backend.freeze_config(bytes(32))
        self.lib.overrides["bpf_map_freeze"] = lambda *args: -errno.EIO
        with self.assertRaises(NativeError) as caught: self.backend.freeze_config(bytes(32))
        self.assertEqual((caught.exception.ret, caught.exception.errno), (-errno.EIO, errno.EIO))

    def test_short_info_rejected(self):
        self.backend.open_object(); self.backend.load_object()
        for size in (0, 39, 257):
            with self.subTest(size=size):
                self.lib.short = size
                with self.assertRaises(NativeError): self.backend.verify_loaded()

    def test_map_and_program_mismatch(self):
        self.backend.open_object(); self.backend.load_object()
        for modifier in (lambda fd, raw: struct.pack_into("<I", raw, 4, 0), lambda fd, raw: struct.pack_into("<I", raw, 0, 99), lambda fd, raw: raw.__setitem__(24, 120)):
            with self.subTest(modifier=modifier):
                self.lib.bad_info = modifier
                with self.assertRaises(NativeError): self.backend.verify_loaded()
        self.lib.bad_info = None
        for ids in ([], [120] * 3, list(range(17))):
            self.lib.program_maps = ids
            with self.assertRaises(NativeError): self.backend.verify_loaded()

    def test_program_short_and_growing_count(self):
        self.backend.open_object(); self.backend.load_object()
        self.lib.bad_info = lambda fd, raw: setattr(self.lib, "short", 79) if fd in self.lib.programs.values() else None
        with self.assertRaises(NativeError): self.backend.verify_loaded()
        self.lib.bad_info = None; self.lib.short = None
        calls = [0]
        original = self.lib.info
        def growing(fd, output, length):
            result = original(fd, output, length)
            if fd in self.lib.programs.values():
                calls[0] += 1
                if calls[0] == 2: struct.pack_into("<I", output, 52, 16)
            return result
        self.lib.overrides["bpf_obj_get_info_by_fd"] = growing
        with self.assertRaisesRegex(NativeError, "grew"): self.backend.verify_loaded()

    def test_enumeration_errors_and_bound(self):
        self.ready()
        self.lib.values["connect_records"][bytes(8)] = bytes(112)
        self.lib.repeat = True
        with self.assertRaisesRegex(NativeError, "repeated"): self.backend.snapshot()
        self.lib.repeat = False
        for error in (errno.EIO, errno.EPERM):
            self.lib.get_next_error = error
            with self.assertRaises(NativeError) as caught: self.backend.snapshot()
            self.assertEqual(caught.exception.errno, error)
        self.lib.get_next_error = errno.ENOENT
        self.assertTrue(all(not values for values in self.backend.snapshot().values()))

    def test_direct_link_fd_zero_and_single_close(self):
        self.ready(); self.lib.link_result = 0
        fd = self.backend.create_link("hoopa_connect", 77)
        self.assertEqual(fd, 0)
        self.backend.verify_link(fd, "hoopa_connect", 123)
        link_call = next(args for name, args in self.lib.calls if name == "bpf_link_create")
        self.assertEqual(link_call, (40, 77, 10, None))
        with patch("native_loader.os.close", side_effect=OSError(errno.EINTR, "interrupted")) as close:
            with self.assertRaises(NativeError): self.backend.close_link(fd)
            with self.assertRaises(NativeError): self.backend.close_link(fd)
            close.assert_called_once_with(0)

    def test_link_info_mismatch_and_short(self):
        self.ready(); fd = self.backend.create_link("hoopa_connect", 77)
        for offset, format_, value in [(0, "I", 1), (4, "I", 0), (8, "I", 55), (16, "Q", 9), (24, "I", 3)]:
            with self.subTest(offset=offset):
                self.lib.bad_info = lambda fd, raw, offset=offset, format_=format_, value=value: struct.pack_into("<" + format_, raw, offset, value)
                with self.assertRaises(NativeError): self.backend.verify_link(fd, "hoopa_connect", 123)
        self.lib.bad_info = None; self.lib.short = 27
        with self.assertRaises(NativeError): self.backend.verify_link(fd, "hoopa_connect", 123)

    def test_enumeration_capacity(self):
        self.ready()
        entries = self.lib.values["connect_records"]
        for key in range(1025): entries[struct.pack("<Q", key)] = bytes(112)
        with self.assertRaisesRegex(NativeError, "exceeded bound"): self.backend.snapshot()
        del entries[struct.pack("<Q", 1024)]
        self.assertEqual(len(self.backend.snapshot()["connect_records"]), 1024)

    def test_freeze_readback_and_unexpected_error(self):
        self.ready()
        self.backend.initialize_config(b"a" * 32)
        with self.assertRaisesRegex(NativeError, "readback"): self.backend.freeze_config(b"b" * 32)
        self.lib.overrides["bpf_map_update_elem"] = lambda *args: -errno.EIO
        with self.assertRaises(NativeError) as caught: self.backend.freeze_config(b"a" * 32)
        self.assertEqual(caught.exception.errno, errno.EIO)

    def test_setter_mismatch_load_error_and_fd_zero(self):
        self.backend.open_object()
        self.lib.types["hoopa_connect"] = (0, 0)
        self.lib.overrides["bpf_program__set_type"] = lambda *args: None
        with self.assertRaisesRegex(NativeError, "setters"): self.backend.load_object()
        self.assertFalse(any(name == "bpf_object__load" for name, _ in self.lib.calls))
        del self.lib.overrides["bpf_program__set_type"]
        self.lib.overrides["bpf_object__load"] = lambda *args: -errno.EACCES
        with self.assertRaises(NativeError) as caught: self.backend.load_object()
        self.assertEqual(caught.exception.errno, errno.EACCES)
        self.lib.overrides["bpf_object__load"] = lambda *args: 0
        self.lib.overrides["bpf_map__fd"] = lambda *args: 0
        self.lib.overrides["bpf_program__fd"] = lambda *args: 0
        self.backend.load_object()
        self.assertEqual(set(self.backend.map_fds.values()), {0})
        self.assertEqual(set(self.backend.program_fds.values()), {0})

    def test_find_pointer_error_and_invalid_object_cannot_load(self):
        self.lib.overrides["bpf_object__find_map_by_name"] = lambda *args: 999
        with self.assertRaises(NativeError): self.backend.open_object()
        with self.assertRaises(NativeError): self.backend.load_object()
        self.backend.close_object()
        self.assertEqual(sum(name == "bpf_object__close" for name, _ in self.lib.calls), 1)

    def test_map_duplicate_id_and_program_metadata(self):
        self.backend.open_object(); self.backend.load_object()
        def duplicate(fd, raw):
            if fd in self.lib.maps.values(): struct.pack_into("<I", raw, 4, 100)
        self.lib.bad_info = duplicate
        with self.assertRaisesRegex(NativeError, "duplicate"): self.backend.verify_loaded()
        for offset, value in ((0, 99), (4, 0), (64, 120)):
            def bad(fd, raw, offset=offset, value=value):
                if fd in self.lib.programs.values():
                    if offset == 64: raw[offset] = value
                    else: struct.pack_into("<I", raw, offset, value)
            self.lib.bad_info = bad
            with self.assertRaisesRegex(NativeError, "program metadata"): self.backend.verify_loaded()

    def test_errno_modes_and_no_fallback(self):
        self.ready()
        def failure(*args): ctypes.set_errno(errno.EOPNOTSUPP); return -1
        self.lib.overrides["bpf_link_create"] = failure
        with self.assertRaises(NativeError) as caught: self.backend.create_link("hoopa_connect", 77)
        self.assertEqual(caught.exception.errno, errno.EOPNOTSUPP)
        self.assertEqual(self.backend.owned_links, set())
        self.assertFalse(any("attach" in name and "expected" not in name for name, _ in self.lib.calls))

    def test_link_cgroup_u64_serializes_without_precision_loss(self):
        import json
        self.ready()
        fd = self.backend.create_link("hoopa_connect", 77)
        cgroup = 2**64 - 1
        self.lib.bad_info = lambda fd, raw: struct.pack_into("<Q", raw, 16, cgroup)
        result = self.backend.verify_link(fd, "hoopa_connect", cgroup)
        self.assertEqual(json.loads(json.dumps(result))["cgroup_id"], str(cgroup))

    def test_registration_failure_closes_unreturned_fd_once(self):
        self.ready()
        class FailedSet(set):
            def add(self, fd): raise MemoryError("ownership allocation")
        self.backend.owned_links = FailedSet()
        with patch("native_loader.os.close") as close:
            with self.assertRaisesRegex(MemoryError, "ownership allocation"):
                self.backend.create_link("hoopa_connect", 77)
            close.assert_called_once_with(90)
        with patch("native_loader.os.close", side_effect=OSError(errno.EIO, "close error")) as close:
            with self.assertRaises(NativeError) as caught:
                self.backend.create_link("hoopa_connect", 77)
            self.assertIsInstance(caught.exception.__cause__, MemoryError)
            self.assertIn("close failed", str(caught.exception))
            close.assert_called_once_with(90)


class FDFlagTests(unittest.TestCase):
    def test_read_only_f_getfl_masks_other_flags_and_propagates_errors(self):
        with patch("native_loader.fcntl.fcntl", return_value=os.O_RDONLY | os.O_NONBLOCK) as call:
            self.assertEqual(NativeBackend._fd_access(321), os.O_RDONLY)
            call.assert_called_once_with(321, fcntl.F_GETFL)
        with patch("native_loader.fcntl.fcntl", side_effect=OSError(errno.EBADF, "bad fd")):
            with self.assertRaises(OSError): NativeBackend._fd_access(321)


if __name__ == "__main__": unittest.main()
