"""Runner tests: fake all native/kernel inputs; only output artifacts touch disk."""
import contextlib
import ctypes
import errno
import io
import json
import os
from pathlib import Path
import stat
import socket
import subprocess
import struct
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import run_observer as runner

ROOT = Path(__file__).resolve().parents[2]


def elf():
    raw = bytearray(64)
    raw[:7] = b"\x7fELF\x02\x01\x01"
    struct.pack_into("<HHI", raw, 16, 1, 247, 1)
    return bytes(raw)


def session(status="observed_unverified", errors=None):
    return dict(schema="sourceidentity-loader-session-v1", status=status,
                errors=errors or [], diagnostic_snapshot=None,
                quiescence="unverified", completeness="unverified",
                association="unverified", identity_capability="unverified")


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="runner-tests-", dir=ROOT / "private")
        self.addCleanup(self.directory.cleanup)
        self.parent = Path(self.directory.name)
        self.args = ["--object", "/fake/object", "--object-sha256", "a" * 64,
                     "--libbpf", "/fake/library", "--libbpf-sha256", "b" * 64,
                     "--cgroup", "/fake/cgroup", "--output-parent", str(self.parent),
                     "--generation", "1", "--netns-cookie", "2", "--destination-port", "443",
                     "--duration", "0.01"]

    def replace(self, option, value):
        result = list(self.args)
        result[result.index(option) + 1] = str(value)
        return result

    def invoke(self, args=None, observe=None, pinned=None):
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        self.process = stack.enter_context(patch.object(subprocess, "Popen", side_effect=AssertionError("process launch forbidden")))
        self.network = stack.enter_context(patch.object(socket, "socket", side_effect=AssertionError("network forbidden")))
        self.native = stack.enter_context(patch.object(runner, "NativeBackend"))
        self.target = stack.enter_context(patch.object(runner, "open_target", return_value=(987654, Path("/fake/cgroup"))))
        self.empty = stack.enter_context(patch.object(runner, "check_empty_leaf"))
        self.inputs = stack.enter_context(patch.object(runner, "pinned_input", side_effect=pinned or [(Path("/fake/object"), elf()), (Path("/fake/library"), b"lib"), (Path("/fake/library"), b"lib")]))
        self.observer = stack.enter_context(patch.object(runner, "observe", side_effect=observe or self.success))
        stack.enter_context(patch.object(runner.platform, "system", return_value="Linux"))
        stack.enter_context(patch.object(runner.platform, "machine", return_value="x86_64"))
        stack.enter_context(patch.object(runner.platform, "release", return_value="fake-kernel"))
        stack.enter_context(patch.object(runner.os, "getpid", return_value=321))
        self.signals = stack.enter_context(patch.object(runner.signal, "signal", return_value="old"))
        stack.enter_context(patch.object(runner.ctypes, "CDLL", side_effect=AssertionError("real CDLL forbidden")))
        real_open, real_stat, real_fstat, real_close = open, os.stat, os.fstat, os.close
        real_read_text = Path.read_text
        def file_open(path, *a, **kw):
            if str(path) == "/sys/kernel/btf/vmlinux": return io.BytesIO(b"fake btf")
            if str(path).startswith(("/proc/", "/sys/")): raise AssertionError("unexpected kernel file input")
            return real_open(path, *a, **kw)
        def proc_stat(path, *a, **kw):
            if str(path) in ("/proc/self/ns/net", "/proc/self/ns/pid", "/proc/self/ns/time"): return SimpleNamespace(st_ino=12)
            if str(path).startswith(("/proc/", "/sys/")): raise AssertionError("unexpected kernel stat input")
            return real_stat(path, *a, **kw)
        def proc_text(path, *a, **kw):
            if str(path) == "/proc/sys/kernel/random/boot_id": return "fake-boot\n"
            if str(path).startswith(("/proc/", "/sys/")): raise AssertionError("unexpected kernel text input")
            return real_read_text(path, *a, **kw)
        stack.enter_context(patch("builtins.open", side_effect=file_open))
        stack.enter_context(patch.object(runner.os, "stat", side_effect=proc_stat))
        stack.enter_context(patch.object(runner.os, "fstat", side_effect=lambda fd: SimpleNamespace(st_ino=99, st_dev=7) if fd == 987654 else real_fstat(fd)))
        self.closed = []
        def close(fd):
            if fd == 987654: self.closed.append(fd)
            else: return real_close(fd)
        stack.enter_context(patch.object(runner.os, "close", side_effect=close))
        stack.enter_context(patch.object(Path, "read_text", proc_text))
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            code = runner.main((args or self.args) + ["--allow-bpf-load"])
        self.output = [json.loads(line) for line in output.getvalue().splitlines()]
        stack.close()
        return code

    def success(self, backend, config, fd, duration, emit, **kwargs):
        self.assertEqual(fd, 987654)
        self.assertEqual(struct.unpack("<QQQII", config), (1, 99, 2, 443, 0))
        kwargs["check_target"]()
        self.assertFalse(kwargs["cancelled"]())
        emit(dict(status="attached_observer", association="unverified"))
        return session()

    def test_without_allow_refuses_before_read_or_native(self):
        output = io.StringIO()
        with patch.object(runner, "pinned_input", side_effect=AssertionError("read forbidden")) as inputs, patch.object(runner, "NativeBackend", side_effect=AssertionError("native forbidden")) as native, patch.object(runner, "open_target", side_effect=AssertionError("target forbidden")), contextlib.redirect_stdout(output):
            self.assertEqual(runner.main(self.args), 2)
        inputs.assert_not_called(); native.assert_not_called()
        self.assertEqual(json.loads(output.getvalue())["status"], "refused")
        self.assertEqual(list(self.parent.iterdir()), [])

    def test_success_private_outputs_and_unverified(self):
        self.assertEqual(self.invoke(), 0)
        directory = Path(self.output[-1]["directory"])
        self.assertEqual(stat.S_IMODE(directory.stat().st_mode), 0o700)
        self.assertEqual({p.name for p in directory.iterdir()}, {"input.json", "attached.json", "result.json"})
        for path in directory.iterdir(): self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        result = json.loads((directory / "result.json").read_text())
        self.assertEqual(result["status"], "observed_unverified")
        for key in ("quiescence", "completeness", "association", "identity_capability"):
            self.assertEqual(result[key], "unverified")
        self.assertEqual(self.closed, [987654])
        self.assertEqual(self.inputs.call_count, 3)
        self.assertEqual(self.signals.call_count, 4)
        self.native.assert_called_once_with(Path("/fake/library"), elf())
        self.assertEqual(self.native.return_value.mock_calls, [])
        self.process.assert_not_called(); self.network.assert_not_called()

    def test_gated_profile_explicitly_selects_backend_and_session(self):
        self.assertEqual(self.invoke(self.args + ["--profile", "gated"]), 0)
        self.native.assert_called_once_with(Path("/fake/library"), elf(), profile="gated")
        self.assertTrue(self.observer.call_args.kwargs["gated"])
        directory = Path(self.output[-1]["directory"])
        self.assertEqual(json.loads((directory / "input.json").read_text())["profile"], "gated")

    def test_namespaced_profile_reaches_backend_session_and_manifest(self):
        self.assertEqual(self.invoke(self.args + ["--profile", "namespaced"]), 0)
        self.native.assert_called_once_with(Path("/fake/library"), elf(), profile="namespaced")
        self.assertEqual(self.observer.call_args.kwargs["profile"], "namespaced")
        self.assertTrue(self.observer.call_args.kwargs["gated"])
        directory = Path(self.output[-1]["directory"])
        self.assertEqual(json.loads((directory / "input.json").read_text())["profile"], "namespaced")

    def test_attached_marker_binds_input_boot_and_monotonic_clock(self):
        import hashlib
        with patch.object(runner.time, "monotonic_ns", return_value=2**63 + 1):
            self.assertEqual(self.invoke(self.args + ["--profile", "namespaced"]), 0)
        directory = Path(self.output[-1]["directory"])
        raw = (directory / "input.json").read_bytes()
        marker = json.loads((directory / "attached.json").read_text())
        self.assertEqual(marker["input_sha256"], hashlib.sha256(raw).hexdigest())
        self.assertEqual(marker["boot_id"], "fake-boot")
        self.assertEqual(marker["observed_monotonic_ns"], str(2**63 + 1))
        self.assertEqual(json.loads(raw)["observer_timens_inode"], "12")

    def test_range_rejections_precede_input(self):
        for option, values in (("--duration", (0, -1, 61, "nan", "inf")), ("--generation", (0, -1, 2**64)), ("--netns-cookie", (0, 2**64)), ("--destination-port", (0, 65536))):
            for value in values:
                with self.subTest(option=option, value=value):
                    self.assertEqual(self.invoke(self.replace(option, value)), 1)
                    self.inputs.assert_not_called(); self.native.assert_not_called(); self.target.assert_not_called()

    def test_platform_rejection(self):
        for system, machine in (("Darwin", "x86_64"), ("Linux", "aarch64")):
            # Outer invoke patches default platform; use side effect at the input boundary
            # through independent main call to establish rejection happens first.
            with patch.object(runner.platform, "system", return_value=system), patch.object(runner.platform, "machine", return_value=machine), patch.object(runner, "pinned_input") as inputs, patch.object(runner, "NativeBackend") as native, contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(runner.main(self.args + ["--allow-bpf-load"]), 1)
                inputs.assert_not_called(); native.assert_not_called()

    def test_invalid_header_and_input_path(self):
        for data in (b"short", bytes(64), elf()[:16] + bytes(48)):
            with self.subTest(data=data[:7]):
                self.assertEqual(self.invoke(pinned=[(Path("/fake/object"), data)]), 1)
                self.native.assert_not_called(); self.target.assert_not_called()
        self.assertEqual(self.invoke(pinned=ValueError("absolute input path required")), 1)
        self.assertIn("absolute input path", self.output[-1]["errors"][0]["message"])
        self.native.assert_not_called()

    def test_output_parent_path_rejected(self):
        for value in ("relative-output", self.parent / "missing"):
            self.assertEqual(self.invoke(self.replace("--output-parent", value)), 1)
            self.native.assert_not_called(); self.target.assert_not_called()

    def test_session_failure_and_digest_failure_both_preserved(self):
        primary = dict(stage="load_object", type="NativeError", message="primary load error", ret=-22, errno=22)
        self.assertEqual(self.invoke(observe=lambda *a, **kw: session("failed", [primary]), pinned=[(Path("/fake/object"), elf()), (Path("/fake/library"), b"lib"), ValueError("digest changed")]), 1)
        errors = self.output[-1]["errors"]
        self.assertEqual(errors[0], primary)
        self.assertIn("digest changed", errors[1]["message"])
        self.assertEqual(self.closed, [987654])

    def test_backend_constructor_and_digest_failures_both_preserved(self):
        # invoke installs its own NativeBackend mock; fail through observe is
        # insufficient here, so configure that mock at the final input boundary.
        calls = [0]
        def inputs(path, digest):
            calls[0] += 1
            if calls[0] == 2:
                self.native.side_effect = RuntimeError("primary constructor exception")
            if calls[0] == 3: raise ValueError("constructor digest changed")
            return (Path("/fake/object"), elf()) if calls[0] == 1 else (Path("/fake/library"), b"lib")
        self.assertEqual(self.invoke(pinned=inputs), 1)
        messages = [item["message"] for item in self.output[-1]["errors"]]
        self.assertTrue(any("primary constructor exception" in message for message in messages), messages)
        self.assertTrue(any("constructor digest changed" in message for message in messages), messages)
        self.observer.assert_not_called()
        self.assertEqual(self.closed, [987654])

    def test_raised_primary_and_digest_failure_both_preserved(self):
        def fail(*args, **kwargs): raise RuntimeError("primary observer exception")
        self.assertEqual(self.invoke(observe=fail, pinned=[(Path("/fake/object"), elf()), (Path("/fake/library"), b"lib"), ValueError("digest changed")]), 1)
        messages = [item["message"] for item in self.output[-1]["errors"]]
        self.assertTrue(any("primary observer exception" in message for message in messages), messages)
        self.assertTrue(any("digest changed" in message for message in messages), messages)
        self.assertEqual(self.closed, [987654])


class CgroupAdmissionTests(unittest.TestCase):
    def test_cgroup2_magic_and_fstatfs_failure_use_fake_libc(self):
        function = Mock()
        library = SimpleNamespace(fstatfs=function)
        with patch.object(runner.ctypes, "CDLL", return_value=library) as cdll:
            def fill(fd, buffer): struct.pack_into("<q", buffer, 0, runner.CGROUP2_SUPER_MAGIC); return 0
            function.side_effect = fill
            self.assertTrue(runner.cgroup2_fd(55))
            function.side_effect = lambda fd, buffer: 0
            self.assertFalse(runner.cgroup2_fd(55))
            def fail(fd, buffer): ctypes.set_errno(errno.EBADF); return -1
            function.side_effect = fail
            with self.assertRaises(OSError): runner.cgroup2_fd(55)
            self.assertEqual(function.argtypes, [ctypes.c_int, ctypes.c_void_p])
            cdll.assert_called_with(None, use_errno=True)

    def test_empty_domain_leaf_prechecks(self):
        for kind, procs, children, valid in ((b"domain\n", b"", [], True), (b"threaded", b"", [], False), (b"domain", b"123", [], False), (b"domain", b"", [SimpleNamespace(is_dir=lambda **kw: True)], False)):
            with self.subTest(kind=kind, procs=procs, children=bool(children)):
                with patch.object(runner, "read_at", side_effect=[kind, procs]), patch.object(runner.os, "scandir") as scan:
                    scan.return_value.__enter__.return_value = iter(children)
                    if valid: runner.check_empty_leaf(55)
                    else:
                        with self.assertRaises(ValueError): runner.check_empty_leaf(55)

    def test_open_target_rejects_relative_non_cgroup_root_and_nonleaf(self):
        with patch.object(runner.os, "open") as opened:
            with self.assertRaises(ValueError): runner.open_target("relative")
            opened.assert_not_called()
        for magic, same_inode, leaf_error, expected_closes in (([False], False, False, [10]), ([True, False], False, False, [11, 10]), ([True, True], True, False, [11, 10]), ([True, True], False, True, [11, 10]), ([True, True], False, False, [11])):
            with self.subTest(magic=magic, same_inode=same_inode, leaf_error=leaf_error):
                with patch.object(Path, "resolve", return_value=Path("/fake/cgroup")), patch.object(runner.os, "open", side_effect=[10, 11]), patch.object(runner, "cgroup2_fd", side_effect=magic), patch.object(runner.os, "fstat", side_effect=lambda fd: SimpleNamespace(st_ino=1 if same_inode or fd == 10 else 2)), patch.object(runner.os, "close") as close, patch.object(runner, "check_empty_leaf", side_effect=ValueError("not empty") if leaf_error else None):
                    if expected_closes == [11]: self.assertEqual(runner.open_target("/fake/cgroup"), (10, Path("/fake/cgroup")))
                    else:
                        with self.assertRaises(ValueError): runner.open_target("/fake/cgroup")
                    self.assertEqual([call.args[0] for call in close.call_args_list], expected_closes)

    def test_read_at_bound_and_open_flags(self):
        for data, valid in ((b"domain\n", True), (b"x" * 65537, False)):
            with patch.object(runner.os, "open", return_value=55) as opened, patch.object(runner.os, "fdopen", return_value=io.BytesIO(data)):
                if valid: self.assertEqual(runner.read_at(10, "cgroup.type"), data)
                else:
                    with self.assertRaises(ValueError): runner.read_at(10, "cgroup.type")
                opened.assert_called_once_with("cgroup.type", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=10)


if __name__ == "__main__": unittest.main()
