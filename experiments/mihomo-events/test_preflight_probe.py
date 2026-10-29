"""Security and scope checks for the optional T130 diagnostic mode."""
import argparse
import hashlib
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
import io
from unittest.mock import Mock

spec = importlib.util.spec_from_file_location("native_probe", Path(__file__).with_name("native_probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class PreflightProbeTests(unittest.TestCase):
    def test_hash_pin_and_diagnostic_denominator(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "observer"
            binary.write_bytes(b"synthetic observer bytes")
            args = argparse.Namespace(preflight_observer=str(binary),
                                      preflight_observer_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                                      count=2, require_identity=False)
            self.assertEqual(probe.checked_preflight_observer(args), binary)
            args.count = 100
            with self.assertRaisesRegex(RuntimeError, "two_diagnostic_samples"):
                probe.checked_preflight_observer(args)
            args.count = 2
            args.require_identity = True
            with self.assertRaises(RuntimeError):
                probe.checked_preflight_observer(args)
            args.require_identity = False
            args.preflight_observer_sha256 = "0" * 64
            with self.assertRaisesRegex(RuntimeError, "hash_mismatch"):
                probe.checked_preflight_observer(args)

    def test_symlink_and_missing_pin_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "target"
            target.write_bytes(b"observer")
            link = Path(directory) / "link"
            link.symlink_to(target)
            args = argparse.Namespace(preflight_observer=str(link), preflight_observer_sha256="0" * 64,
                                      count=2, require_identity=False)
            with self.assertRaisesRegex(RuntimeError, "path_invalid"):
                probe.checked_preflight_observer(args)
            args.preflight_observer_sha256 = None
            with self.assertRaises(RuntimeError):
                probe.checked_preflight_observer(args)

    def test_topology_requires_matching_parent_uid_and_namespaces(self):
        observer = {"observer_parent_pid": 7,
                    "report": {"observer": {"uid": 12, "netns": "net:1", "pidns": "pid:1"}}}
        participant = {"parent_pid": 7, "uid": 12, "netns": "net:1", "pidns": "pid:1"}
        self.assertTrue(probe.topology_matches(observer, [participant.copy() for _ in range(3)], 7))
        for key, value in (("uid", 13), ("netns", "net:2"), ("pidns", "pid:2"), ("parent_pid", 8)):
            changed = dict(participant, **{key: value})
            self.assertFalse(probe.topology_matches(observer, [changed], 7))
        self.assertFalse(probe.topology_matches(observer, [], 7))

    def test_real_process_context_is_bookended(self):
        context = probe.process_context(os.getpid())
        self.assertEqual(context["pid"], os.getpid())
        self.assertEqual(context["uid"], os.geteuid())
        self.assertEqual(context["parent_pid"], os.getppid())
        self.assertTrue(context["start_ticks"].isdigit())

    def test_checked_private_executable_survives_original_replacement(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = root / "original"
            original.write_bytes(b"verified executable bytes")
            digest = hashlib.sha256(original.read_bytes()).hexdigest()
            snapshot = probe.snapshot_executable(original, digest, root / "pinned")
            original.write_bytes(b"replaced bytes")
            self.assertEqual(snapshot.read_bytes(), b"verified executable bytes")
            self.assertEqual(snapshot.stat().st_mode & 0o777, 0o500)
            with self.assertRaisesRegex(RuntimeError, "hash_mismatch"):
                probe.snapshot_executable(original, digest, root / "bad")
            self.assertFalse((root / "bad").exists())

    def test_malformed_observer_report_is_rejected_before_barrier_release(self):
        for report in ([], None, {"report": []}, {"report": {"observer": []}},
                       {"report": {"observer": {"uid": True}}}):
            with self.assertRaisesRegex(RuntimeError, "report_invalid"):
                probe.validate_observer(report)
            self.assertFalse(probe.topology_matches(report, [{"parent_pid": 7}], 7))

    def test_any_barrier_error_closes_waiting_input(self):
        child = Mock(stdin=io.StringIO(), stdout=io.StringIO('{"name":"hoopa-probe"}\n'))
        errors = []
        def broken_action():
            raise AttributeError("private-value")
        probe.release_after_preregistration(child, broken_action, errors.append)
        self.assertTrue(child.stdin.closed)
        self.assertEqual(errors, ["AttributeError"])


if __name__ == "__main__":
    unittest.main()
