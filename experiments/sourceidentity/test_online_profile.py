"""Opt-in online ABI/raw lookup checks; fake libraries only, no BPF load."""
import contextlib
import ctypes
import errno
import hashlib
import io
import json
import os
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch

import inspect_object
import prepare
import run_observer
import test_prepare
from loader_session import observe
from native_loader import NativeBackend, NativeError, TestRunOptions
from observations import ObservationError, decode_entry, expected_maps
from test_gated_loader import GatedLibrary, gate
from test_inspect_object import FakeLibrary
from test_observations import CONFIG, COOKIE, GEN, fixture
from test_prepare import elf_fixture

ACCEPTED = COOKIE - 1
LOOPBACK = int.from_bytes(b"\x7f\0\0\1", "little")
CONFIG_RAW = struct.pack("<QQQII", CONFIG.generation, CONFIG.cgroup, CONFIG.netns,
                         CONFIG.destination_port, 0)
BINDING_FIELDS = (GEN, CONFIG.netns, ACCEPTED, COOKIE, LOOPBACK, LOOPBACK,
                  45678, CONFIG.destination_port, 1, 0)
BINDING = struct.pack("<QQQQIIIIII", *BINDING_FIELDS)
TUPLE = struct.pack("<QQIIII", GEN, CONFIG.netns, LOOPBACK, LOOPBACK,
                    45678, CONFIG.destination_port)
KEY = struct.pack("<Q", ACCEPTED)


class OnlineABITests(unittest.TestCase):
    def test_exact_metadata_programs_and_legacy_session_exclusion(self):
        self.assertEqual(expected_maps("online"), {
            "capture_config": dict(type=2, key_size=4, value_size=32, max_entries=1, map_flags=128),
            "connect_records": dict(type=1, key_size=8, value_size=112, max_entries=1024, map_flags=8),
            "capture_faults": dict(type=2, key_size=4, value_size=8, max_entries=1, map_flags=8),
            "capture_gate": dict(type=2, key_size=4, value_size=40, max_entries=1, map_flags=8),
            "online_tuples": dict(type=1, key_size=32, value_size=8, max_entries=1024, map_flags=8),
            "online_bindings": dict(type=1, key_size=8, value_size=56, max_entries=1024, map_flags=8),
        })
        self.assertEqual(inspect_object.PROGRAMS["online"], inspect_object.PROGRAMS["namespaced"])
        self.assertNotIn("online_tuples", expected_maps("namespaced"))
        with self.assertRaisesRegex(ValueError, "unsupported session profile"):
            observe(object(), CONFIG_RAW, 9, 1, lambda event: None, profile="online")
        argv = ["--allow-bpf-load", "--profile", "online", "--object", "/fake/object",
                "--object-sha256", "a" * 64, "--libbpf", "/fake/lib", "--libbpf-sha256", "b" * 64,
                "--cgroup", "/fake/cgroup", "--generation", "1", "--netns-cookie", "2",
                "--destination-port", "7890", "--output-parent", "/fake/out"]
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            run_observer.parser().parse_args(argv)

    def test_binding_and_tuple_precision_and_capabilities(self):
        binding = decode_entry("online_bindings", KEY, BINDING, CONFIG, profile="online")
        tuples = decode_entry("online_tuples", TUPLE, struct.pack("<Q", COOKIE), CONFIG, profile="online")
        for item in (binding, tuples):
            self.assertEqual(item["generation"], str(GEN))
            self.assertEqual(item["socket_netns_cookie"], str(CONFIG.netns))
            self.assertEqual(item["client_cookie"], str(COOKIE))
            self.assertEqual(item["client_ip4"], "127.0.0.1")
            self.assertEqual(item["server_ip4"], "127.0.0.1")
            self.assertEqual((item["client_port"], item["server_port"]), (45678, CONFIG.destination_port))
            for field in ("evidence_status", "association", "identity_capability"):
                self.assertEqual(item[field], "unverified")
        self.assertEqual(binding["accepted_cookie"], str(ACCEPTED))
        self.assertEqual(binding["schema_version"], 1)
        connect = decode_entry("connect_records", struct.pack("<Q", COOKIE),
                               bytes(fixture("connect_records")), CONFIG, profile="online")
        self.assertEqual(connect["client_cookie"], str(COOKIE))

    def test_binding_rejects_all_wrong_coordinates_schema_and_key(self):
        changes = ((0, GEN + 1), (1, CONFIG.netns + 1), (2, ACCEPTED + 1), (2, 0), (3, 0),
                   (4, 0x7f000001), (5, 0), (6, 0), (6, 65536),
                   (7, CONFIG.destination_port + 1), (8, 0), (8, 2), (9, 1))
        for index, value in changes:
            fields = list(BINDING_FIELDS); fields[index] = value
            with self.subTest(index=index, value=value), self.assertRaises(ObservationError):
                decode_entry("online_bindings", KEY, struct.pack("<QQQQIIIIII", *fields),
                             CONFIG, profile="online")
        for key in (bytes(8), KEY[:-1], KEY + b"x", bytearray(KEY)):
            with self.subTest(key=key), self.assertRaises(ObservationError):
                decode_entry("online_bindings", key, BINDING, CONFIG, profile="online")
        for value in (BINDING[:-1], BINDING + b"x", bytearray(BINDING)):
            with self.subTest(value=value), self.assertRaises(ObservationError):
                decode_entry("online_bindings", KEY, value, CONFIG, profile="online")

    def test_tuple_rejects_malformed_values_and_endpoints(self):
        fields = list(struct.unpack("<QQIIII", TUPLE))
        for index, value in ((0, 0), (1, 0), (2, 0x7f000001), (3, 0), (4, 0), (4, 65536), (5, 1)):
            changed = list(fields); changed[index] = value
            with self.subTest(index=index), self.assertRaises(ObservationError):
                decode_entry("online_tuples", struct.pack("<QQIIII", *changed),
                             struct.pack("<Q", COOKIE), CONFIG, profile="online")
        for key, value in ((TUPLE[:-1], bytes(8)), (TUPLE + b"x", bytes(8)),
                           (TUPLE, bytes(8)), (TUPLE, bytes(7)), (TUPLE, bytes(9))):
            with self.subTest(key=key, value=value), self.assertRaises(ObservationError):
                decode_entry("online_tuples", key, value, CONFIG, profile="online")
        with self.assertRaises(ObservationError):
            decode_entry("online_bindings", KEY, BINDING, CONFIG, profile="namespaced")
        with self.assertRaises(ObservationError):
            decode_entry("established_records", KEY, bytes(56), CONFIG, profile="online")

    def test_online_preparation_snapshots_only_two_sources_before_tools(self):
        case = test_prepare.PrepareTests(); case.setUp(); self.addCleanup(case.doCleanups)
        online = case.root / "online_snapshot.bpf.c"
        online.write_text('#include "connect_snapshot.bpf.c"\n/* online only */\n')
        originals = {str(p): p.read_text() for p in (online, case.source)}
        case.args.profile = "online"
        case.fixture.write_bytes(elf_fixture(prepare.REQUIRED_SECTIONS | {"sockops", "tc"}))
        case.mutation.write_text(json.dumps({str(p): "changed" for p in (online, case.source)}))
        with patch.object(prepare, "ONLINE_SOURCE", online):
            code, manifest, error = case.run_prepare()
        self.assertEqual((code, error), (0, ""))
        self.assertEqual([Path(x["original_path"]) for x in manifest["sources"]], [online, case.source])
        self.assertEqual(Path(manifest["steps"][-1]["argv"][-3]).name, online.name)
        for item in manifest["sources"]:
            self.assertEqual(Path(item["path"]).read_text(), originals[item["original_path"]])
        for section in ("cgroup/connect4", "sockops", "tc"):
            case.fixture.write_bytes(elf_fixture((prepare.REQUIRED_SECTIONS | {"sockops", "tc"}) - {section}))
            with self.subTest(section=section), self.assertRaises(prepare.PreparationError):
                prepare.check_elf(case.fixture, "online")

    def test_online_inspector_is_offline_and_rejects_wrong_map(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); obj = root / "object"; library = root / "libbpf.so"
            raw = bytearray(64); raw[:7] = b"\x7fELF\x02\x01\x01"; struct.pack_into("<HHI", raw, 16, 1, 247, 1)
            obj.write_bytes(raw); library.write_bytes(b"fake, never loaded")
            def inspect(fake):
                with patch("inspect_object.ctypes.CDLL", return_value=fake):
                    return inspect_object.inspect_object("online", str(obj), hashlib.sha256(raw).hexdigest(),
                                                         str(library), hashlib.sha256(library.read_bytes()).hexdigest())
            fake = FakeLibrary("online"); result = inspect(fake)
            self.assertEqual(result["maps"], expected_maps("online"))
            self.assertEqual(result["runtime"], "unverified")
            self.assertNotIn("bpf_object__load", fake.lookups)
            fake.maps[next(p for p, m in fake.maps.items() if m["name"] == b"online_bindings")]["value_size"] = 55
            with self.assertRaisesRegex(inspect_object.InspectionError, "map ABI"):
                inspect(fake)


class OnlineLibrary(GatedLibrary):
    def __init__(self):
        super().__init__()
        self.metadata = expected_maps("online")
        self.maps = {name: 20 + i for i, name in enumerate(self.metadata)}
        self.values = {name: {} for name in self.maps}
        self.values["capture_config"][bytes(4)] = bytes(32)
        self.values["capture_faults"][bytes(4)] = bytes(8)
        self.values["capture_gate"][bytes(4)] = gate()


class OnlineNativeTests(unittest.TestCase):
    def setUp(self):
        self.library = OnlineLibrary()
        for patcher in (patch("inspect_object.ctypes.CDLL", side_effect=lambda *a, **k: self.library),
                        patch("native_loader.NativeBackend._fd_access", side_effect=lambda fd: self.library.fd_access(fd)),
                        patch("native_loader.os.close")):
            patcher.start(); self.addCleanup(patcher.stop)

    def backend(self, ready=True, lookup=True):
        backend = NativeBackend("fake", b"object", profile="online")
        if ready:
            backend.open_object(); backend.load_object(); backend.verify_loaded()
        if ready and lookup:
            backend.owned_links.update((0, 1))
            self.library.values["capture_config"][bytes(4)] = CONFIG_RAW
            self.library.values["online_bindings"][KEY] = BINDING
            self.library.values["connect_records"][struct.pack("<Q", COOKIE)] = bytes(fixture("connect_records"))
        self.library.calls.clear(); self.library.events.clear()
        return backend

    def names(self):
        return [name for name, _ in self.library.calls]

    def test_lookup_exact_keys_locked_samples_and_no_enumeration(self):
        backend = self.backend(); result = backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(result, dict(binding=BINDING, connect=bytes(fixture("connect_records")),
                                     gate=gate(), faults=bytes(8)))
        expected = [("capture_config", bytes(4)), ("capture_gate", bytes(4)), ("capture_faults", bytes(4)),
                    ("online_bindings", KEY), ("connect_records", struct.pack("<Q", COOKIE)),
                    ("capture_faults", bytes(4)), ("capture_gate", bytes(4))]
        calls = self.library.calls
        self.assertEqual(len(calls), len(expected))
        for (name, args), (map_name, key) in zip(calls, expected):
            self.assertEqual(name, "bpf_map_lookup_elem_flags" if map_name == "capture_gate" else "bpf_map_lookup_elem")
            self.assertEqual(args[0], self.library.maps[map_name])
            self.assertEqual(ctypes.string_at(args[1], len(key)), key)
            if map_name == "capture_gate": self.assertEqual(args[3], 4)
        self.assertNotIn("bpf_map_get_next_key", self.names())

    def test_lookup_returns_last_gate_sample_and_all_u64_fields_remain_raw(self):
        backend = self.backend(); final = gate(0, 1, 3, 2)
        self.library.gates = [gate(), final]
        self.assertEqual(backend.online_lookup(str(ACCEPTED), CONFIG_RAW)["gate"], final)

    def test_local_preconditions_reject_before_native_reads(self):
        for cookie in (0, True, 1, "0", "01", "-1", "1 ", str(2**64), "9" * 21, None):
            backend = self.backend()
            with self.subTest(cookie=cookie), self.assertRaises(NativeError):
                backend.online_lookup(cookie, CONFIG_RAW)
            self.assertEqual(self.library.calls, [])
        for links in (set(), {0}, {0, 1, 2}):
            backend = self.backend(); backend.owned_links = links
            with self.subTest(links=links), self.assertRaises(NativeError):
                backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
            self.assertEqual(self.library.calls, [])
        for attr in ("loaded", "verified"):
            backend = self.backend(); setattr(backend, attr, False)
            with self.subTest(attr=attr), self.assertRaises(NativeError):
                backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
            self.assertEqual(self.library.calls, [])
        backend = self.backend(); backend.profile = "namespaced"
        with self.assertRaises(NativeError): backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(self.library.calls, [])

    def test_bad_expected_config_rejects_before_read_and_mismatch_stops_at_readback(self):
        for raw in (None, bytes(31), bytes(33), bytearray(CONFIG_RAW), bytes(32)):
            backend = self.backend()
            with self.subTest(raw=raw), self.assertRaises(NativeError):
                backend.online_lookup(str(ACCEPTED), raw)
            self.assertEqual(self.library.calls, [])
        backend = self.backend(); self.library.values["capture_config"][bytes(4)] = bytes(32)
        with self.assertRaisesRegex(NativeError, "config readback"):
            backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(self.names(), ["bpf_map_lookup_elem"])

    def test_stopped_or_poisoned_initial_gate_stops_before_fault_lookup(self):
        for raw in (gate(closed=1), gate(poison=1), gate(lock=1), gate(active=1)):
            backend = self.backend(); self.library.gates = [raw]
            with self.subTest(raw=raw), self.assertRaises(NativeError):
                backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
            self.assertEqual(self.names(), ["bpf_map_lookup_elem", "bpf_map_lookup_elem_flags"])

    def test_initial_fault_stops_before_binding_and_missing_binding_has_no_fallback(self):
        backend = self.backend(); self.library.values["capture_faults"][bytes(4)] = struct.pack("<Q", 1)
        with self.assertRaisesRegex(NativeError, "faults nonzero"):
            backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(len(self.library.calls), 3)
        self.library = OnlineLibrary(); backend = self.backend()
        self.library.values["online_bindings"].clear()
        with self.assertRaises(NativeError) as caught:
            backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(caught.exception.errno, errno.ENOENT)
        self.assertEqual(len(self.library.calls), 4)

    def test_malformed_binding_stops_before_connect_and_missing_connect_stops_after_lookup(self):
        backend = self.backend(); fields = list(BINDING_FIELDS); fields[2] += 1
        self.library.values["online_bindings"][KEY] = struct.pack("<QQQQIIIIII", *fields)
        with self.assertRaisesRegex(NativeError, "cookie mismatch"):
            backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(len(self.library.calls), 4)
        self.library = OnlineLibrary(); backend = self.backend(); self.library.values["connect_records"].clear()
        with self.assertRaises(NativeError) as caught:
            backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(caught.exception.errno, errno.ENOENT)
        self.assertEqual(len(self.library.calls), 5)

    def test_second_fault_stops_before_final_gate_and_final_stop_rejects(self):
        backend = self.backend(); count = [0]
        def lookup(fd, key, out):
            name = next(n for n, f in self.library.maps.items() if f == fd)
            raw = self.library.values[name][ctypes.string_at(key, self.library.metadata[name]["key_size"])]
            if name == "capture_faults":
                count[0] += 1
                if count[0] == 2: raw = struct.pack("<Q", 1)
            ctypes.memmove(out, raw, len(raw)); return 0
        self.library.overrides["bpf_map_lookup_elem"] = lookup
        with self.assertRaisesRegex(NativeError, "faults nonzero"):
            backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(len(self.library.calls), 6)
        self.library = OnlineLibrary(); backend = self.backend(); self.library.gates = [gate(), gate(closed=1)]
        with self.assertRaisesRegex(NativeError, "gate is closed"):
            backend.online_lookup(str(ACCEPTED), CONFIG_RAW)
        self.assertEqual(len(self.library.calls), 7)

    def test_online_initialize_requires_each_hash_empty_and_zero_gate(self):
        for name, key, value in (("connect_records", struct.pack("<Q", COOKIE), bytes(112)),
                                 ("online_tuples", TUPLE, struct.pack("<Q", COOKIE)),
                                 ("online_bindings", KEY, BINDING)):
            self.library = OnlineLibrary(); backend = self.backend(lookup=False)
            self.library.values[name][key] = value
            with self.subTest(name=name), self.assertRaisesRegex(NativeError, "initially nonempty"):
                backend.initialize_config(CONFIG_RAW)
            self.assertNotIn("bpf_map_update_elem", self.names())
        self.library = OnlineLibrary(); backend = self.backend(lookup=False)
        self.library.values["capture_gate"][bytes(4)] = gate(admitted=1, completed=1)
        with self.assertRaisesRegex(NativeError, "initial gate"):
            backend.initialize_config(CONFIG_RAW)
        self.assertNotIn("bpf_map_update_elem", self.names())
        self.library = OnlineLibrary(); backend = self.backend(lookup=False)
        backend.initialize_config(CONFIG_RAW); backend.freeze_config(CONFIG_RAW)
        self.assertEqual(self.library.values["capture_config"][bytes(4)], CONFIG_RAW)

    def test_exact_online_program_map_ids_readonly_access_and_root_link_target(self):
        backend = self.backend(lookup=False)
        result = backend.verify_loaded()
        self.assertEqual(set(result["programs"]["hoopa_lifecycle"]["map_ids"]), set(result["maps"].values()))
        for name in ("hoopa_connect", "hoopa_lifecycle", "hoopa_stop"):
            self.library.map_references[name] = []
            with self.subTest(name=name), self.assertRaisesRegex(NativeError, "map IDs"):
                backend.verify_loaded()
            self.library.map_references.clear()
        self.library.access_overrides[self.library.maps["online_bindings"]] = os.O_RDWR
        with self.assertRaisesRegex(NativeError, "descriptor access"):
            backend.verify_loaded()
        self.library.access_overrides.clear()
        fd = backend.create_link("hoopa_connect", 9)
        self.assertEqual(backend.verify_link(fd, "hoopa_connect", 21)["cgroup_id"], "21")
        self.assertNotEqual(21, CONFIG.cgroup)
        with self.assertRaises(NativeError): backend.create_link("hoopa_stop", 9)

    def test_no_online_snapshot_and_stop_opts_remain_bounded(self):
        backend = self.backend()
        with self.assertRaisesRegex(NativeError, "online snapshot"):
            backend.snapshot()
        self.assertEqual(self.library.calls, [])
        result = backend.stop_writers(clock=lambda: 0, sleep=lambda seconds: None)
        self.assertEqual(result["status"], "closed_and_drained_observed")
        self.assertEqual(self.library.stop_options["sz"], ctypes.sizeof(TestRunOptions))
        self.assertEqual(self.library.stop_options["data_size_in"], 64)
        self.assertEqual(self.library.stop_options["repeat"], 1)
        for field in ("data_out", "ctx_in", "ctx_out"):
            self.assertIsNone(self.library.stop_options[field])


if __name__ == "__main__":
    unittest.main()
