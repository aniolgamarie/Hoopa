"""Synthetic offline files only: the fixture bytes are never executed."""
import contextlib
import copy
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from check_fixture_run import ARTIFACTS, CLAIMS, EVENTS, check_run, digest, main
from test_calibration import bundle


def encoded(value):
    return json.dumps(value, sort_keys=True).encode()


class FixtureRunTests(unittest.TestCase):
    def setUp(self):
        tmp = Path(__file__).resolve().parents[2] / "private/t132-run-preparation/tmp"
        tmp.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=tmp)
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.directory = self.base / "run"
        self.directory.mkdir()
        self.observer = self.base / "observer.json"
        self.scope_path = self.base / "scope.json"
        registration, source, session, fixtures = bundle()
        source.update(boot_id="synthetic-boot", observer_pidns_inode="4026531836",
                      observer_timens_inode="77", duration_seconds=30,
                      cgroup=dict(inode=registration["cgroup"]))
        marker = dict(event="attached_observer", generation=registration["generation"],
                      duration_seconds=30, boot_id=source["boot_id"], observed_monotonic_ns="100",
                      input_sha256=digest(encoded(source)))
        self.docs = dict(registration=registration, observer_input=source, attached=marker)
        self.fixture_records = {"server": [fixtures[2], fixtures[3], fixtures[4]],
                                "client1": [fixtures[0]], "client2": [fixtures[1]]}
        self.run = dict(schema="sourceidentity-fixture-run-v1", status="completed_unverified",
                        errors=[], generation=registration["generation"], boot_id=source["boot_id"],
                        controller_pid=100, pidns_inode=source["observer_pidns_inode"],
                        timens_inode="77", started_monotonic_ns="110", finished_monotonic_ns="300",
                        deadline_monotonic_ns="30000000100", identity_capability="unverified",
                        association="unverified", completeness="unverified", scope_isolation="unverified",
                        artifacts={}, children={}, events=[dict(event=event, observed_monotonic_ns=str(120+i*20))
                                                          for i, event in enumerate(EVENTS)])
        for i, label in enumerate(("server", "client1", "client2")):
            argv = [str(self.directory / "fixture"), "--role", "serve" if i == 0 else "client",
                    "--generation", registration["generation"], "--port", str(registration["port"])]
            argv += ["--count", "2"] if i == 0 else ["--token", registration["tokens"][i-1]]
            argv += ["--allow-loopback"]
            self.run["children"][label] = dict(label=label, argv=argv, pid=499+i,
                started_monotonic_ns=str((130, 170, 190)[i]), observed_exit_monotonic_ns="210",
                exit_code=0, stdout_eof=True, stderr_eof=True, truncated=False)
        self.scope = dict(schema="sourceidentity-lab-scope-v1", evidence_kind="operator_attestation",
                          generation=registration["generation"], boot_id=source["boot_id"],
                          observer_exit_code=0, claims={name: dict(status="operator_reported", detail="Synthetic claim")
                                                       for name in CLAIMS})
        self.observer.write_bytes(encoded(session))
        self.write_materials()

    def metadata(self, name, raw):
        (self.directory / name).write_bytes(raw)
        return dict(name=name, sha256=digest(raw), bytes=len(raw))

    def write_materials(self):
        for key, name in ARTIFACTS.items():
            raw = b"not executable fixture bytes" if key == "fixture" else encoded(self.docs[key])
            self.run["artifacts"][key] = self.metadata(name, raw)
        for label, records in self.fixture_records.items():
            for stream, suffix, raw in (("stdout", ".stdout.jsonl", b"\n".join(encoded(r) for r in records)+b"\n"),
                                        ("stderr", ".stderr.log", b"")):
                self.run["children"][label][stream] = self.metadata(label + suffix, raw)
        self.write_run()

    def write_run(self):
        raw = encoded(self.run)
        (self.directory / "run.json").write_bytes(raw)
        for key in ("registration", "observer_input", "attached"):
            field = {"registration": "registration_sha256", "observer_input": "observer_input_sha256",
                     "attached": "attached_sha256"}[key]
            self.scope[field] = self.run["artifacts"][key]["sha256"]
        self.scope["fixture_run_sha256"] = digest(raw)
        self.scope["observer_result_sha256"] = digest(self.observer.read_bytes())
        self.scope_path.write_bytes(encoded(self.scope))

    def check(self, **kwargs):
        return check_run(str(self.directory), str(self.observer), str(self.scope_path), **kwargs)

    def relocate(self):
        original = str(self.directory)
        exported = self.base / "exported-run"
        self.directory.rename(exported)
        self.directory = exported
        return original

    def assertRejected(self):
        result = self.check()
        self.assertEqual(result["status"], "rejected", result)
        self.assertTrue(result["errors"])

    def test_consistent_offline_bundle_and_cli_preserve_unverified_fields(self):
        result = self.check()
        self.assertEqual(result["status"], "consistent_unverified", result)
        self.assertEqual(len(result["pairs"]), 2)
        self.assertEqual(result["external_claims"], "operator_reported_unverified")
        self.assertNotIn("original_run_directory", result)
        self.assertNotIn("original_run_directory_claim", result)
        for field in ("identity_capability", "runtime", "association", "completeness",
                      "scope_isolation", "startup_identity", "preregistration"):
            self.assertEqual(result[field], "unverified")
        self.assertEqual(result["inputs"][1]["sha256"], digest(self.observer.read_bytes()))
        with contextlib.redirect_stdout(io.StringIO()) as output:
            code = main(["--run-directory", str(self.directory), "--observer-result", str(self.observer),
                         "--scope-evidence", str(self.scope_path)])
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(output.getvalue())["status"], "consistent_unverified")

    def test_relocated_original_materials_require_explicit_directory(self):
        original = self.relocate()
        before = {p.name: p.read_bytes() for p in self.directory.iterdir()}
        scope_before = self.scope_path.read_bytes()
        result = self.check()
        self.assertEqual(result["status"], "rejected", result)
        self.assertIn("child argv mismatch", result["errors"])
        # The declared path no longer exists on this host. It is compared as
        # receipt text only, while every file is opened from the exported path.
        real_open, real_abspath = os.open, os.path.abspath
        with patch("check_fixture_run.os.open", wraps=real_open) as opened, \
             patch("check_fixture_run.os.path.abspath", wraps=real_abspath) as absolute:
            result = self.check(original_run_directory=original)
        self.assertEqual(result["status"], "consistent_unverified", result)
        self.assertEqual(result["original_run_directory"], original)
        self.assertEqual(result["original_run_directory_claim"], "operator_declared_unverified")
        self.assertNotIn(original, [call.args[0] for call in opened.call_args_list])
        self.assertNotIn(original, [call.args[0] for call in absolute.call_args_list])
        self.assertEqual(opened.call_args_list[0].args[0], str(self.directory))
        for call in opened.call_args_list[1:]:
            if call.kwargs.get("dir_fd") is not None:
                self.assertNotIn("/", call.args[0])
                self.assertTrue(call.args[1] & os.O_NOFOLLOW)
        for field in ("identity_capability", "runtime", "association", "completeness",
                      "scope_isolation", "startup_identity", "preregistration"):
            self.assertEqual(result[field], "unverified")
        with contextlib.redirect_stdout(io.StringIO()) as output:
            code = main(["--run-directory", str(self.directory), "--observer-result", str(self.observer),
                         "--scope-evidence", str(self.scope_path), "--original-run-directory", original])
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(output.getvalue())["original_run_directory"], original)
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.directory.iterdir()})
        self.assertEqual(scope_before, self.scope_path.read_bytes())

    def test_original_directory_must_match_all_three_child_argv(self):
        original = str(self.directory)
        run = copy.deepcopy(self.run)
        for label in ("server", "client1", "client2"):
            with self.subTest(label=label):
                self.run = copy.deepcopy(run)
                self.run["children"][label]["argv"][0] = "/other/guest/run/fixture"
                self.write_run()
                result = self.check(original_run_directory=original)
                self.assertEqual(result["status"], "rejected", result)
                self.assertIn("child argv mismatch", result["errors"])

    def test_original_directory_rejects_noncanonical_posix_paths(self):
        invalid = ("", "/", "relative", "./run", "../run", "//guest/run", "/guest//run",
                   "/guest/run/", "/guest/./run", "/guest/../run", "/.", "/..",
                   "/guest/run\x00", b"/guest/run", Path("/guest/run"), True, 1, {})
        for value in invalid:
            with self.subTest(value=value), patch("check_fixture_run.os.open") as opened:
                result = self.check(original_run_directory=value)
                self.assertEqual(result["status"], "rejected", result)
                self.assertIn("canonical absolute POSIX original run directory required", result["errors"])
                opened.assert_not_called()
        with contextlib.redirect_stdout(io.StringIO()) as output:
            code = main(["--run-directory", str(self.directory), "--observer-result", str(self.observer),
                         "--scope-evidence", str(self.scope_path), "--original-run-directory", "/guest/../run"])
        self.assertEqual(code, 1)
        self.assertEqual(json.loads(output.getvalue())["status"], "rejected")

    def test_original_directory_does_not_rebind_old_receipt_hash(self):
        original = self.relocate()
        self.assertEqual(self.check(original_run_directory=original)["status"], "consistent_unverified")
        self.run["transfer_note"] = "receipt changed after export"
        (self.directory / "run.json").write_bytes(encoded(self.run))
        result = self.check(original_run_directory=original)
        self.assertEqual(result["status"], "rejected", result)
        self.assertIn("scope hash mismatch: fixture_run_sha256", result["errors"])

    def test_original_directory_preserves_host_file_safety(self):
        original = self.relocate()
        fixture = self.directory / "fixture"
        target = self.base / "real-fixture"
        fixture.rename(target)
        fixture.symlink_to(target)
        result = self.check(original_run_directory=original)
        self.assertEqual(result["status"], "rejected", result)
        fixture.unlink()
        target.rename(fixture)
        self.run["artifacts"]["fixture"]["name"] = "../fixture"
        self.write_run()
        result = self.check(original_run_directory=original)
        self.assertEqual(result["status"], "rejected", result)
        self.assertIn("unsafe or unexpected artifact name", result["errors"])

    def test_digest_size_missing_and_extra_files(self):
        path = self.directory / "fixture"
        original = path.read_bytes()
        for action in (lambda: path.write_bytes(b"changed"), lambda: path.unlink(),
                       lambda: (self.directory / "extra").write_bytes(b"extra")):
            with self.subTest(action=action):
                action()
                self.assertRejected()
                path.write_bytes(original)
                (self.directory / "extra").unlink(missing_ok=True)
        self.run["artifacts"]["fixture"]["bytes"] += 1
        self.write_run()
        self.assertRejected()

    def test_run_and_child_failures(self):
        original = copy.deepcopy(self.run)
        changes = [lambda: self.run.update(status="failed"), lambda: self.run.update(errors=["error"]),
                   lambda: self.run["children"]["client1"].update(exit_code=2),
                   lambda: self.run["children"]["server"].update(exit_code=False),
                   lambda: self.run["children"]["server"].update(truncated=True),
                   lambda: self.run["children"]["server"].update(stdout_eof=False),
                   lambda: self.run["children"]["server"].update(stderr_eof=False),
                   lambda: self.run["children"]["client2"].update(pid=100)]
        for change in changes:
            with self.subTest(change=change):
                self.run = copy.deepcopy(original)
                change(); self.write_run(); self.assertRejected()

    def test_marker_boot_hash_namespace_and_deadline_mismatch(self):
        original = copy.deepcopy(self.docs)
        changes = [lambda: self.docs["attached"].update(boot_id="wrong"),
                   lambda: self.docs["attached"].update(input_sha256="0"*64),
                   lambda: self.docs["observer_input"].update(observer_timens_inode="88"),
                   lambda: self.docs["attached"].update(observed_monotonic_ns="111"),
                   lambda: self.docs["attached"].update(duration_seconds=61)]
        for change in changes:
            with self.subTest(change=change):
                self.docs = copy.deepcopy(original)
                change(); self.write_materials(); self.assertRejected()

    def test_start_exit_event_order_and_argv(self):
        original = copy.deepcopy(self.run)
        changes = [lambda: self.run.update(started_monotonic_ns="99"),
                   lambda: self.run["children"]["client1"].update(started_monotonic_ns="150"),
                   lambda: self.run["children"]["server"].update(observed_exit_monotonic_ns="30000000100"),
                   lambda: self.run["children"]["server"].update(observed_exit_monotonic_ns="221"),
                   lambda: self.run["children"]["client2"].update(started_monotonic_ns="179"),
                   lambda: self.run["events"][2].update(observed_monotonic_ns="119"),
                   lambda: self.run["children"]["client1"]["argv"].append("--extra")]
        for change in changes:
            with self.subTest(change=change):
                self.run = copy.deepcopy(original)
                change(); self.write_run(); self.assertRejected()

    def test_insufficient_reserve_at_server_spawn(self):
        # Initial marker/run check still has enough time, but preparation takes
        # 17 seconds. Preserve every other event/child ordering constraint,
        # including the distinct 12-second post-spawn minimum.
        shift = 17000000000
        self.run["finished_monotonic_ns"] = str(int(self.run["finished_monotonic_ns"]) + shift)
        for event in self.run["events"]:
            event["observed_monotonic_ns"] = str(int(event["observed_monotonic_ns"]) + shift)
        for child in self.run["children"].values():
            for field in ("started_monotonic_ns", "observed_exit_monotonic_ns"):
                child[field] = str(int(child[field]) + shift)
        self.write_run()
        result = self.check()
        self.assertEqual(result["status"], "rejected")
        self.assertIn("insufficient reserve at server spawn", " ".join(result["errors"]))

    def test_replaced_observer_result_breaks_scope_binding(self):
        observer = json.loads(self.observer.read_bytes())
        observer["synthetic_note"] = "Another result with identical connection records"
        self.observer.write_bytes(encoded(observer))
        result = self.check()
        self.assertEqual(result["status"], "rejected")
        self.assertIn("scope hash mismatch: observer_result_sha256", " ".join(result["errors"]))

    def test_post_spawn_reserve_cannot_use_earlier_start_timestamp(self):
        original = copy.deepcopy(self.run)
        for first in (1, 3, 4):
            with self.subTest(first=first):
                self.run = copy.deepcopy(original)
                shift = 19000000000
                for event in self.run["events"][first:]:
                    event["observed_monotonic_ns"] = str(int(event["observed_monotonic_ns"]) + shift)
                for child in self.run["children"].values():
                    child["observed_exit_monotonic_ns"] = str(210 + shift)
                if first == 1:
                    for label in ("client1", "client2"):
                        child = self.run["children"][label]
                        child["started_monotonic_ns"] = str(int(child["started_monotonic_ns"]) + shift)
                if first == 3:
                    self.run["children"]["client2"]["started_monotonic_ns"] = str(190 + shift)
                self.run["finished_monotonic_ns"] = str(300 + shift)
                self.write_run()
                result = self.check()
                self.assertEqual(result["status"], "rejected")
                self.assertIn("reserve after spawn", " ".join(result["errors"]))

    def test_source_duration_and_cgroup_inode_must_match(self):
        original = copy.deepcopy(self.docs)
        for fields in (dict(duration_seconds=2), dict(duration_seconds=True),
                       dict(cgroup=dict(inode="999"))):
            with self.subTest(fields=fields):
                self.docs = copy.deepcopy(original)
                self.docs["observer_input"].update(fields)
                self.docs["attached"]["input_sha256"] = digest(encoded(self.docs["observer_input"]))
                self.write_materials()
                self.assertRejected()

    def test_owned_client_pid_token_and_stream_shape(self):
        original = copy.deepcopy(self.fixture_records)
        changes = [lambda: self.fixture_records["client1"][0].update(pid=999),
                   lambda: self.fixture_records["client1"][0].update(token="f"*32),
                   lambda: self.fixture_records["client1"].append(copy.deepcopy(self.fixture_records["client1"][0])),
                   lambda: self.fixture_records["server"].reverse()]
        for change in changes:
            with self.subTest(change=change):
                self.fixture_records = copy.deepcopy(original)
                change(); self.write_materials(); self.assertRejected()

    def test_scope_missing_claim_hash_nonzero_exit_and_missing_file(self):
        original = copy.deepcopy(self.scope)
        changes = [lambda: self.scope["claims"].pop("attachment_continuity"),
                   lambda: self.scope.update(fixture_run_sha256="0"*64),
                   lambda: self.scope.update(observer_exit_code=1),
                   lambda: self.scope.update(observer_exit_code=False),
                   lambda: self.scope["claims"]["placement_before_socket"].update(detail=" ")]
        for change in changes:
            with self.subTest(change=change):
                self.scope = copy.deepcopy(original)
                change(); self.scope_path.write_bytes(encoded(self.scope)); self.assertRejected()
        self.scope_path.unlink()
        self.assertRejected()

    def test_duplicate_and_nonfinite_json(self):
        observer = self.observer.read_bytes()
        for raw in (b'{"schema":1,"schema":2}', b'{"n":NaN}', b'{"n":1e309}'):
            self.observer.write_bytes(raw)
            self.assertRejected()
        self.observer.write_bytes(observer)
        raw = b'{"schema":1,"schema":2}'
        self.run["children"]["client1"]["stdout"] = self.metadata("client1.stdout.jsonl", raw)
        self.write_run()
        self.assertRejected()

    def test_bounds_and_each_scope_digest(self):
        original = copy.deepcopy(self.run)
        for metadata, limit in ((self.run["artifacts"]["fixture"], 64*1024*1024),
                                (self.run["artifacts"]["registration"], 4*1024*1024),
                                (self.run["children"]["client1"]["stdout"], 65536),
                                (self.run["children"]["client1"]["stderr"], 65536)):
            size = metadata["bytes"]
            metadata["bytes"] = limit + 1
            self.write_run(); self.assertRejected()
            metadata["bytes"] = size
        self.run = original
        self.write_run()
        for field in ("registration_sha256", "observer_input_sha256", "attached_sha256",
                      "fixture_run_sha256", "observer_result_sha256"):
            original_hash = self.scope[field]
            self.scope[field] = "0"*64
            self.scope_path.write_bytes(encoded(self.scope)); self.assertRejected()
            self.scope[field] = original_hash
        self.scope_path.write_bytes(b" "*(4*1024*1024+1))
        self.assertRejected()

    def test_traversal_symlink_and_nonregular_inputs(self):
        original = copy.deepcopy(self.run)
        self.run["artifacts"]["fixture"]["name"] = "../fixture"
        self.write_run(); self.assertRejected()
        self.run = original
        self.write_run()
        fixture = self.directory / "fixture"
        raw = fixture.read_bytes()
        target = self.base / "binary"
        target.write_bytes(raw)
        fixture.unlink(); fixture.symlink_to(target)
        self.assertRejected()
        fixture.unlink(); fixture.mkdir()
        self.assertRejected()

    def test_observer_result_failure_and_external_symlink(self):
        session = json.loads(self.observer.read_bytes())
        session["status"] = "failed"
        self.observer.write_bytes(encoded(session))
        self.assertRejected()
        target = self.base / "real-observer"
        self.observer.rename(target)
        self.observer.symlink_to(target)
        self.assertRejected()


if __name__ == "__main__":
    unittest.main()
