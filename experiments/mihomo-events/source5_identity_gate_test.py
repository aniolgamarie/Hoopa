"""Pure/file-only fake-repository tests; no VM, candidate, or decoder execution."""
import ast
import contextlib
import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location('tested_source5_gate', Path(__file__).with_name('source5_identity_gate.py'))
G = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(G)


class GateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.evidence = self.root / 'private/evidence.bin'
        self.evidence.parent.mkdir()
        self.evidence.write_bytes(b'fake evidence, not an actual run\x00')
        self.ref = self.meta(self.evidence)
        self.reports = {}
        for name, path in G.REPORTS.items():
            report = {'status': 'accepted_bounded_formal_boundary', 'core_sha256': G.CORE,
                      'task_T127_complete': False, 'P0_released': False, 'P1_released': False,
                      'nested': {'one': [self.ref]}, 'references': [self.ref]}
            if name == 'positive':
                report.pop('core_sha256')  # Existing positive report binds its candidate through metadata.
                report.update(status='positive_evidence_verified_root_acceptance_separate',
                              totals={'registered_slots': 100, 'verified_positive_flows': 100,
                                      'vms': 5, 'complete_epochs': 5})
            else:
                report.update(registered=G.COUNTS[name], passed=G.COUNTS[name])
            if name == 'permission':
                report.pop('core_sha256')
                report['schema'] = 'hoopa-source5-permission-formal-boundary-validation-v1'
                report['host_check'] = {
                    'schema': 'sourceidentity-source5-permission-host-check-v1',
                    'phase': 'formal', 'status': 'permission_refusal_verified',
                    'ordinary_application_data_plane': False, 'branch_identified': False}
            self.reports[name] = report
        self.manifest = {'schema': 'hoopa-source5-identity-acceptance-v1', 'core_sha256': G.CORE,
                         'scope': 'restricted_source5_identity', 'positive': {'registered': 100, 'passed': 100},
                         'boundaries': {name: {'registered': n, 'passed': n} for name, n in G.COUNTS.items()},
                         'task_T127_complete': True, 'P0_released': False, 'P1_released': False,
                         'root_decision': self.ref, 'historical_failures': [self.ref]}
        self.write_reports()

    def meta(self, path):
        raw = path.read_bytes()
        return {'path': str(path.relative_to(self.root)), 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}

    def write_json(self, path, value):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, sort_keys=True) + '\n')

    def write_reports(self):
        self.manifest['reports'] = {}
        for name, path in G.REPORTS.items():
            target = self.root / path
            self.write_json(target, self.reports[name])
            self.manifest['reports'][name] = self.meta(target)
        return self.write_manifest()

    def write_manifest(self):
        self.path = self.root / G.MANIFEST
        self.write_json(self.path, self.manifest)
        self.sha = self.meta(self.path)['sha256']
        return self.sha

    def check(self):
        return G.check_bundle(self.root, str(self.path), self.sha)

    def rejected(self):
        with self.assertRaises((ValueError, OSError, TypeError)):
            self.check()

    def test_complete_read_only_main(self):
        before = {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        with mock.patch.object(G, 'ROOT', self.root), contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(G.main(['--manifest', str(self.path), '--manifest-sha256', self.sha]), 0)
        result = json.loads(output.getvalue())
        self.assertTrue(result['passed'])
        self.assertEqual(result['positive'], {'registered': 100, 'passed': 100})
        self.assertEqual(result['boundaries']['threevalue'], {'registered': 14, 'passed': 14})
        self.assertTrue(result['task_T127_complete'])
        self.assertFalse(result['P0_released'])
        self.assertEqual(result['new_runtime_samples'], 0)
        self.assertEqual(before, {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()})

    def test_manifest_mutations(self):
        original = copy.deepcopy(self.manifest)
        changes = [('core_sha256', '0' * 64), ('scope', 'product'), ('positive', {'registered': 100, 'passed': 99}),
                   ('task_T127_complete', False), ('P0_released', True), ('P1_released', True),
                   ('historical_failures', []), ('root_decision', None), ('schema', 'wrong')]
        for key, value in changes:
            with self.subTest(key=key):
                self.manifest = copy.deepcopy(original)
                self.manifest[key] = value
                self.write_manifest()
                self.rejected()

    def test_boundary_denominators_and_boolean(self):
        for name in G.COUNTS:
            for key in ('registered', 'passed'):
                old = self.manifest['boundaries'][name][key]
                self.manifest['boundaries'][name][key] = old - 1
                self.write_manifest()
                self.rejected()
                self.manifest['boundaries'][name][key] = old
        self.manifest['boundaries']['permission']['passed'] = True
        self.write_manifest()
        self.rejected()

    def test_missing_hash_size_and_conflicting_refs(self):
        for mutation in ('missing', 'hash', 'size', 'partial'):
            with self.subTest(mutation=mutation):
                ref = dict(self.ref)
                if mutation == 'missing':
                    ref['path'] = 'private/absent'
                elif mutation == 'hash':
                    ref['sha256'] = '0' * 64
                elif mutation == 'size':
                    ref['bytes'] += 1
                else:
                    del ref['bytes']
                self.reports['permission']['references'] = [ref]
                self.write_reports()
                self.rejected()

    def test_report_decision_and_counts(self):
        for name in self.reports:
            old = copy.deepcopy(self.reports[name])
            self.reports[name]['status'] = 'failed'
            self.write_reports()
            self.rejected()
            self.reports[name] = old
        self.reports['positive']['totals']['verified_positive_flows'] = 99
        self.write_reports()
        self.rejected()
        self.reports['positive']['totals']['verified_positive_flows'] = 100
        self.reports['permission']['task_T127_complete'] = True
        self.write_reports()
        self.rejected()

    def test_fixed_manifest_and_report_paths(self):
        other = self.root / 'other.json'
        other.write_bytes(self.path.read_bytes())
        with self.assertRaises(ValueError):
            G.check_bundle(self.root, other, self.sha)
        self.manifest['reports']['positive']['path'] = str(self.root / G.REPORTS['positive'])
        self.write_manifest()
        self.rejected()

    def test_outside_and_symlink_escape(self):
        with tempfile.TemporaryDirectory() as outside:
            target = Path(outside) / 'outside'
            target.write_bytes(self.evidence.read_bytes())
            for path in (str(target), '../' + Path(outside).name + '/outside', 'private/link'):
                if path == 'private/link':
                    (self.root / path).symlink_to(target)
                self.reports['permission']['references'] = [dict(self.ref, path=path)]
                self.write_reports()
                self.rejected()

    def test_absolute_inside_and_no_recursive_json_loading(self):
        # Evidence with a .json extension is hashed only, not executed or parsed.
        target = self.root / 'private/not-json.json'
        target.write_bytes(b'not json; must never be executed')
        ref = self.meta(target)
        ref['path'] = str(target)
        self.reports['permission']['references'] = [ref]
        self.write_reports()
        self.assertTrue(self.check()['passed'])

    def test_duplicate_nonfinite_and_invalid_utf8(self):
        for raw in (b'{"schema":1,"schema":2}', b'{"x":NaN}', b'{"x":Infinity}', b'{"x":1e999}', b'"\xff"'):
            self.path.write_bytes(raw)
            self.sha = hashlib.sha256(raw).hexdigest()
            self.rejected()
        self.write_reports()
        path = self.root / G.REPORTS['permission']
        path.write_bytes(b'{"status":1,"status":2}')
        self.manifest['reports']['permission'] = self.meta(path)
        self.write_manifest()
        self.rejected()

    def test_missing_or_extra_report(self):
        del self.manifest['reports']['permission']
        self.write_manifest()
        self.rejected()

    def test_manifest_or_report_changes_during_verification(self):
        for target in (self.path, self.root / G.REPORTS['positive']):
            self.write_reports()
            original = G.Evidence.finish
            def change_then_finish(evidence):
                target.write_bytes(target.read_bytes() + b' ')
                return original(evidence)
            with mock.patch.object(G.Evidence, 'finish', change_then_finish):
                self.rejected()

    def test_failure_json_and_cli_has_no_injection(self):
        with mock.patch.object(G, 'ROOT', self.root), contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(G.main(['--manifest', str(self.path), '--manifest-sha256', '0' * 64]), 1)
        result = json.loads(output.getvalue())
        self.assertEqual(result['schema'], G.SCHEMA)
        self.assertFalse(result['passed'])
        self.assertFalse(result['task_T127_complete'])
        self.assertFalse(result['P1_released'])
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            G.main(['--repo', str(self.root)])

    def test_permission_independent_no_core_report(self):
        self.assertNotIn('core_sha256', self.reports['permission'])
        self.assertTrue(self.check()['passed'])
        original = copy.deepcopy(self.reports['permission'])
        changes = [('schema', 'other'), ('phase', 'preparation'), ('status', 'failed'),
                   ('ordinary_application_data_plane', True), ('branch_identified', True)]
        for key, value in changes:
            with self.subTest(key=key):
                self.reports['permission'] = copy.deepcopy(original)
                self.reports['permission']['host_check'][key] = value
                self.write_reports()
                self.rejected()
        self.reports['permission'] = copy.deepcopy(original)
        self.reports['permission']['schema'] = 'other'
        self.write_reports()
        self.rejected()
        for key in ('ordinary_application_data_plane', 'branch_identified'):
            self.reports['permission'] = copy.deepcopy(original)
            del self.reports['permission']['host_check'][key]
            self.write_reports()
            self.rejected()

    def test_python38(self):
        for path in (Path(__file__), Path(G.__file__)):
            ast.parse(path.read_text(), feature_version=8)
            compile(path.read_text(), str(path), 'exec')


if __name__ == '__main__':
    unittest.main()
