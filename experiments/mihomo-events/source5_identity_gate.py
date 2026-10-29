#!/usr/bin/env python3
"""Read-only integrity gate for an independently accepted, fixed source5 bundle.

This does not execute evidence code, rerun VM checks, or release the product.
Private referenced evidence must remain available. References are hashed, not
recursively parsed as additional reports.
"""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import re
import stat
import sys

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = 'experiments/mihomo-events/source5-identity-acceptance.json'
CORE = '5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967'
COUNTS = {'pidreuse': 2, 'permission': 1, 'missing-proof': 2, 'threevalue': 14}
REPORTS = {name: 'experiments/mihomo-events/source5-identity-' + name + '-validation.json'
           for name in ['positive'] + list(COUNTS)}
SCHEMA = 'hoopa-source5-identity-acceptance-check-v1'


def require(condition, message):
    if not condition:
        raise ValueError(message)


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, 'duplicate JSON key')
            result[key] = value
        return result
    def nonfinite(value):
        raise ValueError('nonfinite JSON number')
    def finite_float(value):
        number = float(value)
        require(math.isfinite(number), 'nonfinite JSON number')
        return number
    return json.loads(raw.decode('utf-8'), object_pairs_hook=pairs,
                      parse_constant=nonfinite, parse_float=finite_float)


def digest_string(value):
    return isinstance(value, str) and re.fullmatch('[0-9a-f]{64}', value) is not None


def snapshot(st):
    return (st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns, st.st_ctime_ns)


class Evidence:
    def __init__(self, root):
        self.root = Path(root).resolve()
        self.cache = {}

    def resolve(self, value):
        require(isinstance(value, str) and bool(value), 'invalid reference path')
        path = Path(value)
        resolved = (path if path.is_absolute() else self.root / path).resolve(strict=True)
        require(os.path.commonpath([str(self.root), str(resolved)]) == str(self.root), 'reference escapes repository')
        return resolved

    def read(self, value, expected_sha=None, expected_bytes=None, content=False, force=False):
        path = self.resolve(value)
        if expected_sha is not None:
            require(digest_string(expected_sha), 'invalid SHA256')
        if expected_bytes is not None:
            require(type(expected_bytes) is int and expected_bytes >= 0, 'invalid reference byte count')
        previous = self.cache.get(path)
        if previous is not None and not content and not force:
            require(snapshot(path.stat()) == previous[0], 'cached evidence changed')
            require(expected_sha is None or expected_sha == previous[1], 'conflicting reference SHA256')
            require(expected_bytes is None or expected_bytes == previous[2], 'conflicting reference byte count')
            return None
        # Open only a regular file; a replaced final symlink cannot redirect the read.
        fd = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        chunks = [] if content else None
        try:
            before = os.fstat(fd)
            require(stat.S_ISREG(before.st_mode), 'reference is not a regular file')
            h = hashlib.sha256()
            size = 0
            while True:
                block = os.read(fd, 1024 * 1024)
                if not block:
                    break
                h.update(block)
                size += len(block)
                if content:
                    chunks.append(block)
            require(snapshot(before) == snapshot(os.fstat(fd)), 'evidence changed during read')
        finally:
            os.close(fd)
        require(self.resolve(value) == path and snapshot(path.stat()) == snapshot(before), 'evidence path changed')
        sha = h.hexdigest()
        require(expected_sha is None or sha == expected_sha, 'reference SHA256 mismatch')
        require(expected_bytes is None or size == expected_bytes, 'reference byte count mismatch')
        previous = self.cache.get(path)
        current = (snapshot(before), sha, size)
        require(previous is None or previous == current, 'evidence changed between references')
        self.cache[path] = current
        return b''.join(chunks) if content else None

    def reference(self, ref, content=False):
        require(isinstance(ref, dict) and {'path', 'bytes', 'sha256'} <= set(ref), 'incomplete metadata reference')
        return self.read(ref['path'], ref['sha256'], ref['bytes'], content)

    def nested(self, value):
        if isinstance(value, dict):
            if {'path', 'bytes', 'sha256'} <= set(value):
                self.reference(value)
            for key, item in value.items():
                if key == 'references':
                    require(isinstance(item, list), 'references must be a list')
                    for ref in item:
                        self.reference(ref)
                self.nested(item)
        elif isinstance(value, list):
            for item in value:
                self.nested(item)

    def finish(self):
        # Rehash all cached evidence, including manifest and reports, so mutation
        # after an early read cannot be accepted merely from a cached digest.
        for path, (before, sha, size) in list(self.cache.items()):
            require(snapshot(path.stat()) == before, 'evidence changed before completion')
            self.read(str(path), sha, size, force=True)


def check_bundle(root, manifest_path, manifest_sha256):
    evidence = Evidence(root)
    require(digest_string(manifest_sha256), 'invalid manifest SHA256')
    require(evidence.resolve(manifest_path) == evidence.resolve(MANIFEST), 'unexpected acceptance manifest path')
    manifest = strict_json(evidence.read(manifest_path, manifest_sha256, content=True))
    require(isinstance(manifest, dict), 'manifest must be an object')
    require(manifest.get('schema') == 'hoopa-source5-identity-acceptance-v1', 'unexpected manifest schema')
    require(manifest.get('core_sha256') == CORE, 'unexpected core')
    require(manifest.get('scope') == 'restricted_source5_identity', 'unexpected scope')
    require(manifest.get('positive') == {'registered': 100, 'passed': 100}, 'positive denominator mismatch')
    expected = {name: {'registered': count, 'passed': count} for name, count in COUNTS.items()}
    require(manifest.get('boundaries') == expected, 'boundary denominator mismatch')
    for count in [manifest['positive']] + list(manifest['boundaries'].values()):
        require(all(type(count[key]) is int for key in ('registered', 'passed')), 'counts must be integers')
    require(manifest.get('task_T127_complete') is True and manifest.get('P0_released') is False and
            manifest.get('P1_released') is False, 'acceptance decision mismatch')
    reports = manifest.get('reports')
    require(isinstance(reports, dict) and set(reports) == set(REPORTS), 'report set mismatch')
    for name, required_path in REPORTS.items():
        ref = reports[name]
        require(isinstance(ref, dict) and ref.get('path') == required_path, 'unexpected report path')
        report = strict_json(evidence.reference(ref, content=True))
        require(isinstance(report, dict), 'report must be an object')
        if name in ('pidreuse', 'missing-proof', 'threevalue'):
            require(report.get('core_sha256') == CORE, 'report core mismatch')
        elif name == 'permission':
            health = report.get('host_check')
            require(report.get('schema') == 'hoopa-source5-permission-formal-boundary-validation-v1' and
                    isinstance(health, dict) and
                    health.get('schema') == 'sourceidentity-source5-permission-host-check-v1' and
                    health.get('phase') == 'formal' and health.get('status') == 'permission_refusal_verified' and
                    health.get('ordinary_application_data_plane') is False and
                    health.get('branch_identified') is False, 'independent permission report mismatch')
        require(report.get('task_T127_complete') is False and report.get('P0_released') is False and
                report.get('P1_released') is False, 'historical report release flags changed')
        if name == 'positive':
            totals = report.get('totals', {})
            require(report.get('status') == 'positive_evidence_verified_root_acceptance_separate' and
                    totals.get('registered_slots') == 100 and totals.get('verified_positive_flows') == 100 and
                    totals.get('vms') == 5 and totals.get('complete_epochs') == 5, 'positive report mismatch')
        else:
            require(report.get('status') == 'accepted_bounded_formal_boundary' and
                    type(report.get('registered')) is int and type(report.get('passed')) is int and
                    report['registered'] == COUNTS[name] and report['passed'] == COUNTS[name], 'boundary report mismatch')
        evidence.nested(report)
    evidence.reference(manifest.get('root_decision'))
    history = manifest.get('historical_failures')
    require(isinstance(history, list) and bool(history), 'historical failures must remain referenced')
    for ref in history:
        evidence.reference(ref)
    evidence.nested(manifest)
    evidence.finish()
    return {'schema': SCHEMA, 'passed': True, 'scope': 'restricted_source5_identity',
            'positive': {'registered': 100, 'passed': 100}, 'boundaries': expected,
            'task_T127_complete': True, 'P0_released': False, 'P1_released': False,
            'verified_files': len(evidence.cache), 'new_runtime_samples': 0,
            'verification': 'read_only_integrity_of_independently_accepted_evidence'}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', required=True)
    parser.add_argument('--manifest-sha256', required=True)
    args = parser.parse_args(argv)
    try:
        result = check_bundle(ROOT, args.manifest, args.manifest_sha256)
    except Exception as exc:
        result = {'schema': SCHEMA, 'passed': False, 'error': str(exc),
                  'task_T127_complete': False, 'P0_released': False, 'P1_released': False,
                  'new_runtime_samples': 0}
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
