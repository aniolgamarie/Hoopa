"""Gated native wrapper/session acceptance through fake CDLL symbols only."""
import ctypes
import errno
import struct
import unittest
from unittest.mock import patch

from loader_session import observe
from native_loader import NativeBackend, NativeError, TestRunOptions
from observations import decode_gate, expected_maps
from test_loader_session import CONFIG
from test_native_loader import Function, Library


def gate(closed=0, active=0, admitted=0, completed=0, poison=0, lock=0, reserved=0):
    return struct.pack("<IIQQQII", lock, closed, active, admitted, completed, poison, reserved)


class GatedLibrary(Library):
    def __init__(self):
        super().__init__()
        self.metadata = expected_maps("gated")
        self.maps["capture_gate"] = 25
        self.values["capture_gate"] = {bytes(4): gate()}
        self.programs["hoopa_stop"] = 42
        self.types["hoopa_stop"] = (3, 0)
        self.gates = []
        self.stop_ret = self.retval = self.stop_errno = 0
        self.output_size = 64
        self.close_gate = True
        self.map_references = {}
        self.links = {}
        self.events = []
        for name in ("bpf_map_lookup_elem_flags", "bpf_prog_test_run_opts"):
            setattr(self, name, Function(lambda *args, name=name: self.invoke(name, *args)))

    def invoke(self, name, *args):
        self.events.append(name)
        if name == "bpf_program__section_name" and args[0] == 42:
            self.calls.append((name, args))
            return b"tc"
        if name == "bpf_prog_test_run_opts":
            self.calls.append((name, args))
            options = ctypes.cast(args[1], ctypes.POINTER(TestRunOptions)).contents
            self.stop_options = {field: getattr(options, field) for field, _ in TestRunOptions._fields_}
            self.stop_packet = ctypes.string_at(options.data_in, options.data_size_in)
            options.data_size_out = self.output_size
            options.retval = self.retval
            ctypes.set_errno(self.stop_errno)
            if self.close_gate:
                self.values["capture_gate"][bytes(4)] = gate(closed=1)
            return self.stop_ret
        if name == "bpf_map_lookup_elem_flags":
            self.calls.append((name, args))
            value = self.gates.pop(0) if self.gates else self.values["capture_gate"][bytes(4)]
            if isinstance(value, int):
                return value
            ctypes.memmove(args[2], value, 40)
            return 0
        if name == "bpf_link_create":
            self.calls.append((name, args))
            fd = len(self.links)
            self.links[fd] = (args[0], args[2])
            return fd
        return super().invoke(name, *args)

    def info(self, fd, output, length):
        if fd in self.maps.values():
            return super().info(fd, output, length)
        raw = bytearray(256)
        if fd in self.programs.values():
            name = next(name for name, pointer in self.programs.items() if pointer == fd)
            names = {"capture_gate"} if name == "hoopa_stop" else set(self.maps)
            if name == "hoopa_connect":
                names = {"capture_config", "capture_faults", "connect_records", "capture_gate"}
            ids = self.map_references.get(name, [self.maps[key] + 100 for key in sorted(names)])
            capacity, pointer = struct.unpack_from("<IQ", output.raw, 52)
            if pointer:
                array = ctypes.cast(pointer, ctypes.POINTER(ctypes.c_uint32))
                for index, ident in enumerate(ids[:capacity]):
                    array[index] = ident
            struct.pack_into("<II", raw, 0, self.types[name][0], fd + 100)
            struct.pack_into("<I", raw, 52, len(ids))
            raw[64:64 + len(name)] = name.encode()
        else:
            program, attach = self.links[fd]
            struct.pack_into("<III", raw, 0, 3, fd + 500, program + 100)
            struct.pack_into("<QI", raw, 16, 21, attach)
        ctypes.memmove(output, bytes(raw), 256)
        ctypes.cast(length, ctypes.POINTER(ctypes.c_uint32))[0] = 256
        return 0


class GatedLoaderTests(unittest.TestCase):
    def setUp(self):
        self.library = GatedLibrary()
        patcher = patch("inspect_object.ctypes.CDLL", side_effect=lambda *a, **k: self.library)
        patcher.start()
        self.addCleanup(patcher.stop)
        access = patch("native_loader.NativeBackend._fd_access",
                       side_effect=lambda fd: self.library.fd_access(fd))
        access.start(); self.addCleanup(access.stop)
        closer = patch("native_loader.os.close", side_effect=lambda fd: self.library.events.append("close:" + str(fd)))
        closer.start()
        self.addCleanup(closer.stop)

    def backend(self, ready=True):
        backend = NativeBackend("fake", b"fake object", profile="gated")
        if ready:
            backend.open_object()
            backend.load_object()
            backend.verify_loaded()
        return backend

    def test_decode_strict_abi_counts_and_uint64_strings(self):
        maximum = 2 ** 64 - 1
        decoded = decode_gate(gate(1, 0, maximum, maximum))
        self.assertEqual(decoded["admitted"], str(maximum))
        self.assertEqual(decoded["completed"], str(maximum))
        for raw in (bytes(39), gate(lock=1), gate(closed=2), gate(reserved=1),
                    gate(poison=1), gate(active=1), gate(admitted=1, completed=2)):
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                decode_gate(raw)

    def test_profile_and_required_symbols_fail_without_native_calls(self):
        with self.assertRaises(NativeError):
            NativeBackend("fake", b"object", profile="capture")
        for symbol in ("bpf_prog_test_run_opts", "bpf_map_lookup_elem_flags"):
            saved = getattr(self.library, symbol)
            delattr(self.library, symbol)
            with self.subTest(symbol=symbol), self.assertRaises(NativeError):
                self.backend(ready=False)
            setattr(self.library, symbol, saved)
        self.assertEqual(self.library.calls, [])

    def test_exact_program_map_references_and_stop_never_attaches(self):
        backend = self.backend()
        with self.assertRaises(NativeError):
            backend.create_link("hoopa_stop", 9)
        self.assertFalse(any(name == "bpf_link_create" for name, _ in self.library.calls))
        for name in ("hoopa_stop", "hoopa_connect", "hoopa_lifecycle"):
            self.library.map_references[name] = []
            with self.subTest(name=name), self.assertRaisesRegex(NativeError, "map IDs"):
                backend.verify_loaded()
            del self.library.map_references[name]

    def test_initial_gate_must_be_all_zero(self):
        backend = self.backend()
        for raw in (gate(closed=1), gate(active=1, admitted=1), gate(admitted=1, completed=1),
                    gate(poison=1), gate(lock=1), gate(reserved=1)):
            self.library.values["capture_gate"][bytes(4)] = raw
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                backend.initialize_config(CONFIG)
        self.assertEqual(self.library.values["capture_config"][bytes(4)], bytes(32))

    def test_stop_drains_late_active_and_uses_locked_reads_and_bounded_input(self):
        backend = self.backend()
        self.library.gates = [gate(1, 1, 2, 1), gate(1, 0, 2, 2)]
        result = backend.stop_writers(clock=lambda: 0, sleep=lambda seconds: None)
        self.assertEqual(result["polls"], 2)
        self.assertEqual(result["gate"]["completed"], "2")
        call = next(args for name, args in self.library.calls if name == "bpf_prog_test_run_opts")
        self.assertEqual(call[0], 42)
        self.assertEqual(len(call), 2)
        expected = {name: None if name in ("data_out", "ctx_in", "ctx_out") else 0
                    for name, _ in TestRunOptions._fields_}
        expected.update(sz=80, data_in=self.library.stop_options["data_in"],
                        data_size_in=64, repeat=1)
        self.assertTrue(expected["data_in"])
        self.assertEqual(self.library.stop_options, expected)
        self.assertEqual(self.library.stop_packet, bytes(64))
        reads = [args for name, args in self.library.calls if name == "bpf_map_lookup_elem_flags"]
        self.assertEqual([args[3] for args in reads], [4, 4])
        self.assertFalse(any(name == "bpf_map_lookup_elem" and args[0] == 25
                             for name, args in self.library.calls))

    def test_stop_rejects_program_and_read_failures_without_fallback(self):
        cases = ({"stop_ret": -errno.EIO}, {"retval": 1}, {"output_size": 65},
                 {"close_gate": False}, {"gates": [gate(1, poison=1)]},
                 {"gates": [gate(1, active=1)]}, {"gates": [-errno.EIO]})
        for changes in cases:
            with self.subTest(changes=changes):
                self.library = GatedLibrary()
                backend = self.backend()
                for key, value in changes.items():
                    setattr(self.library, key, value)
                with self.assertRaises(ValueError):
                    backend.stop_writers(clock=lambda: 0, sleep=lambda seconds: None)
                self.assertEqual(sum(name == "bpf_prog_test_run_opts" for name, _ in self.library.calls), 1)
                self.assertFalse(any(name == "bpf_link_create" for name, _ in self.library.calls))

    def test_options_c_abi_and_error_conventions(self):
        # Native C probe against pinned libbpf 1.6.2 header on this LP64 lab.
        offsets = (0, 8, 16, 24, 28, 32, 40, 48, 52, 56, 60, 64, 68, 72, 76)
        self.assertEqual(ctypes.sizeof(TestRunOptions), 80)
        self.assertEqual(tuple(getattr(TestRunOptions, name).offset
                               for name, _ in TestRunOptions._fields_), offsets)
        for ret, saved_errno, expected_errno in ((-1, errno.EACCES, errno.EACCES),
                                                 (-errno.EIO, 0, errno.EIO), (1, 0, 0)):
            with self.subTest(ret=ret):
                self.library = GatedLibrary()
                backend = self.backend()
                self.library.stop_ret, self.library.stop_errno = ret, saved_errno
                with self.assertRaises(NativeError) as caught:
                    backend.stop_writers()
                self.assertEqual(caught.exception.ret, ret)
                self.assertEqual(caught.exception.errno, expected_errno)
                self.assertNotIn("bpf_map_lookup_elem_flags", self.library.events)

    def test_stop_deadline_and_hard_poll_bound(self):
        for hard_bound in (False, True):
            with self.subTest(hard_bound=hard_bound):
                self.library = GatedLibrary()
                backend = self.backend()
                active = gate(1, 1, 1, 0)
                self.library.gates = [active] * 1002
                clock = (lambda: 0) if hard_bound else iter((0, 2)).__next__
                with self.assertRaisesRegex(NativeError, "deadline"):
                    backend.stop_writers(clock=clock, sleep=lambda seconds: None)
                reads = [name for name, _ in self.library.calls if name == "bpf_map_lookup_elem_flags"]
                self.assertEqual(len(reads), 1001 if hard_bound else 1)

    def test_snapshot_rejects_undrained_or_drifting_gate(self):
        backend = self.backend()
        for reads in ([gate()], [gate(1, 1, 1, 0)],
                      [gate(1, 0, 1, 1), gate(1, 0, 2, 2)], [gate(1), -errno.EIO]):
            self.library.gates = list(reads)
            with self.subTest(reads=reads), self.assertRaises(ValueError):
                backend.snapshot()

    def test_real_backend_session_stop_before_close_snapshot_after_close(self):
        backend = self.backend(ready=False)
        events = []
        def wait(seconds):
            self.library.events.append("wait")
            self.library.gates = [gate(1, 0, 3, 3)] * 3
        result = observe(backend, CONFIG, 9, 1, events.append, wait=wait, gated=True)
        self.assertEqual(result["status"], "observed_unverified")
        self.assertEqual(result["writer_stop"]["gate"]["admitted"], "3")
        self.assertEqual(len(events), 1)
        after_wait = self.library.events[self.library.events.index("wait") + 1:]
        self.assertEqual(after_wait[:4], ["bpf_prog_test_run_opts", "bpf_map_lookup_elem_flags", "close:1", "close:0"])
        self.assertGreater(after_wait.index("bpf_map_get_next_key"), after_wait.index("close:0"))
        self.assertEqual(after_wait[-1], "bpf_object__close")
        self.assertIsNotNone(result["diagnostic_snapshot"])
        self.assertEqual(len(self.library.links), 2)
        self.assertFalse(backend.owned_links)
        self.assertIsNone(backend.obj)
        for field in ("quiescence", "completeness", "association", "identity_capability"):
            self.assertEqual(result[field], "unverified")

    def test_partial_attach_failure_stops_before_closing_first_link(self):
        backend = self.backend(ready=False)
        def info(fd, output, length):
            if fd == 0:
                self.library.events.append("link_verification_failed")
                return -errno.EIO
            return self.library.info(fd, output, length)
        self.library.overrides["bpf_obj_get_info_by_fd"] = info
        events = []
        result = observe(backend, CONFIG, 9, 1, events.append,
                         wait=lambda seconds: None, gated=True)
        self.assertEqual(result["status"], "failed")
        self.assertEqual(result["errors"][0]["stage"], "verify_link:hoopa_connect")
        self.assertEqual(events, [])
        tail = self.library.events[self.library.events.index("link_verification_failed") + 1:]
        self.assertEqual(tail, ["bpf_prog_test_run_opts", "bpf_map_lookup_elem_flags",
                                "close:0", "bpf_object__close"])
        self.assertIsNone(result["diagnostic_snapshot"])
        self.assertFalse(backend.owned_links)

    def test_session_failure_interrupt_and_stop_failure_close_without_scan(self):
        for failure in (ValueError("wait failed"), KeyboardInterrupt(), None):
            with self.subTest(failure=type(failure).__name__):
                self.library = GatedLibrary()
                backend = self.backend(ready=False)
                def wait(seconds):
                    self.library.events.append("wait")
                    if failure is None:
                        self.library.stop_ret = -errno.EIO
                    else:
                        raise failure
                result = observe(backend, CONFIG, 9, 1, lambda event: None, wait=wait, gated=True)
                self.assertEqual(result["status"], "interrupted" if isinstance(failure, KeyboardInterrupt) else "failed")
                after_wait = self.library.events[self.library.events.index("wait") + 1:]
                self.assertEqual(after_wait[0], "bpf_prog_test_run_opts")
                self.assertEqual(after_wait[-3:], ["close:1", "close:0", "bpf_object__close"])
                self.assertNotIn("bpf_map_get_next_key", after_wait)
                self.assertIsNone(result["diagnostic_snapshot"])
                self.assertFalse(backend.owned_links)
                if failure is None:
                    self.assertEqual(result["errors"][0]["stage"], "stop_writers")


if __name__ == "__main__":
    unittest.main()
