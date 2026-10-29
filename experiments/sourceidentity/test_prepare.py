"""Offline fixtures verify preparation only, never kernel acceptance."""

import contextlib
import io
import json
import os
from pathlib import Path
import stat
import struct
import sys
import tempfile
import unittest
from unittest import mock

import prepare


def elf_fixture(section_names=None):
    names = sorted(prepare.REQUIRED_SECTIONS if section_names is None else section_names)
    strings = b"\0.shstrtab\0" + b"".join(name.encode() + b"\0" for name in names)
    count = len(names) + 2
    data_offset = 64 + 64 * count
    header = struct.pack("<16sHHIQQQIHHHHHH", b"\x7fELF\x02\x01\x01" + b"\0" * 9,
                         1, 247, 1, 0, 0, 64, 0, 64, 0, 0, 64, count, 1)
    sections = [bytes(64), struct.pack("<IIQQQQIIQQ", 1, 3, 0, 0, data_offset, len(strings), 0, 0, 1, 0)]
    for name in names:
        sections.append(struct.pack("<IIQQQQIIQQ", strings.index(name.encode() + b"\0"),
                                    1, 0, 0, data_offset + len(strings),
                                    8 if name in ("cgroup/connect4", "sockops", "tc") else 1, 0, 0, 1, 0))
    return header + b"".join(sections) + strings + b"\0" * 8


class PrepareTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="hoopa-prepare-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "connect_snapshot.bpf.c"
        self.source.write_text("/* fixture only */\n")
        self.lifecycle_source = self.root / "lifecycle_snapshot.bpf.c"
        self.lifecycle_source.write_text('#include "connect_snapshot.bpf.c"\n')
        self.gated_source = self.root / "gated_snapshot.bpf.c"
        self.gated_source.write_text('#include "lifecycle_snapshot.bpf.c"\n#include "connect_snapshot.bpf.c"\n')
        self.namespaced_source = self.root / "namespaced_snapshot.bpf.c"
        self.namespaced_source.write_text('#define HOOPA_NAMESPACED\n#include "gated_snapshot.bpf.c"\n')
        self.btf = self.root / "kernel.btf"
        self.btf.write_bytes(b"fixture BTF")
        self.fixture = self.root / "fixture.o"
        self.fixture.write_bytes(elf_fixture())
        self.calls = self.root / "calls.jsonl"
        self.failure = self.root / "failure"
        self.mutation = self.root / "mutation.json"
        self.observed = self.root / "observed.jsonl"
        self.tool_mutation = self.root / "tool-mutation"
        self.tool = self.root / "fake-tool"
        self.tool.write_text("#!" + sys.executable + "\n" + '''import json, pathlib, sys
args = sys.argv[1:]
with open(CALLS, "a") as stream:
    stream.write(json.dumps(args) + "\\n")
if pathlib.Path(FAILURE).exists():
    print("fixture failure", file=sys.stderr)
    sys.exit(7)
if args in (["--version"], ["version"]):
    if pathlib.Path(MUTATION).exists():
        for path, content in json.loads(pathlib.Path(MUTATION).read_text()).items():
            pathlib.Path(path).write_text(content)
        pathlib.Path(MUTATION).unlink()
    if pathlib.Path(TOOL_MUTATION).exists():
        with open(__file__, "a") as stream:
            stream.write("\\n# mutated tool\\n")
        pathlib.Path(TOOL_MUTATION).unlink()
    print("fixture tool version 1")
elif args[:3] == ["btf", "dump", "file"] and args[-2:] == ["format", "c"]:
    with open(OBSERVED, "a") as stream:
        stream.write(json.dumps(["btf", pathlib.Path(args[3]).read_text()]) + "\\n")
    print("/* fixture vmlinux header */")
elif args[:4] == ["-g", "-O2", "-target", "bpfel"]:
    with open(OBSERVED, "a") as stream:
        stream.write(json.dumps(["source", pathlib.Path(args[-3]).read_text()]) + "\\n")
        if pathlib.Path(args[-3]).name == "gated_snapshot.bpf.c":
            included = pathlib.Path(args[-3]).parent / "lifecycle_snapshot.bpf.c"
            stream.write(json.dumps(["included_lifecycle", included.read_text()]) + "\\n")
        if pathlib.Path(args[-3]).name in ("lifecycle_snapshot.bpf.c", "gated_snapshot.bpf.c"):
            included = pathlib.Path(args[-3]).parent / "connect_snapshot.bpf.c"
            stream.write(json.dumps(["included_source", included.read_text()]) + "\\n")
    pathlib.Path(args[-1]).write_bytes(pathlib.Path(FIXTURE).read_bytes())
else:
    sys.exit(99)
'''.replace("CALLS", repr(str(self.calls))).replace("FAILURE", repr(str(self.failure)))
                             .replace("TOOL_MUTATION", repr(str(self.tool_mutation)))
                             .replace("MUTATION", repr(str(self.mutation)))
                             .replace("OBSERVED", repr(str(self.observed)))
                             .replace("FIXTURE", repr(str(self.fixture))))
        self.tool.chmod(0o700)
        self.args = type("Args", (), {"clang": str(self.tool), "bpftool": str(self.tool),
                                    "btf": str(self.btf), "output_parent": str(self.root)})()
        for patcher in (mock.patch.object(prepare, "SOURCE", self.source),
                        mock.patch.object(prepare, "LIFECYCLE_SOURCE", self.lifecycle_source),
                        mock.patch.object(prepare, "GATED_SOURCE", self.gated_source),
                        mock.patch.object(prepare, "NAMESPACED_SOURCE", self.namespaced_source),
                        mock.patch.object(prepare.platform, "system", return_value="Linux"),
                        mock.patch.object(prepare.platform, "machine", return_value="x86_64")):
            patcher.start()
            self.addCleanup(patcher.stop)

    def run_prepare(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            result = prepare.prepare(self.args)
        summary = json.loads(out.getvalue())
        manifest = json.loads(Path(summary["manifest"]).read_text())
        return result, manifest, err.getvalue()

    def test_namespaced_snapshots_all_sources_before_commands(self):
        self.args.profile = "namespaced"
        self.fixture.write_bytes(elf_fixture(prepare.REQUIRED_SECTIONS | {"sockops", "tc"}))
        paths = (self.namespaced_source, self.gated_source, self.lifecycle_source, self.source)
        originals = {str(path): path.read_text() for path in paths}
        self.mutation.write_text(json.dumps({str(path): "changed" for path in paths}))
        code, manifest, error = self.run_prepare()
        self.assertEqual((code, error), (0, ""))
        self.assertEqual(manifest["profile"], "namespaced")
        self.assertEqual([Path(item["original_path"]) for item in manifest["sources"]], list(paths))
        for item in manifest["sources"]:
            snapshot = Path(item["path"])
            self.assertEqual(snapshot.read_text(), originals[item["original_path"]])
            self.assertEqual(item["sha256"], prepare.digest(snapshot))
        compile_step = manifest["steps"][-1]
        self.assertEqual(Path(compile_step["argv"][-3]).name, "namespaced_snapshot.bpf.c")
        self.assertEqual(len(manifest["sources"]), 4)

    def test_namespaced_requires_all_three_valid_program_sections(self):
        names = prepare.REQUIRED_SECTIONS | {"sockops", "tc"}
        self.fixture.write_bytes(elf_fixture(names))
        prepare.check_elf(self.fixture, "namespaced")
        for section in ("cgroup/connect4", "sockops", "tc"):
            with self.subTest(section=section):
                self.fixture.write_bytes(elf_fixture(names - {section}))
                with self.assertRaises(prepare.PreparationError):
                    prepare.check_elf(self.fixture, "namespaced")
                offset = 64 + (sorted(names).index(section) + 2) * 64
                for field, fmt, value in ((4, "I", 8), (32, "Q", 7), (32, "Q", 0)):
                    raw = bytearray(elf_fixture(names))
                    struct.pack_into("<" + fmt, raw, offset + field, value)
                    self.fixture.write_bytes(raw)
                    with self.assertRaises(prepare.PreparationError):
                        prepare.check_elf(self.fixture, "namespaced")

    def test_success_has_exact_whitelist_hashes_and_private_permissions(self):
        result, manifest, error = self.run_prepare()
        self.assertEqual((result, error), (0, ""))
        self.assertEqual(manifest["status"], "prepared")
        self.assertEqual(manifest["profile"], "capture")
        self.assertEqual(manifest["sources"], [manifest["inputs"]["source"]])
        directory = Path(manifest["output_directory"])
        calls = [json.loads(line) for line in self.calls.read_text().splitlines()]
        self.assertEqual(calls, [["--version"], ["version"],
            ["btf", "dump", "file", str(directory / "kernel.btf"), "format", "c"],
            ["-g", "-O2", "-target", "bpfel", "-D__TARGET_ARCH_x86", "-I" + str(directory),
             "-c", str(directory / "connect_snapshot.bpf.c"), "-o", str(directory / "connect_snapshot.bpf.o")]])
        for forbidden in ("load", "attach", "sudo", "curl", "wget", "mount"):
            self.assertNotIn(forbidden, [arg for call in calls for arg in call])
        for field in ("runtime", "verifier", "association"):
            self.assertEqual(manifest[field], "unverified")
        self.assertEqual(stat.S_IMODE(directory.stat().st_mode), 0o700)
        for path in directory.iterdir():
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            if path.name != "manifest.json":
                self.assertEqual(manifest["artifacts"][path.name]["sha256"], prepare.digest(path))
        self.assertEqual(manifest["inputs"]["btf"]["sha256"], prepare.digest(self.btf))
        self.assertEqual(manifest["inputs"]["btf"]["original_path"], str(self.btf))
        for name in ("clang", "bpftool"):
            self.assertTrue(manifest["inputs"][name]["unchanged"])
            self.assertEqual(manifest["inputs"][name]["sha256"], manifest["inputs"][name]["final_sha256"])
        self.assertIn("fixture tool version", manifest["inputs"]["clang"]["version"])
        again, second, _ = self.run_prepare()
        self.assertEqual(again, 0)
        self.assertNotEqual(second["output_directory"], str(directory))

    def test_cli_defaults_and_rejects_unlisted_profile_or_source(self):
        required = ["prepare.py", "--clang", str(self.tool), "--bpftool", str(self.tool),
                    "--btf", str(self.btf)]
        with mock.patch.object(sys, "argv", required), mock.patch.object(prepare, "prepare", return_value=0) as call:
            self.assertEqual(prepare.main(), 0)
            self.assertEqual(call.call_args[0][0].profile, "capture")
        for extra in (["--profile", "arbitrary"], ["--source", str(self.source)]):
            with self.subTest(extra=extra), mock.patch.object(sys, "argv", required + extra), \
                    contextlib.redirect_stderr(io.StringIO()), mock.patch.object(prepare, "prepare") as call:
                with self.assertRaises(SystemExit) as raised:
                    prepare.main()
                self.assertEqual(raised.exception.code, 2)
                call.assert_not_called()

    def test_lifecycle_snapshots_both_sources_before_tools_and_keeps_capture_output(self):
        result, capture, _ = self.run_prepare()
        self.assertEqual(result, 0)
        capture_object = Path(capture["output_directory"]) / "connect_snapshot.bpf.o"
        capture_bytes = capture_object.read_bytes()
        self.calls.unlink()
        self.observed.unlink()
        self.args.profile = "lifecycle"
        self.fixture.write_bytes(elf_fixture(prepare.REQUIRED_SECTIONS | {"sockops"}))
        originals = {"source": self.lifecycle_source.read_text(), "included_source": self.source.read_text(),
                     "btf": self.btf.read_text()}
        self.mutation.write_text(json.dumps({str(self.lifecycle_source): "changed lifecycle",
                                            str(self.source): "changed include", str(self.btf): "changed BTF"}))
        result, manifest, error = self.run_prepare()
        self.assertEqual((result, error), (0, ""))
        self.assertEqual(manifest["profile"], "lifecycle")
        self.assertIn("sockops", manifest["sections"])
        self.assertEqual(dict(json.loads(line) for line in self.observed.read_text().splitlines()), originals)
        directory = Path(manifest["output_directory"])
        self.assertNotEqual(str(directory), capture["output_directory"])
        self.assertEqual(capture_object.read_bytes(), capture_bytes)
        self.assertEqual(manifest["inputs"]["source"], manifest["sources"][0])
        self.assertEqual(len(manifest["sources"]), 2)
        for binding, original, content in zip(manifest["sources"], (self.lifecycle_source, self.source),
                                               (originals["source"], originals["included_source"])):
            snapshot = Path(binding["path"])
            self.assertEqual(binding["original_path"], str(original))
            self.assertEqual(snapshot.parent, directory)
            self.assertEqual(snapshot.read_text(), content)
            self.assertEqual(binding["sha256"], prepare.digest(snapshot))
        calls = [json.loads(line) for line in self.calls.read_text().splitlines()]
        self.assertEqual(calls, [["--version"], ["version"],
            ["btf", "dump", "file", str(directory / "kernel.btf"), "format", "c"],
            ["-g", "-O2", "-target", "bpfel", "-D__TARGET_ARCH_x86", "-I" + str(directory),
             "-c", str(directory / "lifecycle_snapshot.bpf.c"), "-o", str(directory / "lifecycle_snapshot.bpf.o")]])
        for field in ("runtime", "verifier", "association"):
            self.assertEqual(manifest[field], "unverified")

    def test_gated_snapshots_three_sources_before_tools(self):
        self.args.profile = "gated"
        self.fixture.write_bytes(elf_fixture(prepare.REQUIRED_SECTIONS | {"sockops", "tc"}))
        source_paths = (self.gated_source, self.lifecycle_source, self.source)
        contents = [path.read_text() for path in source_paths]
        originals = dict(zip(("source", "included_lifecycle", "included_source"), contents))
        originals["btf"] = self.btf.read_text()
        changes = {str(path): "changed " + path.name for path in source_paths}
        changes[str(self.btf)] = "changed BTF"
        self.mutation.write_text(json.dumps(changes))
        result, manifest, error = self.run_prepare()
        self.assertEqual((result, error), (0, ""))
        self.assertEqual(manifest["profile"], "gated")
        self.assertTrue({"cgroup/connect4", "sockops", "tc"} <= set(manifest["sections"]))
        directory = Path(manifest["output_directory"])
        self.assertTrue(directory.name.startswith("hoopa-sourceidentity-gated-"))
        self.assertEqual(dict(json.loads(line) for line in self.observed.read_text().splitlines()), originals)
        self.assertEqual(len(manifest["sources"]), 3)
        self.assertEqual(manifest["inputs"]["source"], manifest["sources"][0])
        for binding, original, content in zip(manifest["sources"], source_paths, contents):
            snapshot = Path(binding["path"])
            self.assertEqual(binding["original_path"], str(original))
            self.assertEqual(snapshot.parent, directory)
            self.assertEqual(snapshot.read_text(), content)
            self.assertEqual(original.read_text(), changes[str(original)])
            self.assertEqual(binding["sha256"], prepare.digest(snapshot))
        calls = [json.loads(line) for line in self.calls.read_text().splitlines()]
        self.assertEqual(calls, [["--version"], ["version"],
            ["btf", "dump", "file", str(directory / "kernel.btf"), "format", "c"],
            ["-g", "-O2", "-target", "bpfel", "-D__TARGET_ARCH_x86", "-I" + str(directory),
             "-c", str(directory / "gated_snapshot.bpf.c"), "-o", str(directory / "gated_snapshot.bpf.o")]])
        self.assertEqual(stat.S_IMODE(directory.stat().st_mode), 0o700)
        for path in directory.iterdir():
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        for field in ("runtime", "verifier", "association"):
            self.assertEqual(manifest[field], "unverified")

    def test_gated_requires_valid_tc_instruction_section(self):
        self.args.profile = "gated"
        self.fixture.write_bytes(elf_fixture(prepare.REQUIRED_SECTIONS | {"sockops"}))
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertIn("missing sections: tc", manifest["error"])
        self.assertEqual(len(manifest["steps"]), 4)
        names = sorted(prepare.REQUIRED_SECTIONS | {"sockops", "tc"})
        program = 64 + (names.index("tc") + 2) * 64
        for offset, fmt, value in ((program + 32, "Q", 0), (program + 32, "Q", 7),
                                   (program + 4, "I", 8)):
            with self.subTest(offset=offset, value=value):
                data = bytearray(elf_fixture(names))
                struct.pack_into("<" + fmt, data, offset, value)
                self.fixture.write_bytes(data)
                with self.assertRaises(prepare.PreparationError):
                    prepare.check_elf(self.fixture, "gated")

    def test_lifecycle_missing_or_oversized_included_source_stops_before_tools(self):
        self.args.profile = "lifecycle"
        self.source.unlink()
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertIn("connect_snapshot.bpf.c", manifest["error"])
        self.assertEqual(manifest["steps"], [])
        self.assertFalse(self.calls.exists())
        self.source.write_bytes(b"x" * (prepare.MAX_SOURCE + 1))
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertIn("source: size limit exceeded", manifest["error"])
        self.assertEqual(manifest["steps"], [])
        self.assertFalse(self.calls.exists())

    def test_lifecycle_requires_valid_sockops_instruction_section(self):
        self.args.profile = "lifecycle"
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertIn("missing sections: sockops", manifest["error"])
        self.assertEqual(len(manifest["steps"]), 4)
        names = sorted(prepare.REQUIRED_SECTIONS | {"sockops"})
        program = 64 + (names.index("sockops") + 2) * 64
        for offset, fmt, value in ((program + 32, "Q", 0), (program + 32, "Q", 7),
                                   (program + 4, "I", 8)):
            with self.subTest(offset=offset, value=value):
                data = bytearray(elf_fixture(names))
                struct.pack_into("<" + fmt, data, offset, value)
                self.fixture.write_bytes(data)
                with self.assertRaises(prepare.PreparationError):
                    prepare.check_elf(self.fixture, "lifecycle")

    def test_missing_relative_nonexecutable_inputs_fail_without_commands(self):
        for field, value in (("btf", str(self.root / "missing")), ("clang", "relative"),
                             ("bpftool", str(self.btf))):
            original = getattr(self.args, field)
            with self.subTest(field=field):
                setattr(self.args, field, value)
                result, manifest, error = self.run_prepare()
                self.assertEqual(result, 1)
                self.assertEqual(manifest["steps"], [])
                self.assertIn(field + ":", error)
                self.assertFalse(self.calls.exists())
            setattr(self.args, field, original)

    def test_missing_source_and_unsupported_platform(self):
        self.source.unlink()
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertIn("source:", manifest["error"])
        with mock.patch.object(prepare.platform, "machine", return_value="aarch64"):
            result, manifest, _ = self.run_prepare()
            self.assertEqual(result, 1)
            self.assertIn("platform:", manifest["error"])
        self.assertFalse(self.calls.exists())

    def test_failed_tool_preserves_manifest_exit_and_logs(self):
        self.failure.touch()
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertEqual(manifest["status"], "failed")
        self.assertEqual(len(manifest["steps"]), 1)
        step = manifest["steps"][0]
        self.assertEqual(step["exit_code"], 7)
        self.assertEqual(step["status"], "failed")
        self.assertEqual(Path(step["stderr"]).read_text(), "fixture failure\n")

    def test_invalid_and_missing_sections_fail_after_compile(self):
        for data in (b"not ELF", elf_fixture({"cgroup/connect4"}),
                     elf_fixture()[:100], b"\x7fELF\x02\x02\x01" + elf_fixture()[7:]):
            with self.subTest(data=data[:20]):
                self.fixture.write_bytes(data)
                result, manifest, _ = self.run_prepare()
                self.assertEqual(result, 1)
                self.assertIn("object:", manifest["error"])
                self.assertEqual(len(manifest["steps"]), 4)
                self.assertEqual(manifest["verifier"], "unverified")

    def test_symlink_tools_resolve_to_fixed_real_path(self):
        link = self.root / "clang-link"
        link.symlink_to(self.tool)
        self.args.clang = str(link)
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 0)
        self.assertEqual(manifest["steps"][0]["argv"][0], str(self.tool))

    def test_output_limit_and_timeout_fail_with_logs(self):
        self.tool.write_text("#!" + sys.executable + "\nimport sys\nsys.stdout.write('x' * 10000)\n")
        with mock.patch.object(prepare, "MAX_LOG", 100):
            # Version stdout defaults to MAX_LOG at definition; stderr cap is dynamic.
            self.tool.write_text("#!" + sys.executable + "\nimport sys\nsys.stderr.write('x' * 10000)\n")
            result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertIn("output limit", manifest["error"])
        self.assertEqual(Path(manifest["steps"][0]["stderr"]).stat().st_size, 100)
        self.tool.write_text("#!" + sys.executable + "\nimport time\ntime.sleep(5)\n")
        with mock.patch.object(prepare, "STEP_SECONDS", 0.1):
            result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertIn("deadline", manifest["error"])
        self.assertIsNotNone(manifest["steps"][0]["exit_code"])

    def test_bad_section_offsets_and_wrong_machine_are_rejected(self):
        for offset, fmt, value in ((18, "H", 62), (40, "Q", 2**63),
                                   (62, "H", 500), (64 + 64 + 24, "Q", 2**63)):
            with self.subTest(offset=offset):
                data = bytearray(elf_fixture())
                struct.pack_into("<" + fmt, data, offset, value)
                self.fixture.write_bytes(data)
                with self.assertRaises(prepare.PreparationError):
                    prepare.check_elf(self.fixture)

    def test_original_changes_do_not_change_private_compilation_inputs(self):
        originals = {"source": self.source.read_text(), "btf": self.btf.read_text()}
        self.mutation.write_text(json.dumps({str(self.source): "changed C", str(self.btf): "changed BTF"}))
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 0)
        observed = dict(json.loads(line) for line in self.observed.read_text().splitlines())
        self.assertEqual(observed, originals)
        self.assertEqual(self.source.read_text(), "changed C")
        self.assertEqual(self.btf.read_text(), "changed BTF")
        for name in ("source", "btf"):
            snapshot = Path(manifest["inputs"][name]["path"])
            self.assertEqual(snapshot.parent, Path(manifest["output_directory"]))
            self.assertEqual(snapshot.read_text(), originals[name])
            self.assertEqual(manifest["inputs"][name]["sha256"], prepare.digest(snapshot))

    def test_tool_mutation_fails_even_when_compile_succeeds(self):
        self.tool_mutation.touch()
        result, manifest, _ = self.run_prepare()
        self.assertEqual(result, 1)
        self.assertEqual(manifest["status"], "failed")
        self.assertIn("tool changed", manifest["error"])
        self.assertEqual(manifest["steps"][-1]["name"], "compile")
        self.assertEqual(manifest["steps"][-1]["exit_code"], 0)
        self.assertFalse(manifest["inputs"]["clang"]["unchanged"])
        self.assertEqual(manifest["verifier"], "unverified")

    def test_snapshots_enforce_actual_read_limits(self):
        for name, patch_name in (("source", "MAX_SOURCE"), ("btf", "MAX_ARTIFACT")):
            with self.subTest(name=name), mock.patch.object(prepare, patch_name, 2):
                result, manifest, _ = self.run_prepare()
                self.assertEqual(result, 1)
                self.assertIn(name + ": size limit exceeded", manifest["error"])
                self.assertFalse(self.calls.exists())

    def test_empty_required_or_invalid_instruction_sections_are_rejected(self):
        # The fixture sorts sections, so find the program and another required name.
        names = sorted(prepare.REQUIRED_SECTIONS)
        program = 64 + (names.index("cgroup/connect4") + 2) * 64
        maps = 64 + (names.index(".maps") + 2) * 64
        for offset, fmt, value in ((program + 32, "Q", 0), (maps + 32, "Q", 0),
                                   (program + 32, "Q", 7), (program + 4, "I", 8)):
            with self.subTest(offset=offset, value=value):
                data = bytearray(elf_fixture())
                struct.pack_into("<" + fmt, data, offset, value)
                self.fixture.write_bytes(data)
                with self.assertRaises(prepare.PreparationError):
                    prepare.check_elf(self.fixture)


if __name__ == "__main__":
    unittest.main()
