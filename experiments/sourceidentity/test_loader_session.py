"""Fault injection for resource ownership/order; never touches kernel BPF APIs."""
import struct
import unittest
import ctypes
from unittest.mock import patch

from loader_session import observe


CONFIG = struct.pack("<QQQII", 17, 21, 23, 8080, 0)


class Backend:
    def __init__(self, failures=None):
        self.calls = []
        self.failures = failures or {}
        self.next_fd = 0
        self.raw = {"capture_faults": [(bytes(4), bytes(8))], "connect_records": [],
                    "established_records": [], "close_records": []}
        self.cancel = False
        self.cancel_on_create = False

    def call(self, name):
        self.calls.append(name)
        if name in self.failures:
            raise self.failures[name]

    def open_object(self): self.call("open")
    def load_object(self): self.call("load")
    def verify_loaded(self): self.call("verify_loaded"); return {"test": "fake"}
    def initialize_config(self, raw):
        assert raw == CONFIG
        self.call("initialize")
    def freeze_config(self, raw): self.call("freeze")
    def create_link(self, name, fd):
        assert fd == 9
        self.call("create:" + name)
        result, self.next_fd = self.next_fd, self.next_fd + 1
        self.cancel = self.cancel_on_create
        return result
    def verify_link(self, fd, name, cgroup):
        assert cgroup == 21
        self.call("verify:" + name)
        return {"test": "fake", "fd": fd}
    def close_link(self, fd): self.call("close:" + str(fd))
    def snapshot(self): self.call("snapshot"); return self.raw
    def close_object(self): self.call("close_object")


class SessionTests(unittest.TestCase):
    def run_session(self, backend, **kwargs):
        events = []
        def emit(event):
            backend.call("emit")
            events.append(event)
        def wait(duration):
            backend.call("wait")
        def check(): backend.call("target")
        result = observe(backend, CONFIG, 9, 0.1, emit, wait=wait,
                         check_target=check, **kwargs)
        return result, events

    def test_success_order_fd_zero_and_no_completeness_claim(self):
        backend = Backend()
        result, events = self.run_session(backend)
        self.assertEqual(backend.calls, ["open", "load", "verify_loaded", "initialize", "freeze", "target",
            "create:hoopa_connect", "verify:hoopa_connect", "create:hoopa_lifecycle", "verify:hoopa_lifecycle",
            "target", "emit", "wait", "close:1", "close:0", "snapshot", "close_object"])
        self.assertEqual(result["status"], "observed_unverified")
        self.assertEqual(result["quiescence"], "unverified")
        self.assertEqual(result["completeness"], "unverified")
        self.assertEqual(result["identity_capability"], "unverified")
        self.assertEqual(events[0]["event"], "attached_observer")
        self.assertEqual(len(events), 1)

    def test_each_pre_attach_failure_closes_object_without_marker(self):
        for stage in ("open", "load", "verify_loaded", "initialize", "freeze", "target", "create:hoopa_connect"):
            with self.subTest(stage=stage):
                backend = Backend({stage: ValueError(stage)})
                result, events = self.run_session(backend)
                self.assertEqual(result["status"], "failed")
                self.assertEqual(events, [])
                self.assertEqual(result["link_close_attempts"], [])
                self.assertNotIn("snapshot", backend.calls)
                self.assertEqual(backend.calls[-1], "close_object")

    def test_each_partial_attach_failure_closes_owned_links(self):
        cases = {"verify:hoopa_connect": ["close:0"], "create:hoopa_lifecycle": ["close:0"],
                 "verify:hoopa_lifecycle": ["close:1", "close:0"]}
        for stage, closes in cases.items():
            with self.subTest(stage=stage):
                backend = Backend({stage: ValueError(stage)})
                result, events = self.run_session(backend)
                self.assertEqual(events, [])
                self.assertEqual([c for c in backend.calls if c.startswith("close:")], closes)
                self.assertNotIn("snapshot", backend.calls)
                self.assertIsNone(result["diagnostic_snapshot"])

    def test_emit_and_wait_failures_close_both_without_scan(self):
        for stage in ("emit", "wait"):
            with self.subTest(stage=stage):
                backend = Backend({stage: ValueError(stage)})
                result, _ = self.run_session(backend)
                self.assertEqual(backend.calls[-3:], ["close:1", "close:0", "close_object"])
                self.assertEqual(result["status"], "failed")

    def test_primary_and_all_cleanup_errors_retained_without_retries(self):
        backend = Backend({"verify:hoopa_lifecycle": ValueError("primary"),
                           "close:1": OSError(4, "interrupted close"),
                           "close:0": OSError(9, "bad fd"),
                           "close_object": ValueError("object close")})
        result, _ = self.run_session(backend)
        self.assertEqual(len(result["errors"]), 4)
        self.assertEqual(result["errors"][0]["message"], "primary")
        self.assertEqual(backend.calls.count("close:0"), 1)
        self.assertEqual(backend.calls.count("close:1"), 1)
        self.assertEqual(result["errors"][1]["errno"], 4)
        self.assertNotIn("snapshot", backend.calls)

    def test_normal_cleanup_failure_skips_scan_and_still_closes_object(self):
        backend = Backend({"close:1": ValueError("failure")})
        result, _ = self.run_session(backend)
        self.assertEqual(result["status"], "failed")
        self.assertEqual(backend.calls[-3:], ["close:1", "close:0", "close_object"])

    def test_snapshot_failure_still_closes_object(self):
        backend = Backend({"snapshot": ValueError("scan failed")})
        result, _ = self.run_session(backend)
        self.assertEqual(result["errors"][0]["stage"], "diagnostic_snapshot")
        self.assertEqual(backend.calls[-1], "close_object")
        self.assertIsNone(result["diagnostic_snapshot"])

    def test_decoder_failure_preserves_raw(self):
        backend = Backend()
        backend.raw["connect_records"] = [(b"bad key", b"bad value")]
        result, _ = self.run_session(backend)
        self.assertEqual(result["status"], "failed")
        entry = result["diagnostic_snapshot"]["connect_records"][0]
        self.assertEqual(entry["value_hex"], b"bad value".hex())
        self.assertIn("decode_error", entry)

    def test_fault_counter_missing_invalid_or_nonzero_rejects(self):
        for entries in ([], [(bytes(4), b"short")], [(bytes(4), (1).to_bytes(8, "little"))]):
            with self.subTest(entries=entries):
                backend = Backend()
                backend.raw["capture_faults"] = entries
                result, _ = self.run_session(backend)
                self.assertEqual(result["status"], "failed")
                self.assertIsNotNone(result["diagnostic_snapshot"])

    def test_keyboard_interrupt_cleanup(self):
        backend = Backend({"wait": KeyboardInterrupt()})
        result, _ = self.run_session(backend)
        self.assertEqual(result["status"], "interrupted")
        self.assertEqual(backend.calls[-3:], ["close:1", "close:0", "close_object"])

    def test_deferred_cancellation_after_create_registers_fd_before_stop(self):
        backend = Backend()
        backend.cancel_on_create = True
        result, events = self.run_session(backend, cancelled=lambda: backend.cancel)
        self.assertEqual(result["status"], "interrupted")
        self.assertEqual(events, [])
        self.assertEqual(backend.calls[-3:], ["create:hoopa_connect", "close:0", "close_object"])

    def test_invalid_config_duration_reject_before_backend(self):
        for value in (0, -1, 61, float("nan"), float("inf"), True):
            backend = Backend()
            with self.assertRaises(ValueError):
                observe(backend, CONFIG, 9, value, lambda event: None)
            self.assertEqual(backend.calls, [])
        backend = Backend()
        with self.assertRaises(ValueError):
            observe(backend, bytes(32), 9, 1, lambda event: None)
        self.assertEqual(backend.calls, [])

    def test_target_change_after_both_attaches_has_no_marker(self):
        backend = Backend()
        checks = []
        def check():
            checks.append(1)
            if len(checks) == 2:
                raise ValueError("external target change")
        events = []
        result = observe(backend, CONFIG, 9, 1, events.append, check_target=check)
        self.assertEqual(events, [])
        self.assertEqual(result["status"], "failed")
        self.assertEqual(backend.calls[-3:], ["close:1", "close:0", "close_object"])

    def test_real_backend_and_controller_over_fake_cdll(self):
        from native_loader import NativeBackend
        from test_native_loader import Library
        library, links, closed = Library(), {}, []
        def create(program, target, attach, opts):
            self.assertTrue(library.frozen)
            fd = len(links)  # Includes descriptor zero.
            links[fd] = (program + 100, attach)
            return fd
        def info(fd, output, length):
            if fd not in links:
                return library.info(fd, output, length)
            raw = bytearray(256)
            struct.pack_into("<III", raw, 0, 3, fd + 500, links[fd][0])
            struct.pack_into("<QI", raw, 16, 21, links[fd][1])
            ctypes.memmove(output, bytes(raw), 256)
            ctypes.cast(length, ctypes.POINTER(ctypes.c_uint32))[0] = 256
            return 0
        library.overrides["bpf_link_create"] = create
        library.overrides["bpf_obj_get_info_by_fd"] = info
        with patch("inspect_object.ctypes.CDLL", return_value=library), \
                patch("native_loader.os.close", side_effect=closed.append), \
                patch("native_loader.NativeBackend._fd_access", side_effect=library.fd_access):
            backend = NativeBackend("fake", b"fake object")
            events = []
            result = observe(backend, CONFIG, 9, 1, events.append, wait=lambda seconds: None)
        self.assertEqual(result["status"], "observed_unverified")
        self.assertEqual(closed, [1, 0])
        self.assertEqual(len(events), 1)
        self.assertEqual(len(result["kernel_objects"]["maps"]), 5)
        self.assertFalse(backend.owned_links)
        self.assertIsNone(backend.obj)
        self.assertEqual(library.values["capture_config"][bytes(4)], CONFIG)


if __name__ == "__main__":
    unittest.main()
