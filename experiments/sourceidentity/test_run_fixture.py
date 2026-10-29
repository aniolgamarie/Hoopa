"""Finite orchestration tests; no fixture sockets, cgroup mutation or BPF."""
import copy
from contextlib import ExitStack, contextmanager, redirect_stdout
import hashlib
import io
import json
from pathlib import Path
import stat
import struct
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import MagicMock, mock_open, patch

import run_fixture as runner
from check_fixture_run import CLAIMS, check_run
from test_calibration import bundle

TOKENS = ["1" * 32, "2" * 32]


class Clock:
    def __init__(self): self.value = 1000000000
    def __call__(self): self.value += 1; return self.value


def inputs():
    reg = dict(schema="sourceidentity-calibration-registration-v1", generation="1", cgroup="99",
               netns_cookie="2", port=18991, tokens=TOKENS)
    source = dict(schema="sourceidentity-observer-input-v1", profile="namespaced",
                  config_hex=struct.pack("<QQQII", 1, 99, 2, 18991, 0).hex(), boot_id="boot",
                  observer_pidns_inode="12", observer_timens_inode="13",
                  duration_seconds=30,
                  cgroup=dict(path="/fake/cgroup", inode="99", device="7"))
    raw = json.dumps(source).encode()
    marker = dict(event="attached_observer", generation="1", boot_id="boot",
                  observed_monotonic_ns="100000000", duration_seconds=30,
                  input_sha256=hashlib.sha256(raw).hexdigest())
    return reg, source, marker, raw


class Children:
    def __init__(self, clock, fail=None):
        self.clock, self.fail, self.items, self.closed = clock, fail, {}, 0
    def spawn(self, label, argv):
        if self.fail == "spawn" and label == "client2": raise OSError("spawn denied")
        self.items[label] = dict(label=label, argv=argv, pid=500 + len(self.items),
                                started_monotonic_ns=str(self.clock()), observed_exit_monotonic_ns=None,
                                exit_code=None, stdout=b"", stderr=b"", stdout_eof=False,
                                stderr_eof=False, truncated=False)
    def pump(self, timeout):
        ready = dict(schema="sourceidentity-fixture-v1", event="listening", generation="1", port=18991)
        server = self.items["server"]
        server["stdout"] = (json.dumps(ready) + "\n").encode()
        if self.fail == "early": server["exit_code"] = 1
        if self.fail == "ready": server["stdout"] = b'{"event":"wrong"}\n'
        if len(self.items) != 3 or self.fail == "stall": return
        rows = []
        for i, label in enumerate(("client1", "client2")):
            item = self.items[label]
            shared = dict(schema="sourceidentity-fixture-v1", event="connection", generation="1",
                          identity_capability="unverified", token=TOKENS[i])
            client = dict(shared, role="client", pid=item["pid"])
            if self.fail == "pid": client["pid"] += 100
            item["stdout"] = (json.dumps(client) + "\n").encode()
            rows.append(dict(shared, role="serve", witness={}))
        server["stdout"] += "".join(json.dumps(r) + "\n" for r in rows).encode()
        for item in self.items.values():
            item.update(exit_code=0, observed_exit_monotonic_ns=str(self.clock()),
                        stdout_eof=True, stderr_eof=True)
        if self.fail == "exit": self.items["client1"]["exit_code"] = 2
        if self.fail == "truncate": self.items["client1"]["truncated"] = True
    def views(self): return copy.deepcopy(self.items)
    def close(self):
        self.closed += 1
        return ["cleanup error"] if self.fail == "cleanup" else []


class RunFixtureTests(unittest.TestCase):
    def test_registration_marker_and_clock_window(self):
        reg, source, marker, raw = inputs()
        self.assertEqual(runner.validate_inputs(reg, source, marker, raw, 1000000000), 30100000000)
        for field, value in (("input_sha256", "0" * 64), ("boot_id", "other"),
                             ("observed_monotonic_ns", "2000000000"), ("duration_seconds", 1),
                             ("duration_seconds", True), ("generation", "2")):
            with self.subTest(field=field), self.assertRaises(ValueError):
                runner.validate_inputs(reg, source, dict(marker, **{field: value}), raw, 1000000000)
        reg["tokens"] = [TOKENS[0]]
        with self.assertRaises(ValueError): runner.validate_inputs(reg, source, marker, raw, 1000000000)

    def run_drive(self, failure=None, cancel=lambda: False, guard=lambda: None):
        clock = Clock(); group = Children(clock, failure)
        result = runner.drive(group, "/private/run/fixture", "1", 18991, TOKENS,
                              30100000000, guard=guard, clock=clock, cancelled=cancel)
        self.assertEqual(group.closed, 1)
        return result, group

    def test_order_and_success_preserve_unverified(self):
        result, group = self.run_drive()
        self.assertEqual(result["status"], "completed_unverified")
        self.assertEqual([x["event"] for x in result["events"]],
                         ["preflight_complete", "server_started", "listening_observed",
                          "client1_started", "client2_started", "children_completed"])
        self.assertEqual(list(group.items), ["server", "client1", "client2"])
        self.assertEqual(result["children"]["client1"]["exit_code"], 0)

    def test_failure_paths_cleanup_and_prevent_success(self):
        for failure in ("spawn", "early", "ready", "pid", "exit", "truncate", "stall", "cleanup"):
            with self.subTest(failure=failure):
                result, group = self.run_drive(failure)
                self.assertEqual(result["status"], "failed")
                self.assertTrue(result["errors"])
                if failure in ("early", "ready"): self.assertEqual(list(group.items), ["server"])

    def test_cancel_and_scope_failure_before_spawn(self):
        result, group = self.run_drive(cancel=lambda: True)
        self.assertEqual(result["status"], "failed"); self.assertEqual(group.items, {})
        def fail(): raise ValueError("scope changed")
        result, group = self.run_drive(guard=fail)
        self.assertEqual(result["status"], "failed"); self.assertEqual(group.items, {})

    def test_reserve_rechecked_after_preparation_and_slow_scope_sample(self):
        clock = Clock(); clock.value = 18000000000
        group = Children(clock)
        result = runner.drive(group, "/private/run/fixture", "1", 18991, TOKENS,
                              30100000000, clock=clock)
        self.assertEqual(result["status"], "failed")
        self.assertIn("less than 14 seconds", result["errors"][0]["message"])
        self.assertEqual(group.items, {})
        self.assertEqual(group.closed, 1)
        for advance in (18000000000, 31000000000):
            clock = Clock(); group = Children(clock)
            def slow_scope(): clock.value = advance
            result = runner.drive(group, "/private/run/fixture", "1", 18991, TOKENS,
                                  30100000000, guard=slow_scope, clock=clock)
            self.assertEqual(result["status"], "failed")
            self.assertEqual(group.items, {})
            self.assertEqual(group.closed, 1)

    def test_default_refusal_before_read_spawn_or_directory(self):
        args = ["--registration", "/missing", "--observer-directory", "/missing",
                "--fixture", "/missing", "--fixture-sha256", "a" * 64, "--output-parent", "/missing"]
        with patch.object(runner, "read_input", side_effect=AssertionError("read")), \
             patch.object(runner, "ChildSet", side_effect=AssertionError("spawn")), \
             patch("sys.stdout", new=io.StringIO()) as output:
            self.assertEqual(runner.main(args), 2)
        self.assertEqual(json.loads(output.getvalue())["status"], "refused")

    def test_slow_listening_and_spawn_prevent_further_children(self):
        for mode, expected in (("listening", ["server"]), ("server", ["server"]),
                               ("client1", ["server", "client1"])):
            with self.subTest(mode=mode):
                clock = Clock()
                class SlowChildren(Children):
                    def spawn(self, label, argv):
                        super().spawn(label, argv)
                        if label == mode:
                            clock.value += 18000000000 if mode == "server" else 3000000000
                    def pump(self, timeout):
                        super().pump(timeout)
                        if mode == "listening": clock.value += 3000000000
                group = SlowChildren(clock)
                result = runner.drive(group, "/private/run/fixture", "1", 18991, TOKENS,
                                      30100000000, clock=clock)
                self.assertEqual(result["status"], "failed")
                self.assertIn("reserve", result["errors"][0]["message"])
                self.assertEqual(list(group.items), expected)
                self.assertEqual(group.closed, 1)


class ScopeTests(unittest.TestCase):
    @contextmanager
    def environment(self, **changes):
        state = dict(boot=b"boot\n", pidns=12, timens=13, children_timens=13,
                     inode=99, device=7, parent_inode=1, parent_device=7,
                     filesystem=True, parent_filesystem=True, close_failure=False,
                     members=b"100\n", kind=b"domain\n", childdir=False)
        state.update(changes)
        opened, closed = [], []
        fd_paths = {}
        def open_fd(path, flags, **kwargs):
            fd = 1000 + len(opened)
            opened.append(fd)
            fd_paths[fd] = str(path)
            return fd
        def info(fd):
            parent = fd_paths[fd] != "/fake/cgroup"
            return SimpleNamespace(st_ino=state["parent_inode" if parent else "inode"],
                                   st_dev=state["parent_device" if parent else "device"])
        def proc_stat(path, *args, **kwargs):
            keys = {"/proc/self/ns/pid": "pidns", "/proc/self/ns/time": "timens",
                    "/proc/self/ns/time_for_children": "children_timens"}
            return SimpleNamespace(st_ino=state[keys[str(path)]])
        def read(fd, name):
            return state["kind"] if name == "cgroup.type" else state["members"]
        def close_fd(fd):
            closed.append(fd)
            if state["close_failure"] and fd_paths[fd] == "/fake/cgroup":
                raise OSError("target close fault")
        def scan(fd):
            context = MagicMock()
            entry = MagicMock()
            entry.is_dir.return_value = state["childdir"]
            context.__enter__.return_value = iter([entry])
            return context
        with ExitStack() as stack:
            stack.enter_context(patch.object(runner.os, "getpid", return_value=100))
            stack.enter_context(patch.object(runner.os, "stat", side_effect=proc_stat))
            stack.enter_context(patch("builtins.open", mock_open(read_data=state["boot"])))
            stack.enter_context(patch.object(runner.os, "open", side_effect=open_fd))
            stack.enter_context(patch.object(runner.os, "fstat", side_effect=info))
            stack.enter_context(patch.object(runner.os, "close", side_effect=close_fd))
            stack.enter_context(patch.object(runner.os, "scandir", side_effect=scan))
            stack.enter_context(patch.object(runner, "cgroup2_fd", side_effect=lambda fd:
                state["filesystem" if fd_paths[fd] == "/fake/cgroup" else "parent_filesystem"]))
            stack.enter_context(patch.object(runner, "read_at", side_effect=read))
            yield state, opened, closed

    def test_unique_driver_leaf_and_each_fd_closed_once(self):
        with self.environment() as (state, opened, closed):
            scope = runner.Scope(inputs()[1])
            scope.check({100})
            scope.close(); scope.close()
            self.assertEqual(sorted(closed), sorted(opened))
            self.assertEqual(len(closed), len(set(closed)))

    def test_constructor_rejects_namespace_boot_target_and_membership_mismatches(self):
        changes = (dict(boot=b"wrong\n"), dict(pidns=88), dict(timens=88),
                   dict(children_timens=88), dict(inode=98), dict(device=8),
                   dict(parent_inode=99), dict(parent_filesystem=False),
                   dict(filesystem=False), dict(kind=b"threaded\n"),
                   dict(members=b"100\n101\n"), dict(members=b"101\n"),
                   dict(members=b"100\nnot-a-pid\n"), dict(childdir=True))
        for change in changes:
            with self.subTest(change=change), self.environment(**change) as (_, opened, closed):
                with self.assertRaises(ValueError):
                    runner.Scope(inputs()[1])
                self.assertEqual(sorted(closed), sorted(opened))
                self.assertEqual(len(closed), len(set(closed)))

    def test_constructor_preserves_primary_error_when_target_close_fails(self):
        with self.environment(filesystem=False, close_failure=True) as (_, opened, closed):
            with self.assertRaises(ValueError) as caught:
                runner.Scope(inputs()[1])
            self.assertIn("target cgroup identity mismatch", str(caught.exception))
            self.assertIn("scope close failed: target close fault", str(caught.exception))
            self.assertEqual(sorted(closed), sorted(opened))
            self.assertEqual(len(closed), len(set(closed)))

    def test_recheck_rejects_changed_namespaces_and_unowned_member(self):
        for key, value in (("pidns", 88), ("timens", 88), ("children_timens", 88),
                           ("members", b"100\n501\n")):
            with self.subTest(key=key), self.environment() as (state, opened, closed):
                scope = runner.Scope(inputs()[1])
                state[key] = value
                with self.assertRaises(ValueError): scope.check({100, 500})
                scope.close()
                self.assertEqual(sorted(closed), sorted(opened))


class MainIntegrationTests(unittest.TestCase):
    def setUp(self):
        tmp = Path(__file__).resolve().parents[2] / "private/t132-run-preparation/tmp"
        tmp.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(dir=tmp)
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name)
        self.observer = self.base / "observer"
        self.observer.mkdir()
        self.output_parent = self.base / "runs"
        self.output_parent.mkdir()
        self.registration, self.source, self.session, self.records = bundle()
        self.source.update(boot_id="synthetic-boot", observer_pidns_inode="4026531836",
                           observer_timens_inode="77", duration_seconds=30,
                           cgroup=dict(path="/fake/cgroup", inode=self.registration["cgroup"], device="7"))
        self.registration_path = self.base / "registration.json"
        self.registration_path.write_bytes(self.encode(self.registration))
        self.source_raw = self.encode(self.source)
        (self.observer / "input.json").write_bytes(self.source_raw)
        self.marker = dict(event="attached_observer", generation=self.registration["generation"],
                           boot_id=self.source["boot_id"], duration_seconds=30,
                           observed_monotonic_ns="100000000", input_sha256=self.sha(self.source_raw))
        (self.observer / "attached.json").write_bytes(self.encode(self.marker))
        binary = bytearray(64)
        binary[:7] = b"\x7fELF\x02\x01\x01"
        struct.pack_into("<HH", binary, 16, 2, 62)
        self.binary = bytes(binary)
        self.binary_path = self.base / "original-fixture"
        self.binary_path.write_bytes(self.binary)
        self.clock = Clock()

    @staticmethod
    def encode(value): return json.dumps(value, sort_keys=True).encode()
    @staticmethod
    def sha(raw): return hashlib.sha256(raw).hexdigest()

    def invoke(self, failure=None, observer_stop=False):
        test = self
        class FakeScope:
            def __init__(self, source):
                self.pid, self.pidns, self.timens = 100, source["observer_pidns_inode"], source["observer_timens_inode"]
                self.closed, self.samples = 0, []
            def check(self, allowed): self.samples.append(set(allowed))
            def close(self): self.closed += 1
        class FullChildren(Children):
            def __init__(self): super().__init__(test.clock, failure)
            def spawn(self, label, argv):
                super().spawn(label, argv)
                self.items[label]["pid"] = {"server": 499, "client1": 500, "client2": 501}[label]
                if observer_stop and label == "server":
                    (test.observer / "result.json").write_bytes(test.encode(test.session))
            def pump(self, timeout):
                server = self.items["server"]
                server["stdout"] = test.encode(test.records[2]) + b"\n"
                if len(self.items) != 3: return
                server["stdout"] += b"".join(test.encode(r)+b"\n" for r in test.records[3:])
                for i, label in enumerate(("client1", "client2")):
                    self.items[label]["stdout"] = test.encode(test.records[i])+b"\n"
                for child in self.items.values():
                    child.update(exit_code=0, observed_exit_monotonic_ns=str(test.clock()),
                                 stdout_eof=True, stderr_eof=True, stderr=b"synthetic diagnostic\n")
        scope = FakeScope(self.source)
        children = FullChildren()
        original_drive = runner.drive
        def drive(*args, **kwargs):
            return original_drive(*args, **kwargs, clock=self.clock)
        args = ["--allow-fixture-processes", "--registration", str(self.registration_path),
                "--observer-directory", str(self.observer), "--fixture", str(self.binary_path),
                "--fixture-sha256", self.sha(self.binary), "--output-parent", str(self.output_parent)]
        with ExitStack() as stack:
            stack.enter_context(patch.object(runner.platform, "system", return_value="Linux"))
            stack.enter_context(patch.object(runner.platform, "machine", return_value="x86_64"))
            stack.enter_context(patch.object(runner.time, "monotonic_ns", side_effect=self.clock))
            stack.enter_context(patch.object(runner, "Scope", return_value=scope))
            stack.enter_context(patch.object(runner, "ChildSet", return_value=children))
            stack.enter_context(patch.object(runner, "pinned_input", return_value=(str(self.binary_path), self.binary)))
            stack.enter_context(patch.object(runner, "drive", side_effect=drive))
            output = stack.enter_context(redirect_stdout(io.StringIO()))
            code = runner.main(args)
        summary = json.loads(output.getvalue())
        directory = Path(summary["directory"]) if summary["directory"] else None
        receipt = json.loads((directory / "run.json").read_bytes()) if directory else None
        return code, summary, directory, receipt, scope, children

    def test_complete_main_creates_private_bound_material_and_checker_accepts(self):
        code, summary, directory, receipt, scope, children = self.invoke()
        self.assertEqual(code, 0, summary)
        self.assertEqual(receipt["status"], "completed_unverified")
        self.assertEqual(len(list(directory.iterdir())), 11)
        self.assertEqual(stat.S_IMODE(directory.stat().st_mode), 0o700)
        for path in directory.iterdir():
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700 if path.name == "fixture" else 0o600)
        for metadata in list(receipt["artifacts"].values()) + [child[stream] for child in receipt["children"].values()
                                                                        for stream in ("stdout", "stderr")]:
            raw = (directory / metadata["name"]).read_bytes()
            self.assertEqual(metadata["sha256"], self.sha(raw))
            self.assertEqual(metadata["bytes"], len(raw))
        self.assertEqual((directory / "observer-input.json").read_bytes(), self.source_raw)
        self.assertEqual((self.observer / "input.json").read_bytes(), self.source_raw)
        self.assertEqual((directory / "registration.json").read_bytes(), self.registration_path.read_bytes())
        self.assertEqual((directory / "attached.json").read_bytes(), (self.observer / "attached.json").read_bytes())
        self.assertEqual(self.registration_path.read_bytes(), self.encode(self.registration))
        self.assertEqual((self.observer / "attached.json").read_bytes(), self.encode(self.marker))
        self.assertEqual(self.binary_path.read_bytes(), self.binary)
        self.assertEqual(scope.closed, 1)
        self.assertEqual(children.closed, 1)
        self.assertEqual(set(children.items), {"server", "client1", "client2"})
        self.assertEqual((directory / "client1.stderr.log").read_bytes(), b"synthetic diagnostic\n")
        observer_result = self.base / "observer-result.json"
        observer_result.write_bytes(self.encode(self.session))
        scope_evidence = dict(schema="sourceidentity-lab-scope-v1", evidence_kind="operator_attestation",
            generation=receipt["generation"], boot_id=receipt["boot_id"], observer_exit_code=0,
            observer_result_sha256=self.sha(observer_result.read_bytes()),
            fixture_run_sha256=self.sha((directory / "run.json").read_bytes()),
            claims={claim: dict(status="operator_reported", detail="Synthetic test claim only") for claim in CLAIMS})
        for key in ("registration", "observer_input", "attached"):
            scope_evidence[key+"_sha256"] = receipt["artifacts"][key]["sha256"]
        scope_path = self.base / "scope.json"
        scope_path.write_bytes(self.encode(scope_evidence))
        checked = check_run(str(directory), str(observer_result), str(scope_path))
        self.assertEqual(checked["status"], "consistent_unverified", checked)

    def test_cleanup_failure_preserves_child_outputs_and_primary_receipt(self):
        code, summary, directory, receipt, scope, children = self.invoke(failure="cleanup")
        self.assertEqual(code, 1)
        self.assertEqual(receipt["status"], "failed")
        self.assertTrue(any(error["stage"] == "cleanup" and "cleanup error" in error["message"]
                            for error in receipt["errors"]))
        self.assertEqual((directory / "client1.stdout.jsonl").read_bytes(), self.encode(self.records[0])+b"\n")
        self.assertEqual((directory / "client1.stderr.log").read_bytes(), b"synthetic diagnostic\n")
        self.assertEqual(scope.closed, 1)
        self.assertEqual(children.closed, 1)

    def test_observer_result_appearance_stops_before_clients(self):
        code, summary, directory, receipt, scope, children = self.invoke(observer_stop=True)
        self.assertEqual(code, 1)
        self.assertEqual(set(children.items), {"server"})
        self.assertTrue(any("observer stopped" in error["message"] for error in receipt["errors"]))
        self.assertEqual(scope.closed, 1)
        self.assertEqual(children.closed, 1)

    def test_preexisting_observer_result_prevents_scope_and_children(self):
        (self.observer / "result.json").write_bytes(self.encode(self.session))
        code, summary, directory, receipt, scope, children = self.invoke()
        self.assertEqual(code, 1)
        self.assertIsNone(directory)
        self.assertIsNone(receipt)
        self.assertTrue(any("already stopped" in error["message"] for error in summary["errors"]))
        self.assertEqual(scope.closed, 0)
        self.assertEqual(children.closed, 0)
        self.assertEqual(children.items, {})


if __name__ == "__main__": unittest.main()
