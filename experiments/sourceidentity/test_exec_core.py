"""Bootstrap FD ownership tests; no BPF, networking, child processes or exec."""
import contextlib
import errno
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

# Support the requested module command as well as discovery from this directory.
sys.path.insert(0, str(Path(__file__).resolve().parent))
import exec_core as bootstrap


def resources():
    maps = {name: index + 10 for index, name in enumerate(bootstrap.MAPS)}
    programs = {name: index + 20 for index, name in enumerate(bootstrap.PROGRAMS)}
    links = [[name, index + 30] for index, name in enumerate(bootstrap.LINKS)]
    return SimpleNamespace(map_fds=maps, program_fds=programs), SimpleNamespace(links=links)


def handoff():
    groups = {}; fd = 200
    for name, names in (("maps", bootstrap.MAPS), ("programs", bootstrap.PROGRAMS), ("links", bootstrap.LINKS)):
        groups[name] = {}
        for key in names:
            groups[name][key] = fd; fd += 1
    return dict(schema="hoopa-core-identity-handoff-v1", core_generation="fixed",
                generation="18446744073709551615", source_cgroup_id="22", root_cgroup_id="1",
                netns_cookie="3", pidns_inode=4026531836,
                boot_id="01234567-89ab-cdef-0123-456789abcdef",
                source_path="/sys/fs/cgroup/hoopa-online-fixed", port=18993, **groups)


class Copies:
    def __init__(self, fail_duplicate=None, fail_inherit=None, fail_close=None):
        self.trace = []; self.allocated = []; self.closed = []
        self.fail_duplicate = fail_duplicate; self.fail_inherit = fail_inherit; self.fail_close = fail_close
    def duplicate(self, fd):
        self.trace.append(("duplicate", fd))
        if len(self.allocated) == self.fail_duplicate:
            raise OSError(errno.EMFILE, "duplicate limit")
        new = 200 + len(self.allocated); self.allocated.append(new); return new
    def get(self, fd):
        self.trace.append(("get", fd)); return False
    def inherit(self, fd, value):
        self.trace.append(("inherit", fd, value))
        if fd == self.fail_inherit:
            raise OSError(errno.EPERM, "inheritable failure")
    def close(self, fd):
        self.trace.append(("close", fd)); self.closed.append(fd)
        if fd == self.fail_close:
            raise OSError(errno.EINTR, "close interrupted")
    def run(self, backend=None, owner=None, **kw):
        if backend is None: backend, owner = resources()
        return bootstrap.duplicate_resources(backend, owner, duplicate=self.duplicate,
                    inheritable=self.inherit, close=self.close, get_inheritable=self.get, **kw)


class DuplicateTests(unittest.TestCase):
    def test_all_copies_cloexec_before_first_inheritance(self):
        fake = Copies(); groups, fds = fake.run()
        self.assertEqual(fds, list(range(200, 211)))
        self.assertEqual(set(groups["maps"]), set(bootstrap.MAPS))
        self.assertEqual(set(groups["programs"]), set(bootstrap.PROGRAMS))
        self.assertEqual(set(groups["links"]), set(bootstrap.LINKS))
        inherit_at = next(i for i, entry in enumerate(fake.trace) if entry[0] == "inherit")
        self.assertEqual(sum(e[0] == "duplicate" for e in fake.trace[:inherit_at]), 11)
        checked = [e[1] for e in fake.trace[:inherit_at] if e[0] == "get"]
        self.assertEqual(checked[:11], fds)
        backend, owner = resources()
        self.assertEqual(set(checked[11:]), set(backend.map_fds.values()) |
                         set(backend.program_fds.values()) | {fd for _, fd in owner.links})
        self.assertEqual(fake.closed, [])
    def test_partial_duplicate_failure_closes_only_acquired_copies_reverse(self):
        for count in (0, 1, 5, 10):
            with self.subTest(count=count):
                fake = Copies(fail_duplicate=count)
                with self.assertRaisesRegex(OSError, "duplicate limit"): fake.run()
                self.assertEqual(fake.closed, list(reversed(range(200, 200 + count))))
                self.assertFalse(any(e[0] == "inherit" for e in fake.trace))
    def test_partial_inherit_failure_closes_all_once_no_retry(self):
        for fd in (200, 205, 210):
            with self.subTest(fd=fd):
                fake = Copies(fail_inherit=fd)
                with self.assertRaisesRegex(OSError, "inheritable failure"): fake.run()
                self.assertEqual(fake.closed, list(range(210, 199, -1)))
                self.assertEqual([e[1] for e in fake.trace if e[0] == "inherit"], list(range(200, fd + 1)))
    def test_cleanup_error_retains_primary_and_attempts_remaining_once(self):
        fake = Copies(fail_inherit=205, fail_close=209)
        with self.assertRaises(bootstrap.BootstrapCleanupError) as caught: fake.run()
        self.assertIsInstance(caught.exception.__cause__, OSError)
        self.assertIn("inheritable failure", str(caught.exception))
        self.assertIn("close interrupted", str(caught.exception))
        self.assertEqual(fake.closed, list(range(210, 199, -1)))
        self.assertEqual(fake.closed.count(209), 1)
    def test_original_or_new_inheritable_rejected_before_enabling_exports(self):
        for bad in (200, 10):
            with self.subTest(fd=bad):
                fake = Copies(); fake.get = lambda fd: fd == bad
                with self.assertRaisesRegex(ValueError, "CLOEXEC"): fake.run()
                expected = [200] if bad == 200 else list(range(210, 199, -1))
                self.assertEqual(fake.closed, expected)
                self.assertFalse(any(e[0] == "inherit" for e in fake.trace))
    def test_get_inheritable_error_keeps_new_fd_owned(self):
        fake = Copies()
        def get(fd):
            if fd == 203: raise OSError(errno.EBADF, "get flags failed")
            return False
        fake.get = get
        with self.assertRaisesRegex(OSError, "get flags failed"): fake.run()
        self.assertEqual(fake.closed, [203, 202, 201, 200])
    def test_malformed_duplicate_result_never_closes_source_or_external_low_fd(self):
        for bad in (True, None, -1, 0, 199, 10, 200, 1024):
            with self.subTest(result=bad):
                backend, owner = resources(); fake = Copies(); results = iter((200, bad))
                with self.assertRaises(ValueError):
                    bootstrap.duplicate_resources(backend, owner, duplicate=lambda fd: next(results),
                        inheritable=fake.inherit, close=fake.close, get_inheritable=fake.get)
                self.assertEqual(fake.closed, [1024, 200] if bad == 1024 else [200])
    def test_malformed_copy_alias_cannot_close_high_numbered_original(self):
        for original in (200, 1024):
            with self.subTest(fd=original):
                backend, owner = resources(); backend.map_fds["capture_config"] = original
                close = Mock()
                with self.assertRaises(ValueError):
                    bootstrap.duplicate_resources(backend, owner, duplicate=lambda fd: original,
                        inheritable=Mock(), close=close, get_inheritable=lambda fd: False)
                close.assert_not_called()
    def test_invalid_source_sets_touch_no_fd(self):
        for mode in ("unknown-map", "missing-link", "alias-source", "none-link"):
            with self.subTest(mode=mode):
                backend, owner = resources()
                if mode == "unknown-map": backend.map_fds["unknown"] = 99
                if mode == "missing-link": owner.links.pop()
                if mode == "alias-source": owner.links[0][1] = backend.map_fds["capture_config"]
                if mode == "none-link": owner.links[0][1] = None
                fake = Copies()
                with self.assertRaises(ValueError): fake.run(backend, owner)
                self.assertEqual(fake.trace, [])


class HandoffTests(unittest.TestCase):
    def test_exact_record_preserves_u64_strings_and_canonical_encoding(self):
        value = handoff(); encoded = bootstrap.encode_handoff(value)
        self.assertEqual(json.loads(encoded), value)
        self.assertIsInstance(json.loads(encoded)["generation"], str)
        self.assertLessEqual(len(encoded.encode()), 8192)
        self.assertEqual(encoded, bootstrap.encode_handoff(value))
    def test_rejects_noncanonical_or_unknown_fields(self):
        for field, values in {
            "generation": ["0", "01", "+1", "18446744073709551616", 1, True],
            "pidns_inode": [0, True, 2**32], "port": [0, True, 65536],
            "boot_id": ["01234567-89AB-cdef-0123-456789abcdef", "bad"],
            "source_path": ["/sys/fs/cgroup/hoopa-online-x/", "/sys/fs/cgroup/hoopa-online-x/y", "/sys/fs/cgroup/hoopa-online-../x"],
            "schema": ["wrong"], "core_generation": ["", None],
        }.items():
            for invalid in values:
                with self.subTest(field=field, invalid=invalid):
                    value = handoff(); value[field] = invalid
                    with self.assertRaises(ValueError): bootstrap.encode_handoff(value)
        value = handoff(); value["extra"] = 1
        with self.assertRaises(ValueError): bootstrap.encode_handoff(value)
        value = handoff(); value["source_cgroup_id"] = value["root_cgroup_id"]
        with self.assertRaises(ValueError): bootstrap.encode_handoff(value)
    def test_rejects_fd_aliases_unknowns_and_size_bound(self):
        for mode in ("alias", "low", "high", "bool", "unknown", "missing", "oversize"):
            with self.subTest(mode=mode):
                value = handoff()
                if mode == "alias": value["links"]["hoopa_connect"] = 200
                if mode == "low": value["maps"]["capture_config"] = 199
                if mode == "high": value["maps"]["capture_config"] = 1024
                if mode == "bool": value["maps"]["capture_config"] = True
                if mode == "unknown": value["maps"]["other"] = 222
                if mode == "missing": value["maps"].pop("capture_config")
                if mode == "oversize": value["core_generation"] = "a" * 8192
                with self.assertRaises(ValueError): bootstrap.encode_handoff(value)


class CorePinTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / "core"
        self.data = b"\x7fELFcontrolled-test-bytes"
        self.path.write_bytes(self.data); self.path.chmod(0o700)
        self.sha = hashlib.sha256(self.data).hexdigest()
    def test_verified_same_regular_fd_is_cloexec_even_if_path_replaced(self):
        fd = bootstrap.open_core(str(self.path), self.sha)
        try:
            self.assertFalse(os.get_inheritable(fd))
            self.assertTrue(stat.S_ISREG(os.fstat(fd).st_mode))
            replacement = self.path.with_name("other"); replacement.write_bytes(b"different")
            os.replace(str(replacement), str(self.path))
            os.lseek(fd, 0, os.SEEK_SET)
            self.assertEqual(os.read(fd, len(self.data) + 1), self.data)
        finally: os.close(fd)
    def test_bad_hash_permission_type_and_symlink_refuse(self):
        for kind in ("hash", "not-executable", "directory", "symlink", "empty"):
            with self.subTest(kind=kind):
                path = self.path; expected = self.sha
                if kind == "hash": expected = "0" * 64
                if kind == "not-executable": self.path.chmod(0o600)
                if kind == "directory": path = Path(self.temp.name)
                if kind == "symlink":
                    path = self.path.with_name("link"); path.symlink_to(self.path)
                if kind == "empty":
                    path = self.path.with_name("empty"); path.touch(); path.chmod(0o700)
                with self.assertRaises((ValueError, OSError)): bootstrap.open_core(str(path), expected)
                self.path.chmod(0o700)
    def test_invalid_pin_does_not_open_path(self):
        with patch.object(bootstrap.os, "open") as opened:
            for path, sha in (("relative", self.sha), (str(self.path), "bad")):
                with self.assertRaises(ValueError): bootstrap.open_core(path, sha)
            opened.assert_not_called()
    def test_fifo_is_rejected_without_waiting_for_a_writer(self):
        path = self.path.with_name("fifo")
        os.mkfifo(str(path), 0o700)
        with self.assertRaisesRegex(ValueError, "regular core"):
            bootstrap.open_core(str(path), self.sha)
    def test_oversize_fd_is_closed_before_read(self):
        info = SimpleNamespace(st_mode=stat.S_IFREG | 0o700, st_size=128*1024*1024+1)
        with patch.object(bootstrap.os, "open", return_value=400), \
             patch.object(bootstrap.os, "fstat", return_value=info), \
             patch.object(bootstrap.os, "read") as read, \
             patch.object(bootstrap.os, "close") as close:
            with self.assertRaisesRegex(ValueError, "bounded regular"):
                bootstrap.open_core(str(self.path), self.sha)
            read.assert_not_called()
            close.assert_called_once_with(400)
    def test_short_read_and_cleanup_failure_preserve_both_errors(self):
        info = SimpleNamespace(st_mode=stat.S_IFREG | 0o700, st_size=10)
        with patch.object(bootstrap.os, "open", return_value=400), \
             patch.object(bootstrap.os, "fstat", return_value=info), \
             patch.object(bootstrap.os, "read", return_value=b""), \
             patch.object(bootstrap.os, "close", side_effect=OSError(errno.EINTR, "close-pin")) as close:
            with self.assertRaises(bootstrap.BootstrapCleanupError) as caught:
                bootstrap.open_core(str(self.path), self.sha)
            self.assertIn("short core read", str(caught.exception))
            self.assertIn("close-pin", str(caught.exception))
            close.assert_called_once_with(400)


def arguments(allow=True):
    result = []
    if allow: result.append("--allow-bpf-load")
    values = dict(object="/work/online.bpf.o", object_sha256="a"*64,
                  libbpf="/work/libbpf.so", libbpf_sha256="b"*64,
                  core="/work/core", core_sha256="c"*64, core_config="/work/config.json",
                  core_home="/work/home", core_generation="fixed", scope="/sys/fs/cgroup/hoopa-online-fixed",
                  output="/work/output", generation="7", netns_cookie="3", port="18993")
    for name, value in values.items(): result.extend(("--"+name.replace("_", "-"), value))
    return result


class MainTests(unittest.TestCase):
    def test_default_deny_before_any_input_or_bpf(self):
        with patch.object(bootstrap, "pinned") as pinned, \
             patch.object(bootstrap, "NativeBackend") as native, \
             patch.object(bootstrap.os, "open") as opened, \
             patch.object(bootstrap.os, "geteuid") as euid, \
             patch.object(bootstrap.Path, "read_text") as read, \
             contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit) as caught: bootstrap.main(arguments(False))
            self.assertEqual(caught.exception.code, 2)
            for mock in (pinned, native, opened, euid, read): mock.assert_not_called()
    def main_context(self, stack, exec_error=None, owner_error=False, close_error=False):
        backend, links_owner = resources()
        owner = SimpleNamespace(links=links_owner.links, start=Mock(return_value={}),
                                close=Mock(return_value={"errors": []}))
        if owner_error: owner.close.side_effect = RuntimeError("owner cleanup failed")
        placed = [False]
        def read_text(path):
            path = str(path)
            if path == "/proc/cmdline": return "console=ttyS0 hoopa_lab=t132"
            if path.endswith("cgroup.type"): return "domain"
            if path.endswith("cgroup.procs"): return ""
            if path == "/proc/self/cgroup": return "0::/hoopa-online-fixed" if placed[0] else "0::/"
            if path == "/proc/self/mountinfo": return "27 25 0:25 / /sys/fs/cgroup rw - cgroup2 cgroup2 rw"
            if path.endswith("boot_id"): return "01234567-89ab-cdef-0123-456789abcdef"
            raise AssertionError(path)
        def write_text(path, data): placed[0] = True; return len(data)
        def path_stat(path): return SimpleNamespace(st_ino=22 if str(path).endswith("hoopa-online-fixed") else 1)
        def os_stat(path): return SimpleNamespace(st_ino=4026531836)
        opened = Mock(side_effect=[90, 91])
        close = Mock(side_effect=(lambda fd: (_ for _ in ()).throw(OSError(errno.EINTR, "root close failed")) if fd == 90 and close_error else None))
        stream = io.StringIO()
        # No real file/socket/BPF call occurs in this full bootstrap exercise.
        for target, value in (("geteuid", Mock(return_value=0)), ("stat", os_stat),
                              ("open", opened), ("close", close), ("getpid", Mock(return_value=72))):
            stack.enter_context(patch.object(bootstrap.os, target, value))
        stack.enter_context(patch.object(bootstrap.os, "supports_fd", {bootstrap.os.execve}))
        stack.enter_context(patch.object(bootstrap.os, "fdopen", return_value=contextlib.nullcontext(stream)))
        for target, value in (("read_text", read_text), ("write_text", write_text),
                              ("stat", path_stat), ("is_symlink", lambda p: False), ("mkdir", lambda *a, **k: None)):
            stack.enter_context(patch.object(bootstrap.Path, target, value))
        stack.enter_context(patch.dict(bootstrap.os.environ, HOOPA_GUARD_GENERATION="fixed", HOOPA_GUARD_CONFIG_SHA256="d"*64))
        stack.enter_context(patch.object(bootstrap, "pinned", return_value=b"object"))
        core = stack.enter_context(patch.object(bootstrap, "open_core", return_value=89))
        stack.enter_context(patch.object(bootstrap, "NativeBackend", return_value=backend))
        stack.enter_context(patch.object(bootstrap, "AdmissionOwner", return_value=owner))
        exports = handoff(); fdset = {name: exports[name] for name in ("maps", "programs", "links")}
        stack.enter_context(patch.object(bootstrap, "duplicate_resources", return_value=(fdset, list(range(200, 211)))))
        # Save original execve in supports_fd before substituting the callable.
        execute = stack.enter_context(patch.object(bootstrap.os, "execve", side_effect=exec_error))
        bootstrap.os.supports_fd.add(execute)
        return owner, close, execute, core, stream
    def test_exec_uses_exact_hashed_fd_and_strict_handoff_then_failure_closes(self):
        with contextlib.ExitStack() as stack:
            owner, close, execute, core, stream = self.main_context(stack, OSError(errno.ENOEXEC, "exec failed"))
            with self.assertRaisesRegex(OSError, "exec failed"): bootstrap.main(arguments())
            core.assert_called_once_with("/work/core", "c"*64)
            fd, argv, env = execute.call_args[0]
            self.assertEqual(fd, 89)
            self.assertEqual(argv, ["/work/core", "-d", "/work/home", "-f", "/work/config.json"])
            record = json.loads(env["HOOPA_IDENTITY_HANDOFF"])
            self.assertEqual(bootstrap.encode_handoff(record), env["HOOPA_IDENTITY_HANDOFF"])
            self.assertEqual(record["pidns_inode"], 4026531836)
            self.assertEqual([c.args[0] for c in close.call_args_list], list(range(210, 199, -1)) + [90, 89])
            owner.close.assert_called_once_with()
            self.assertEqual(json.loads(stream.getvalue())["handoff"], record)
    def test_owner_and_fd_cleanup_errors_preserve_exec_error_and_all_closes(self):
        with contextlib.ExitStack() as stack:
            owner, close, execute, core, stream = self.main_context(stack, OSError(errno.ENOEXEC, "exec failed"), True, True)
            with self.assertRaises(bootstrap.BootstrapCleanupError) as caught: bootstrap.main(arguments())
            text = str(caught.exception)
            for expected in ("exec failed", "owner cleanup failed", "root close failed"): self.assertIn(expected, text)
            self.assertEqual([c.args[0] for c in close.call_args_list], list(range(210, 199, -1)) + [90, 89])
    def test_duplicate_failure_is_not_reclosed_by_outer_cleanup(self):
        actual_duplicate = bootstrap.duplicate_resources
        fake = Copies(fail_duplicate=5)
        with contextlib.ExitStack() as stack:
            owner, close, execute, core, stream = self.main_context(stack)
            def duplicate(backend, owner, profile="online"):
                self.assertEqual(profile, "online")
                return actual_duplicate(backend, owner, duplicate=fake.duplicate,
                    inheritable=fake.inherit, close=fake.close, get_inheritable=fake.get)
            stack.enter_context(patch.object(bootstrap, "duplicate_resources", side_effect=duplicate))
            with self.assertRaisesRegex(OSError, "duplicate limit"): bootstrap.main(arguments())
            self.assertEqual(fake.closed, [204, 203, 202, 201, 200])
            self.assertEqual([c.args[0] for c in close.call_args_list], [90, 89])
            owner.close.assert_called_once()
            execute.assert_not_called()
    def test_owner_start_failure_still_releases_root_and_verified_core(self):
        with contextlib.ExitStack() as stack:
            owner, close, execute, core, stream = self.main_context(stack)
            owner.start.side_effect = RuntimeError("start failed")
            duplicate = stack.enter_context(patch.object(bootstrap, "duplicate_resources"))
            with self.assertRaisesRegex(RuntimeError, "start failed"): bootstrap.main(arguments())
            self.assertEqual([c.args[0] for c in close.call_args_list], [90, 89])
            owner.close.assert_called_once()
            duplicate.assert_not_called()
            execute.assert_not_called()
    def test_core_pin_failure_precedes_backend_and_has_no_outer_owned_fd(self):
        with contextlib.ExitStack() as stack:
            owner, close, execute, core, stream = self.main_context(stack)
            core.side_effect = ValueError("core pin failed")
            native = stack.enter_context(patch.object(bootstrap, "NativeBackend"))
            with self.assertRaisesRegex(ValueError, "core pin failed"): bootstrap.main(arguments())
            close.assert_not_called()
            owner.close.assert_not_called()
            native.assert_not_called()
            execute.assert_not_called()
    def test_exec_return_is_failure_and_releases_resources(self):
        with contextlib.ExitStack() as stack:
            owner, close, execute, core, stream = self.main_context(stack)
            with self.assertRaisesRegex(RuntimeError, "unexpectedly returned"): bootstrap.main(arguments())
            self.assertEqual(close.call_count, 13)
            owner.close.assert_called_once()


if __name__ == "__main__": unittest.main()
