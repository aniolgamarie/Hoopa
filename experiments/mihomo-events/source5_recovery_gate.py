#!/usr/bin/env python3
"""Hash and cross-bind an independently accepted T128 evidence bundle.

No evidence code or decoders are executed. No VM, network, BPF, or recovery
writer is started. The original private artifacts must remain available.
"""
import argparse
import json
from pathlib import Path
import sys

from source5_identity_gate import CORE, Evidence, digest_string, require, strict_json

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = 'experiments/mihomo-events/source5-recovery-acceptance.json'
REPORT = 'experiments/mihomo-events/source5-recovery-formal-validation.json'
SCHEMA = 'hoopa-source5-recovery-acceptance-check-v1'
COUNTS = {'offline-drop-closed': 2, 'offline-reject-closed': 2, 'held-opened': 2,
          'combined-reopen': 2, 'source-write-enospc': 2, 'logical-quota': 3,
          'active-term': 1, 'buffer-conflict': 2}
FAMILIES = {name: 'transport' for name in ('offline-drop-closed', 'offline-reject-closed', 'held-opened')}
FAMILIES.update({'combined-reopen': 'restart', 'source-write-enospc': 'enospc',
                 'logical-quota': 'quota', 'active-term': 'term', 'buffer-conflict': 'buffer'})


def count(value, expected, label):
    require(type(value) is int and value == expected, label)


def unreleased(value):
    require(value.get('P0_released') is False and value.get('P1_released') is False,
            'product release flags must remain false')


def read_report(evidence, ref, schema=None):
    value = strict_json(evidence.reference(ref, content=True))
    require(isinstance(value, dict), 'report must be an object')
    if schema:
        require(value.get('schema') == schema, 'report schema mismatch')
    evidence.nested(value)
    return value


def check_bundle(root, manifest_path, manifest_sha256):
    evidence = Evidence(root)
    require(digest_string(manifest_sha256), 'invalid manifest SHA256')
    require(evidence.resolve(manifest_path) == evidence.resolve(MANIFEST), 'unexpected acceptance path')
    accepted = strict_json(evidence.read(manifest_path, manifest_sha256, content=True))
    require(isinstance(accepted, dict) and accepted.get('schema') == 'hoopa-source5-recovery-acceptance-v1',
            'unexpected acceptance schema')
    require(accepted.get('scope') == 'restricted_source5_recovery' and accepted.get('core_sha256') == CORE,
            'unexpected scope/core')
    expected = {name: dict(registered=n, executed=n, passed=n) for name, n in COUNTS.items()}
    require(accepted.get('cases') == expected, 'case set or denominator mismatch')
    for row in accepted['cases'].values():
        require(all(type(v) is int for v in row.values()), 'counts must be integers')
    count(accepted.get('vms'), 8, 'VM denominator mismatch')
    count(accepted.get('slots'), 16, 'application denominator mismatch')
    require(accepted.get('task_T128_complete') is True, 'missing root acceptance')
    unreleased(accepted)

    identity = read_report(evidence, accepted['identity_acceptance'], 'hoopa-source5-identity-acceptance-v1')
    require(identity.get('core_sha256') == CORE and identity.get('task_T127_complete') is True,
            'accepted same-core identity prerequisite missing')
    unreleased(identity)

    matrix_ref = accepted['matrix_registration']
    matrix = read_report(evidence, matrix_ref, 'hoopa-source5-recovery-matrix-registration-v1')
    require(matrix.get('phase') == 'formal' and matrix.get('core_sha256') == CORE and
            matrix.get('status') == 'all_registrations_frozen_before_first_VM', 'matrix was not preregistered')
    require(set(matrix['cases']) == set(COUNTS) and set(matrix['families']) == set(FAMILIES.values()),
            'registered case/family inventory mismatch')
    count(matrix.get('registered_slots'), 16, 'registered application denominator mismatch')
    registrations = {}
    canonical_exports = {}
    all_slots = []
    for family, entry in matrix['families'].items():
        reg = read_report(evidence, entry['registration'])
        require(reg.get('phase') == 'formal' and reg.get('core_sha256') == CORE and
                reg.get('status') == 'all_slots_registered_before_execution', 'non-formal registration')
        for field in ('image', 'initramfs'):
            evidence.reference(reg[field])
        expected_cases = {name for name, value in FAMILIES.items() if value == family}
        require(set(reg['cases']) == expected_cases, 'family case inventory mismatch')
        ids = [row['slot_id'] for row in reg['slots']]
        count(reg['registered_denominator'], sum(COUNTS[name] for name in expected_cases),
              'family denominator mismatch')
        require(len(ids) == reg['registered_denominator'] and
                all(s.startswith('formal-' + family + '-') for s in ids), 'non-formal slot namespace')
        family_cases = []
        for name in sorted(expected_cases):
            declared = matrix['cases'][name]
            require(declared == dict(family=family, slot_ids=reg['cases'][name]), 'root/family slot binding mismatch')
            count(len(declared['slot_ids']), COUNTS[name], 'case slot count mismatch')
            family_cases.extend(declared['slot_ids'])
        require(sorted(ids) == sorted(family_cases), 'family slots missing/duplicated across cases')
        all_slots.extend(ids)
        registrations[family] = reg
    require(len(all_slots) == len(set(all_slots)) == 16, 'global slot identity collision')

    require(accepted['report']['path'] == REPORT, 'unexpected formal report path')
    report = read_report(evidence, accepted['report'], 'hoopa-source5-recovery-formal-validation-v1')
    require(report.get('status') == 'formal_evidence_verified_root_acceptance_separate' and
            report.get('core_sha256') == CORE and report.get('matrix_registration') == matrix_ref and
            report.get('task_T128_complete') is False, 'formal report binding/status mismatch')
    unreleased(report)
    require(set(report['cases']) == set(COUNTS), 'formal run set mismatch')
    runs = set()
    for name, n in COUNTS.items():
        row = report['cases'][name]
        family = FAMILIES[name]
        reg_ref = matrix['families'][family]['registration']
        require(row['family'] == family and row['registration'] == reg_ref and
                row['slot_ids'] == matrix['cases'][name]['slot_ids'], 'run registration binding mismatch')
        for field in ('registered', 'executed', 'passed'):
            count(row.get(field), n, 'case ' + field + ' mismatch')
        run = evidence.resolve(row['run'])
        require(run.is_dir() and run not in runs, 'VM run reused across cases')
        require(run.parent == evidence.root / 'private/t128-source5-formal-vm' / family and
                run.name.startswith('vm-' + name + '-'), 'run outside registered formal family')
        runs.add(run)
        admission = read_report(evidence, row['matrix_admission'], 'hoopa-source5-recovery-execution-admission-v1')
        require(admission.get('matrix_registration') == matrix_ref and admission.get('case') == name and
                admission.get('all_registrations_verified') is True and
                admission.get('case_not_claimed') is True, 'missing before-execution root matrix admission')
        launch = read_report(evidence, row['launch'])
        check = read_report(evidence, row['host_check'])
        require(evidence.resolve(row['launch']['path']) == run / 'launch.json' and
                evidence.resolve(row['host_check']['path']) == run / 'host-check.json', 'foreign run artifacts')
        require(launch.get('phase') == check.get('phase') == 'formal' and
                launch.get('mode') == check.get('mode') == name and
                launch.get('registration') == check.get('registration') == reg_ref, 'run phase/case mismatch')
        require([s['slot_id'] for s in launch['slots']] == row['slot_ids'] ==
                [s['slot_id'] for s in check['slots']], 'executed slot set mismatch')
        require(check.get('status') == 'all_registered_slots_verified' and check.get('errors') == [] and
                check.get('core_sha256') == CORE and check.get('task_T128_complete') is False,
                'original host verdict not a bounded success')
        count(check.get('registered'), n, 'host denominator mismatch')
        count(check.get('passed'), n, 'host pass count mismatch')
        require(all(launch.get(k) is False for k in
                    ('network_devices', 'host_mounts_shared', 'kernel_on_host_changed', 'host_cgroups_changed')),
                'VM isolation mismatch')
        require(check['image'] == registrations[family]['image'], 'host image registration mismatch')
        image_ref = registrations[family]['image']
        archive_ref = registrations[family]['initramfs']
        evidence.read(str(run / 'guest-image.json'), image_ref['sha256'], image_ref['bytes'])
        evidence.read(str(run / 'initramfs.cpio.gz'), archive_ref['sha256'], archive_ref['bytes'])
        exported = read_report(evidence, row['export'])
        require(evidence.resolve(row['export']['path']) == run / 'export.json', 'foreign canonical export')
        names = [item['name'] for item in exported['files']]
        require(len(names) == len(set(names)) and 0 < len(names) <= 100, 'invalid export census')
        require(sum(item['bytes'] for item in exported['files']) <= 16 * 1024 * 1024, 'export exceeds fixed bound')
        actual_refs = row['export_files']
        require(len(actual_refs) == len(names), 'incomplete exported evidence references')
        for item, ref in zip(exported['files'], actual_refs):
            require(not Path(item['name']).is_absolute() and '..' not in Path(item['name']).parts,
                    'export path escapes run')
            require(evidence.resolve(ref['path']) == run / 'evidence' / item['name'] and
                    ref['bytes'] == item['bytes'] and ref['sha256'] == item['sha256'], 'export reference mismatch')
        canonical_exports[name] = {evidence.resolve(ref['path']): (ref['bytes'], ref['sha256']) for ref in actual_refs}
        evidence.read(str(run / 'evidence.tar.gz'), exported['sha256'])

    require(set(accepted['actual_overlays']) == {'combined-reopen', 'source-write-enospc'}, 'actual overlay set mismatch')
    for name, ref in accepted['actual_overlays'].items():
        overlay = read_report(evidence, ref, 'hoopa-source5-recovery-formal-overlay-validation-v1')
        require(overlay.get('status') == 'actual_fixed_source_private_copy_passed' and
                overlay.get('case') == name and overlay.get('runtime_registration') == report['cases'][name]['registration'],
                'overlay must bind this formal run')
        require(overlay.get('actual_entry') == dict(passed=1, failed=0, skipped=0, race_exit=0),
                'actual overlay skipped/failed')
        require(all(type(value) is int for value in overlay['actual_entry'].values()),
                'overlay counts must be integers')
        count(overlay.get('private_copy_reopens'), 3, 'overlay reopen count mismatch')
        require(overlay.get('original_inputs_unchanged') is True and overlay.get('fixed_source_unchanged') is True,
                'overlay mutated original inputs')
        run = evidence.resolve(report['cases'][name]['run'])
        require(isinstance(overlay.get('input_refs'), list) and bool(overlay['input_refs']), 'overlay inputs absent')
        for item in overlay['input_refs']:
            require(run / 'evidence' in evidence.resolve(item['path']).parents, 'overlay input not from this VM')
            require(canonical_exports[name].get(evidence.resolve(item['path'])) == (item['bytes'], item['sha256']),
                    'overlay input is not an unchanged canonical export')

    boundary = read_report(evidence, accepted['fixed_source_checks'], 'hoopa-source5-fault-boundaries-preparation-v1')
    require(boundary.get('core_sha256') == CORE and
            boundary.get('status') == 'fixed_source_synthetic_and_injected_checks_passed' and
            boundary.get('runtime_VM_samples') == 0, 'fixed-source checks misclassified')
    for key, n in (('source_fault_checks', 5), ('buffer_and_initial_write_checks', 15)):
        check = boundary[key]
        count(check.get('including_subtests_passed'), n, 'fixed-source test count mismatch')
        for field in ('failed', 'skipped', 'race_exit'):
            count(check.get(field), 0, 'fixed-source test incomplete')
    require(isinstance(accepted.get('limits'), list) and bool(accepted['limits']), 'bounded scope limits absent')
    decision = read_report(evidence, accepted['root_decision'])
    require(decision.get('decision') == 'accept_restricted_source5_recovery' and
            decision.get('task_T128_complete') is True and decision.get('matrix_registration') == matrix_ref and
            decision.get('report') == accepted['report'], 'root decision missing or bound to another matrix')
    unreleased(decision)
    history = accepted.get('historical_failures')
    require(isinstance(history, list) and bool(history), 'retained failure history absent')
    for ref in history:
        evidence.reference(ref)
    evidence.nested(accepted)
    evidence.finish()
    return dict(schema=SCHEMA, passed=True, cases=expected, vms=8, slots=16,
                task_T128_complete=True, P0_released=False, P1_released=False,
                verified_files=len(evidence.cache), new_runtime_samples=0,
                verification='read_only_integrity_of_independently_accepted_evidence')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', required=True)
    parser.add_argument('--manifest-sha256', required=True)
    args = parser.parse_args(argv)
    try:
        result = check_bundle(ROOT, args.manifest, args.manifest_sha256)
    except Exception as exc:
        result = dict(schema=SCHEMA, passed=False, error=str(exc), task_T128_complete=False,
                      P0_released=False, P1_released=False, new_runtime_samples=0)
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
