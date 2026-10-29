"""Synthetic byte fixtures only: these tests do not perform a kernel capture."""

import json
import struct
import unittest
from dataclasses import FrozenInstanceError

from observations import Config, ObservationError, decode_config, decode_entry, expected_maps


MAX64 = (1 << 64) - 1
GEN = (1 << 53) + 7
COOKIE = MAX64
CONFIG = Config(GEN, 23, 42, 7890)
KEY = COOKIE.to_bytes(8, "little")


def put(raw, offset, value, width=4):
    raw[offset:offset + width] = value.to_bytes(width, "little")
    return raw


def fixture(kind):
    raw = bytearray({"connect_records": 112, "established_records": 48,
                     "close_records": 40}[kind])
    for offset, number in ((0, GEN), (8, MAX64), (16, COOKIE)):
        put(raw, offset, number, 8)
    if kind == "connect_records":
        for offset, number in ((24, 42), (32, 23), (40, GEN + 1)):
            put(raw, offset, number, 8)
        for offset, number in ((48, 1), (52, 100), (56, 101), (60, 4026531836),
                               (64, 100), (68, 1), (72, 1)):
            put(raw, offset, number)
        raw[80:96] = b"worker\0garbage!\0"
        raw[96:112] = b"leader\0" + b"\0" * 9
    elif kind == "established_records":
        put(raw, 24, 1)
        put(raw, 28, 4)
        raw[32:40] = b"\x7f\0\0\1" * 2
        put(raw, 40, 45678)
        put(raw, 44, 7890)
    else:
        put(raw, 24, 1)
        put(raw, 28, 1)
        put(raw, 32, 7)
    return raw


class ConfigTests(unittest.TestCase):
    def test_config_and_frozen_value(self):
        raw = struct.pack("<QQQII", GEN, 23, 42, 65535, 0)
        config = decode_config(raw)
        self.assertEqual(config, Config(GEN, 23, 42, 65535))
        with self.assertRaises(FrozenInstanceError):
            config.netns = 2

    def test_config_rejections(self):
        for values in ((0, 23, 42, 1, 0), (1, 0, 42, 1, 0),
                       (1, 23, 0, 1, 0), (1, 23, 42, 0, 0),
                       (1, 23, 42, 65536, 0), (1, 23, 42, 1, 1)):
            with self.subTest(values=values), self.assertRaises(ObservationError):
                decode_config(struct.pack("<QQQII", *values))
        for raw in (b"", b"\0" * 31, b"\0" * 33, bytearray(32)):
            with self.assertRaises(ObservationError):
                decode_config(raw)

    def test_expected_maps(self):
        capture = expected_maps("capture")
        self.assertEqual(set(capture), {"capture_config", "connect_records", "capture_faults"})
        self.assertEqual(capture["capture_config"], dict(type=2, key_size=4,
                         value_size=32, max_entries=1, map_flags=128))
        self.assertEqual(capture["connect_records"], dict(type=1, key_size=8,
                         value_size=112, max_entries=1024, map_flags=8))
        self.assertEqual(capture["capture_faults"], dict(type=2, key_size=4,
                         value_size=8, max_entries=1, map_flags=8))
        lifecycle = expected_maps("lifecycle")
        for name, size in (("established_records", 48), ("close_records", 40)):
            self.assertEqual(lifecycle[name], dict(type=1, key_size=8,
                             value_size=size, max_entries=2048, map_flags=8))
        lifecycle["capture_config"]["value_size"] = 9
        self.assertEqual(expected_maps("capture")["capture_config"]["value_size"], 32)
        with self.assertRaises(ObservationError):
            expected_maps("other")


class EntryTests(unittest.TestCase):
    def decode(self, kind, raw=None, key=KEY, config=CONFIG):
        return decode_entry(kind, key, bytes(fixture(kind) if raw is None else raw), config)

    def rejects(self, kind, offset, number, width=4):
        with self.subTest(kind=kind, offset=offset, number=number):
            with self.assertRaises(ObservationError):
                self.decode(kind, put(fixture(kind), offset, number, width))

    def test_connect_precision_and_name_boundaries(self):
        record = self.decode("connect_records")
        self.assertEqual(record["generation"], str(GEN))
        self.assertEqual(record["client_cookie"], str(MAX64))
        self.assertEqual(record["observed_boot_ns"], str(MAX64))
        self.assertEqual(record["leader_start_boottime_ns"], str(GEN + 1))
        self.assertEqual(record["caller_tid_initial"], 101)
        self.assertEqual(record["leader_pid_initial"], 100)
        self.assertEqual(record["caller_comm"]["text"], "worker")
        self.assertEqual(len(record["caller_comm"]["raw_hex"]), 32)
        raw = fixture("connect_records")
        put(raw, 68, 0)
        raw[80:96] = b"partial\0" + b"\xff" * 7 + b"\0"
        self.assertEqual(self.decode("connect_records", raw)["caller_comm"],
                         dict(status="unavailable", text="partial", raw_hex=raw[80:96].hex()))
        raw[80] = 255
        self.assertIsNone(self.decode("connect_records", raw)["caller_comm"]["text"])
        self.assertEqual(json.loads(json.dumps(record))["client_cookie"], str(MAX64))

    def test_connect_invalid_fields(self):
        for offset, number, width in ((0, GEN + 1, 8), (16, 0, 8), (24, 43, 8),
                                     (32, 24, 8), (40, 0, 8), (48, 0, 4),
                                     (48, 2, 4), (52, 0, 4), (56, 0, 4),
                                     (60, 0, 4), (64, 101, 4), (68, 2, 4),
                                     (72, 2, 4), (76, 1, 4), (95, 1, 1), (111, 1, 1)):
            self.rejects("connect_records", offset, number, width)

    def test_established_mixed_endian_and_passive(self):
        record = self.decode("established_records")
        self.assertEqual((record["local_ip4"], record["remote_ip4"]),
                         ("127.0.0.1", "127.0.0.1"))
        self.assertEqual((record["local_port"], record["remote_port"]), (45678, 7890))
        self.assertEqual(record["socket_netns_cookie"], "unknown")
        raw = fixture("established_records")
        put(raw, 28, 5)
        put(raw, 40, 7890)
        put(raw, 44, 65535)
        self.assertEqual(self.decode("established_records", raw)["remote_port"], 65535)
        put(raw, 44, 1)
        self.assertEqual(self.decode("established_records", raw)["remote_port"], 1)

    def test_established_invalid_fields(self):
        for offset, number in ((24, 2), (28, 3), (28, 6), (32, 0x7f000001),
                               (36, 0), (40, 0), (40, 65536), (44, 0),
                               (44, 65536), (44, 45678)):
            self.rejects("established_records", offset, number)
        raw = fixture("established_records")
        put(raw, 28, 5)
        with self.assertRaises(ObservationError):
            self.decode("established_records", raw)

    def test_close_state_boundaries(self):
        for old in (1, 7, 12):
            raw = put(fixture("close_records"), 28, old)
            record = self.decode("close_records", raw)
            self.assertEqual(record["old_state"], old)
            self.assertEqual(record["association"], "unverified")
            self.assertNotIn("process_exit", record)
            self.assertNotIn("cleanup_allowed", record)
        for offset, number in ((24, 2), (28, 0), (28, 13), (32, 1), (36, 1)):
            self.rejects("close_records", offset, number)

    def test_shared_key_length_generation_and_status(self):
        for kind in ("connect_records", "established_records", "close_records"):
            record = self.decode(kind)
            self.assertEqual(record["evidence_status"], "unverified")
            for raw in (fixture(kind)[:-1], fixture(kind) + b"\0"):
                with self.assertRaises(ObservationError):
                    self.decode(kind, raw)
            for key in (b"", KEY[:-1], KEY + b"\0", b"\0" * 8, (1).to_bytes(8, "little")):
                with self.assertRaises(ObservationError):
                    self.decode(kind, key=key)
            with self.assertRaises(ObservationError):
                self.decode(kind, config=Config(GEN + 1, 23, 42, 7890))
        for name in ("capture_config", "unknown"):
            with self.assertRaises(ObservationError):
                decode_entry(name, KEY, b"", CONFIG)
        with self.assertRaises(ObservationError):
            self.decode("close_records", config=Config(0, 23, 42, 7890))

    def test_fault_count_precision_and_zero_never_passed(self):
        for count in (0, (1 << 53) + 1, MAX64):
            result = decode_entry("capture_faults", b"\0" * 4,
                                  count.to_bytes(8, "little"), CONFIG)
            self.assertEqual(result, dict(fault_count=str(count), evidence_status="unverified"))
        for key, value in ((b"\1\0\0\0", b"\0" * 8), (b"\0" * 3, b"\0" * 8),
                           (b"\0" * 4, b"\0" * 7), (b"\0" * 4, b"\0" * 9)):
            with self.assertRaises(ObservationError):
                decode_entry("capture_faults", key, value, CONFIG)


if __name__ == "__main__":
    unittest.main()
