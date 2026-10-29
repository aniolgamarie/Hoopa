import os
import selectors
import subprocess
import sys
import time
import unittest
from unittest import mock

from fixture_processes import ChildSet, ProcessError


class FakeProcess:
    def __init__(self):
        self.pid = 123
        self.code = None
        self.kills = 0
        self.writers = {}
        for name in ("stdout", "stderr"):
            reader, writer = os.pipe()
            setattr(self, name, os.fdopen(reader, "rb", buffering=0))
            self.writers[name] = writer

    def emit(self, name, data):
        os.write(self.writers[name], data)

    def eof(self, name):
        writer = self.writers.pop(name, None)
        if writer is not None:
            os.close(writer)

    def poll(self):
        return self.code

    def kill(self):
        self.kills += 1
        self.code = -9
        for name in list(self.writers):
            self.eof(name)


class ChildSetTests(unittest.TestCase):
    def fixture(self, **kwargs):
        proc = FakeProcess()
        factory = mock.Mock(return_value=proc)
        children = ChildSet(popen=factory, **kwargs)
        self.addCleanup(children.close)
        self.addCleanup(lambda: [proc.eof(n) for n in list(proc.writers)])
        children.spawn("fixture", ["program", "a; echo literal"])
        return children, proc, factory

    def test_arguments_and_independent_snapshots(self):
        children, proc, factory = self.fixture(clock=lambda: 37)
        factory.assert_called_once_with(
            ["program", "a; echo literal"], stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, shell=False, bufsize=0)
        view = children.views()["fixture"]
        self.assertEqual(view["started_monotonic_ns"], "37")
        self.assertEqual(view["pid"], proc.pid)
        view["argv"].append("changed")
        view["stdout"] = b"changed"
        self.assertEqual(children.views()["fixture"]["stdout"], b"")
        self.assertEqual(len(children.views()["fixture"]["argv"]), 2)

    def test_limit_duplicate_and_bad_arguments(self):
        procs = []
        def factory(*args, **kwargs):
            proc = FakeProcess()
            procs.append(proc)
            return proc
        children = ChildSet(popen=factory)
        self.addCleanup(children.close)
        for invalid in ("program", [], [42]):
            with self.assertRaises(ProcessError):
                children.spawn("bad", invalid)
        for label in ("a", "b", "c"):
            children.spawn(label, ["program"])
        for label in ("a", "d"):
            with self.assertRaises(ProcessError):
                children.spawn(label, ["program"])
        self.assertEqual(len(procs), 3)

    def test_segmented_streams_eof_before_exit_and_exit_before_eof(self):
        children, proc, _ = self.fixture(clock=lambda: 99)
        proc.emit("stdout", b"first")
        proc.emit("stderr", b"err")
        children.pump(0)
        proc.emit("stdout", b"second")
        proc.eof("stderr")
        children.pump(0)
        view = children.views()["fixture"]
        self.assertEqual(view["stdout"], b"firstsecond")
        self.assertEqual(view["stderr"], b"err")
        self.assertTrue(view["stderr_eof"])
        self.assertIsNone(view["exit_code"])
        proc.code = 7
        children.pump(0)
        view = children.views()["fixture"]
        self.assertEqual(view["exit_code"], 7)
        self.assertEqual(view["observed_exit_monotonic_ns"], "99")
        self.assertFalse(view["stdout_eof"])
        proc.eof("stdout")
        children.pump(0)
        self.assertEqual(children.close(), [])
        self.assertEqual(proc.kills, 0)
        self.assertEqual(children.views()["fixture"]["stdout"], b"firstsecond")

    def test_output_overflow_on_each_stream_retains_prefix(self):
        for name in ("stdout", "stderr"):
            with self.subTest(stream=name):
                children, proc, _ = self.fixture()
                for _ in range(16):
                    proc.emit(name, b"x" * 4096)
                    children.pump(0)
                self.assertFalse(children.views()["fixture"]["truncated"])
                proc.emit(name, b"overflow")
                with self.assertRaisesRegex(ProcessError, name + " exceeded"):
                    children.pump(0)
                view = children.views()["fixture"]
                self.assertEqual(view[name], b"x" * 65536)
                self.assertTrue(view["truncated"])
                children.close()
                self.assertEqual(proc.kills, 1)
                self.assertEqual(children.views()["fixture"]["exit_code"], -9)

    def test_partial_spawn_failure_preserves_owned_child(self):
        children, proc, factory = self.fixture()
        factory.side_effect = OSError("spawn fault")
        with self.assertRaisesRegex(ProcessError, "spawn fault"):
            children.spawn("second", ["missing"])
        self.assertEqual(list(children.views()), ["fixture"])
        self.assertEqual(children.close(), [])
        self.assertEqual(proc.kills, 1)

    def test_pipe_setup_failure_retains_and_closes_both_handles(self):
        proc = FakeProcess()
        children = ChildSet(popen=lambda *a, **kw: proc)
        with mock.patch("fixture_processes.os.set_blocking", side_effect=OSError("setup")):
            with self.assertRaisesRegex(ProcessError, "setup"):
                children.spawn("fixture", ["program"])
        self.assertIn("fixture", children.views())
        children.close()
        self.assertEqual(proc.kills, 1)
        self.assertTrue(proc.stdout.closed)
        self.assertTrue(proc.stderr.closed)

    def test_close_errors_preserved_and_idempotent(self):
        children, proc, _ = self.fixture()
        with mock.patch.object(proc, "kill", side_effect=OSError("kill fault")) as kill:
            with mock.patch.object(children._selector, "select", side_effect=OSError("select fault")):
                errors = children.close()
            self.assertEqual(kill.call_count, 1)
        self.assertTrue(any("kill fault" in error for error in errors))
        self.assertTrue(any("select fault" in error for error in errors))
        self.assertTrue(any("exit could not be confirmed" in error for error in errors))
        self.assertEqual(children.close(), errors)
        errors.append("external mutation")
        self.assertNotIn("external mutation", children.close())
        self.assertTrue(proc.stdout.closed)
        self.assertTrue(proc.stderr.closed)

    def test_close_drains_overflow_and_retains_primary_error(self):
        children, proc, _ = self.fixture()
        for _ in range(16):
            proc.emit("stdout", b"x" * 4096)
            children.pump(0)
        proc.emit("stdout", b"extra")
        errors = children.close()
        self.assertTrue(any("stdout exceeded" in error for error in errors))
        self.assertEqual(children.views()["fixture"]["exit_code"], -9)
        self.assertTrue(children.views()["fixture"]["stdout_eof"])

    def test_handle_and_selector_close_failures_do_not_repeat(self):
        children, proc, _ = self.fixture()
        original = proc.stdout
        handle = mock.Mock(wraps=original)
        def close_handle():
            original.close()
            raise OSError("handle fault")
        handle.close.side_effect = close_handle
        proc.stdout = handle
        original_selector_close = children._selector.close
        def close_selector():
            original_selector_close()
            raise OSError("selector fault")
        with mock.patch.object(children._selector, "close", side_effect=close_selector) as close:
            errors = children.close()
            self.assertEqual(children.close(), errors)
            self.assertEqual(close.call_count, 1)
        self.assertEqual(handle.close.call_count, 1)
        self.assertTrue(any("handle fault" in error for error in errors))
        self.assertTrue(any("selector fault" in error for error in errors))
        self.assertEqual(proc.kills, 1)

    def test_poll_failure_never_kills_an_unconfirmed_process(self):
        children, proc, _ = self.fixture()
        with mock.patch.object(proc, "poll", side_effect=OSError("poll fault")):
            errors = children.close()
        self.assertEqual(proc.kills, 0)
        self.assertTrue(any("poll fault" in error for error in errors))
        self.assertTrue(any("exit could not be confirmed" in error for error in errors))

    def test_timeout_validation(self):
        children, _, _ = self.fixture()
        for timeout in (-1, 0.11, float("nan"), float("inf"), "0"):
            with self.assertRaises(ProcessError):
                children.pump(timeout)

    def test_real_local_python_transport(self):
        children = ChildSet()
        self.addCleanup(children.close)
        children.spawn("python", [sys.executable, "-c",
            "import os,sys; os.write(1,b'out'); os.write(2,b'err'); sys.exit(3)"])
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            children.pump(0.05)
            view = children.views()["python"]
            if (view["exit_code"] is not None and view["stdout_eof"]
                    and view["stderr_eof"]):
                break
        self.assertEqual(view["stdout"], b"out")
        self.assertEqual(view["stderr"], b"err")
        self.assertEqual(view["exit_code"], 3)
        self.assertTrue(view["stdout_eof"] and view["stderr_eof"])
        self.assertEqual(children.close(), [])


if __name__ == "__main__":
    unittest.main()
