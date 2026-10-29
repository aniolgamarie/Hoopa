"""Namespaced ABI and gated lifecycle acceptance using synthetic bytes/CDLL only."""
import json
import struct
import unittest
from dataclasses import replace
from unittest.mock import patch

from loader_session import diagnostic_snapshot, observe
from native_loader import NativeBackend, NativeError
from observations import decode_entry, expected_maps
from test_observations import CONFIG, KEY, MAX64, fixture, put
from test_gated_loader import GatedLibrary, gate
from test_loader_session import CONFIG as SESSION_CONFIG


def established(netns=CONFIG.netns):
    raw = fixture("established_records")
    put(raw, 24, 2)
    return bytes(raw) + struct.pack("<Q", netns)


class NamespacedDecoderTests(unittest.TestCase):
    def test_exact_map_abi_difference(self):
        old, new = expected_maps("gated"), expected_maps("namespaced")
        self.assertEqual(new["established_records"]["value_size"], 56)
        new["established_records"]["value_size"] = 48
        self.assertEqual(old, new)

    def test_schema_namespace_and_uint64_precision(self):
        config = replace(CONFIG, netns=MAX64)
        result = decode_entry("established_records", KEY, established(MAX64), config,
                              profile="namespaced")
        self.assertEqual(result["socket_netns_cookie"], str(MAX64))
        self.assertEqual(json.loads(json.dumps(result))["socket_netns_cookie"], str(MAX64))
        self.assertEqual(result["schema_version"], 2)
        self.assertEqual(result["association"], "unverified")
        for raw in (established(0), established(CONFIG.netns + 1),
                    bytes(put(bytearray(established()), 24, 1)), established()[:48]):
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                decode_entry("established_records", KEY, raw, CONFIG, profile="namespaced")
        for profile in ("lifecycle", "gated"):
            with self.subTest(profile=profile), self.assertRaises(ValueError):
                decode_entry("established_records", KEY, established(), CONFIG, profile=profile)
            with self.assertRaises(ValueError):
                decode_entry("established_records", KEY, established()[:48], CONFIG, profile=profile)

    def test_close_and_connect_keep_schema_one(self):
        for name in ("close_records", "connect_records"):
            result = decode_entry(name, KEY, bytes(fixture(name)), CONFIG, profile="namespaced")
            self.assertEqual(result["schema_version"], 1)
        self.assertEqual(decode_entry("close_records", KEY, bytes(fixture("close_records")),
                                     CONFIG, profile="namespaced")["socket_netns_cookie"], "unknown")

    def test_snapshot_profile_rejects_mixed_data_and_preserves_bytes(self):
        raw = dict(connect_records=[], close_records=[], capture_faults=[(bytes(4), bytes(8))],
                   established_records=[(KEY, established())])
        decoded, errors = diagnostic_snapshot(raw, CONFIG, profile="namespaced")
        self.assertEqual(errors, [])
        self.assertEqual(decoded["established_records"][0]["observation"]["schema_version"], 2)
        decoded, errors = diagnostic_snapshot(raw, CONFIG)
        self.assertEqual(len(errors), 1)
        self.assertEqual(decoded["established_records"][0]["value_hex"], established().hex())


class NamespacedNativeTests(unittest.TestCase):
    def setUp(self):
        self.library = GatedLibrary()
        self.library.metadata = expected_maps("namespaced")
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
        backend = NativeBackend("fake", b"object", profile="namespaced")
        if ready:
            backend.open_object(); backend.load_object(); backend.verify_loaded()
        return backend

    def test_gate_symbols_required_and_initial_gate_checked(self):
        for symbol in ("bpf_prog_test_run_opts", "bpf_map_lookup_elem_flags"):
            saved = getattr(self.library, symbol)
            delattr(self.library, symbol)
            with self.subTest(symbol=symbol), self.assertRaises(NativeError):
                self.backend(False)
            setattr(self.library, symbol, saved)
        self.assertEqual(self.library.calls, [])
        backend = self.backend()
        self.library.values["capture_gate"][bytes(4)] = gate(closed=1)
        with self.assertRaisesRegex(NativeError, "initial gate"):
            backend.initialize_config(SESSION_CONFIG)

    def test_exact_declarations_and_actual_ids(self):
        self.library.metadata["established_records"]["value_size"] = 48
        with self.assertRaisesRegex(NativeError, "ABI"):
            self.backend()
        self.library.metadata = expected_maps("namespaced")
        backend = self.backend()
        with self.assertRaises(NativeError):
            backend.create_link("hoopa_stop", 9)
        for name in self.library.programs:
            self.library.map_references[name] = []
            with self.subTest(name=name), self.assertRaisesRegex(NativeError, "map IDs"):
                backend.verify_loaded()
            del self.library.map_references[name]

    def test_explicit_profile_forces_stop_and_reads_full_record(self):
        backend = self.backend(False)
        raw = bytearray(established(23))
        struct.pack_into("<QQQ", raw, 0, 17, 19, 29)
        struct.pack_into("<I", raw, 44, 8080)
        def wait(seconds):
            self.library.events.append("wait")
            self.library.values["established_records"] = {struct.pack("<Q", 29): bytes(raw)}
        result = observe(backend, SESSION_CONFIG, 9, 1, lambda event: None, wait=wait,
                         profile="namespaced", gated=False)
        self.assertEqual(result["status"], "observed_unverified")
        self.assertEqual(result["profile"], "namespaced")
        entry = result["diagnostic_snapshot"]["established_records"][0]
        self.assertEqual(entry["observation"]["socket_netns_cookie"], "23")
        self.assertEqual(len(bytes.fromhex(entry["value_hex"])), 56)
        events = self.library.events[self.library.events.index("wait") + 1:]
        self.assertEqual(events[:4], ["bpf_prog_test_run_opts", "bpf_map_lookup_elem_flags", "close:1", "close:0"])
        self.assertGreater(events.index("bpf_map_get_next_key"), events.index("close:0"))
        self.assertEqual(events[-1], "bpf_object__close")
        self.assertEqual(len(self.library.links), 2)
        reads = [a for n, a in self.library.calls if n == "bpf_map_lookup_elem_flags"]
        self.assertTrue(reads)
        self.assertTrue(all(a[3] == 4 for a in reads))

    def test_stop_and_cleanup_errors_skip_scan_and_close_once(self):
        self.library.stop_ret = -5
        backend = self.backend(False)
        with patch("native_loader.os.close", side_effect=OSError(4, "close")) as close:
            result = observe(backend, SESSION_CONFIG, 9, 1, lambda event: None,
                             wait=lambda seconds: None, profile="namespaced")
        self.assertEqual(result["status"], "failed")
        self.assertEqual([e["stage"] for e in result["errors"]],
                         ["stop_writers", "close_link:hoopa_lifecycle", "close_link:hoopa_connect"])
        self.assertEqual(close.call_count, 2)
        self.assertIsNone(result["diagnostic_snapshot"])
        self.assertEqual(self.library.events[-1], "bpf_object__close")

    def test_snapshot_requires_closed_stable_gate(self):
        backend = self.backend()
        for values in ([gate()], [gate(1, 1, 1)], [gate(1), gate(1, 0, 1, 1)]):
            self.library.gates = list(values)
            with self.subTest(values=values), self.assertRaises(ValueError):
                backend.snapshot()

    def test_explicit_backend_profile_mismatch_rejects_before_open(self):
        backend = self.backend(False)
        with self.assertRaises(ValueError):
            observe(backend, SESSION_CONFIG, 9, 1, lambda event: None, profile="gated")
        self.assertEqual(self.library.calls, [])


if __name__ == "__main__":
    unittest.main()
