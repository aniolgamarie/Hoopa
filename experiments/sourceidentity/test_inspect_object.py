"""Offline inspector tests with callable CDLL symbols, never a real library."""

import ctypes
import hashlib
from pathlib import Path
import struct
import tempfile
import unittest
from unittest import mock

import inspect_object as inspector
from observations import expected_maps


class Function:
    """A ctypes-like symbol that records calls and accepts ABI declarations."""

    def __init__(self, implementation):
        self.implementation = implementation
        self.calls = []

    def __call__(self, *args):
        self.calls.append(args)
        return self.implementation(*args)


class FakeLibrary:
    OBJECT = 100

    def __init__(self, profile="capture", legacy=False):
        self.symbols = {}
        self.lookups = []
        self.programs = {1000 + i: dict(name=name.encode(), section=section.encode(),
                                     observed_type=0, observed_expected_attach_type=0)
                         for i, (name, section) in enumerate(inspector.PROGRAMS[profile].items())}
        self.maps = {2000 + i: dict(name=name.encode(), **metadata)
                     for i, (name, metadata) in enumerate(expected_maps(profile).items())}
        self.symbol("bpf_object__open_mem", lambda *args: self.OBJECT)
        self.symbol("libbpf_get_error", lambda obj: 0)
        self.symbol("bpf_object__close", lambda obj: None)
        self.symbol("bpf_program__next" if legacy else "bpf_object__next_program",
                    self.iterator(self.programs, legacy))
        self.symbol("bpf_map__next" if legacy else "bpf_object__next_map",
                    self.iterator(self.maps, legacy))
        for field, symbol in (("name", "bpf_program__name"),
                              ("section", "bpf_program__section_name"),
                              ("observed_type", "bpf_program__get_type" if legacy else "bpf_program__type"),
                              ("observed_expected_attach_type", "bpf_program__get_expected_attach_type"
                               if legacy else "bpf_program__expected_attach_type")):
            self.symbol(symbol, lambda ptr, field=field: self.programs[ptr][field])
        for field in ("name", "type", "key_size", "value_size", "max_entries", "map_flags"):
            self.symbol("bpf_map__" + field, lambda ptr, field=field: self.maps[ptr][field])

    def symbol(self, name, implementation):
        self.symbols[name] = Function(implementation)

    def iterator(self, items, legacy):
        def next_item(first, second):
            previous, obj = (first, second) if legacy else (second, first)
            if obj != self.OBJECT:
                raise AssertionError("iterator object/previous argument order is wrong")
            keys = list(items)
            index = 0 if previous is None else keys.index(previous) + 1
            return keys[index] if index < len(keys) else None
        return next_item

    def __getattr__(self, name):
        self.lookups.append(name)
        if name not in self.symbols:
            raise AttributeError(name)
        return self.symbols[name]


class InspectorTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        root = Path(self.directory.name)
        self.object = root / "observation.o"
        elf = bytearray(64)
        elf[:7] = b"\x7fELF\x02\x01\x01"
        struct.pack_into("<HHI", elf, 16, 1, 247, 1)
        self.object.write_bytes(elf)
        self.library = root / "libbpf.so"
        self.library.write_bytes(b"synthetic library bytes: never loaded")
        self.system = mock.patch.object(inspector.platform, "system", return_value="Linux")
        self.machine = mock.patch.object(inspector.platform, "machine", return_value="x86_64")
        self.system.start()
        self.machine.start()
        self.addCleanup(self.system.stop)
        self.addCleanup(self.machine.stop)

    @staticmethod
    def digest(path):
        return hashlib.sha256(path.read_bytes()).hexdigest()

    def inspect(self, library, profile="capture", **overrides):
        arguments = dict(profile=profile, object_path=str(self.object),
                         object_sha256=self.digest(self.object), libbpf_path=str(self.library),
                         libbpf_sha256=self.digest(self.library))
        arguments.update(overrides)
        with mock.patch.object(inspector.ctypes, "CDLL", return_value=library) as cdll:
            result = inspector.inspect_object(**arguments)
        cdll.assert_called_once_with(str(self.library), use_errno=True)
        return result

    def wrapper(self, library):
        with mock.patch.object(inspector.ctypes, "CDLL", return_value=library):
            return inspector.Libbpf(self.library)

    def assert_closed_once(self, library):
        self.assertEqual(library.symbols["bpf_object__close"].calls, [(library.OBJECT,)])

    def test_profiles_and_modern_abi_use_offline_symbols_only(self):
        for profile in ("capture", "lifecycle", "gated", "namespaced"):
            with self.subTest(profile=profile):
                fake = FakeLibrary(profile)
                with mock.patch.object(inspector.os, "open", wraps=inspector.os.open) as opened:
                    result = self.inspect(fake, profile)
                self.assertTrue(opened.called)
                required_flags = inspector.os.O_NOFOLLOW | inspector.os.O_NONBLOCK
                for call in opened.call_args_list:
                    self.assertEqual(call.args[1] & required_flags, required_flags)
                self.assertEqual(result["status"], "inspected")
                self.assertEqual(result["profile"], profile)
                self.assertEqual(result["maps"], expected_maps(profile))
                for field in ("runtime", "verifier", "association", "program_type_verification"):
                    self.assertEqual(result[field], "unverified")
                self.assert_closed_once(fake)
                self.assertEqual(fake.symbols["bpf_object__next_program"].calls[0], (100, None))
                self.assertEqual(fake.symbols["bpf_object__next_map"].calls[0], (100, None))
                for function in fake.symbols.values():
                    self.assertTrue(hasattr(function, "argtypes"))
                    self.assertTrue(hasattr(function, "restype"))
                self.assertEqual(fake.symbols["bpf_object__open_mem"].argtypes,
                                 [ctypes.c_void_p, ctypes.c_size_t, ctypes.c_void_p])
                self.assertIs(fake.symbols["bpf_map__type"].restype, ctypes.c_int)
                self.assertIs(fake.symbols["libbpf_get_error"].restype, ctypes.c_long)
                for field in ("key_size", "value_size", "max_entries", "map_flags"):
                    self.assertIs(fake.symbols["bpf_map__" + field].restype, ctypes.c_uint32)
                self.assertEqual(set(fake.lookups), set(fake.symbols))
                self.assertFalse(any(word in name for name in fake.lookups
                                     for word in ("__load", "__attach", "__pin", "__unpin")))

    def test_namespaced_rejects_old_map_declaration(self):
        fake = FakeLibrary("namespaced")
        for metadata in fake.maps.values():
            if metadata["name"] == b"established_records":
                metadata["value_size"] = 48
        with self.assertRaisesRegex(inspector.InspectionError, "map ABI"):
            self.inspect(fake, "namespaced")
        self.assert_closed_once(fake)

    def test_legacy_iterator_and_getter_fallback(self):
        fake = FakeLibrary("lifecycle", legacy=True)
        result = self.inspect(fake, "lifecycle")
        self.assertEqual(result["iterator_api"], {"program": "bpf_program__next", "map": "bpf_map__next"})
        for symbol in ("bpf_program__next", "bpf_map__next"):
            calls = fake.symbols[symbol].calls
            self.assertEqual(calls[0], (None, 100))
            self.assertTrue(all(obj == 100 for previous, obj in calls))
        self.assert_closed_once(fake)

    def test_null_and_error_pointer_do_not_close_invalid_object(self):
        for pointer, error in ((None, 0), ((1 << 64) - 22, -22)):
            with self.subTest(pointer=pointer):
                fake = FakeLibrary()
                fake.symbol("bpf_object__open_mem", lambda *args: pointer)
                fake.symbol("libbpf_get_error", lambda obj: error)
                with mock.patch.object(inspector.ctypes, "get_errno", return_value=13):
                    with self.assertRaisesRegex(inspector.InspectionError,
                                                "open failed: {}".format(error or 13)):
                        self.wrapper(fake).inspect(b"synthetic ELF")
                self.assertEqual(fake.symbols["bpf_object__close"].calls, [])
                self.assertEqual(fake.symbols["libbpf_get_error"].calls, [(pointer,)])

    def test_metadata_exception_closes_once(self):
        fake = FakeLibrary()
        def fail(ptr):
            raise RuntimeError("synthetic getter failure")
        fake.symbol("bpf_map__value_size", fail)
        with self.assertRaisesRegex(RuntimeError, "synthetic getter failure"):
            self.wrapper(fake).inspect(b"bytes")
        self.assert_closed_once(fake)

    def test_repeated_and_unbounded_iteration_rejected(self):
        for mode in ("repeat", "excess"):
            with self.subTest(mode=mode):
                fake = FakeLibrary()
                if mode == "repeat":
                    fake.symbol("bpf_object__next_program", lambda obj, previous: 1000)
                else:
                    fake.programs.clear()
                    for i in range(inspector.MAX_ITEMS + 1):
                        fake.programs[1000 + i] = dict(name=b"p", section=b"s", observed_type=0,
                                                      observed_expected_attach_type=0)
                with self.assertRaisesRegex(inspector.InspectionError, "repeated or exceeded bound"):
                    self.wrapper(fake).inspect(b"bytes")
                self.assert_closed_once(fake)

    def test_missing_symbol_is_diagnostic_before_open(self):
        fake = FakeLibrary()
        del fake.symbols["bpf_map__map_flags"]
        with self.assertRaisesRegex(inspector.InspectionError, "missing symbol: bpf_map__map_flags"):
            self.wrapper(fake)
        self.assertEqual(fake.symbols["bpf_object__open_mem"].calls, [])
        self.assertEqual(fake.symbols["bpf_object__close"].calls, [])

    def test_map_and_program_declaration_mismatches_close_once(self):
        for mode in ("map", "program", "duplicate_program", "duplicate_map"):
            with self.subTest(mode=mode):
                fake = FakeLibrary()
                if mode == "map":
                    fake.maps[2000]["value_size"] += 1
                elif mode == "program":
                    fake.programs[1000]["section"] = b"wrong/section"
                elif mode == "duplicate_program":
                    fake.programs[1001] = dict(fake.programs[1000])
                else:
                    fake.maps[2001]["name"] = fake.maps[2000]["name"]
                with self.assertRaisesRegex(inspector.InspectionError,
                                            "map ABI|program names/sections|duplicate map"):
                    self.inspect(fake)
                self.assert_closed_once(fake)

    def test_bad_input_rejected_before_cdll(self):
        valid = dict(profile="capture", object_path=str(self.object),
                     object_sha256=self.digest(self.object), libbpf_path=str(self.library),
                     libbpf_sha256=self.digest(self.library))
        for change, message in (({"profile": "bad"}, "unsupported profile"),
                                ({"object_sha256": "0" * 64}, "SHA256 mismatch"),
                                ({"libbpf_sha256": "0" * 64}, "SHA256 mismatch"),
                                ({"object_sha256": "A" * 64}, "lowercase hex")):
            with self.subTest(change=change), mock.patch.object(inspector.ctypes, "CDLL") as cdll:
                arguments = dict(valid, **change)
                with self.assertRaisesRegex(inspector.InspectionError, message):
                    inspector.inspect_object(**arguments)
                cdll.assert_not_called()
        with mock.patch.object(inspector.os, "open") as opened, \
                mock.patch.object(inspector.ctypes, "CDLL") as cdll:
            arguments = dict(valid, object_path=self.directory.name)
            with self.assertRaisesRegex(inspector.InspectionError, "regular input file required"):
                inspector.inspect_object(**arguments)
            opened.assert_not_called()
            cdll.assert_not_called()
        for offset, replacement, message in ((0, b"BAD!", "ELF64"),
                                              (5, b"\x02", "ELF64"),
                                              (16, b"\x02\0", "BPF relocatable"),
                                              (18, b"\x3e\0", "BPF relocatable")):
            with self.subTest(offset=offset), mock.patch.object(inspector.ctypes, "CDLL") as cdll:
                raw = bytearray(64)
                raw[:7] = b"\x7fELF\x02\x01\x01"
                struct.pack_into("<HHI", raw, 16, 1, 247, 1)
                raw[offset:offset + len(replacement)] = replacement
                self.object.write_bytes(raw)
                arguments = dict(valid, object_sha256=self.digest(self.object))
                with self.assertRaisesRegex(inspector.InspectionError, message):
                    inspector.inspect_object(**arguments)
                cdll.assert_not_called()


if __name__ == "__main__":
    unittest.main()
