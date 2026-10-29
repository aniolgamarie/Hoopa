#!/usr/bin/env python3
"""Read-only integrity check of an independently accepted T129 source5 bundle.

This hashes original artifacts; it does not execute evidence scripts, decoders,
VMs, a core, a collector, or a recovery writer. Private evidence is required.
"""
import argparse
from pathlib import Path
import sys

from source5_identity_gate import CORE, Evidence, digest_string, require, strict_json
from source5_recovery_gate import count, read_report, unreleased

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = 'experiments/mihomo-events/source5-quality-acceptance.json'
REPORT = 'experiments/mihomo-events/source5-quality-formal-validation.json'
CONTRACT = 'specs/001-hoopa-proxy-manager/contracts/source5-formal-quality-v1.md'
SCHEMA = 'hoopa-source5-quality-acceptance-check-v1'
# Global case identity is distinct from each family's internal VM mode.
CASES = {
    'http-ready': ('http', 'held-opened', 2),
    'socks-ready': ('socks', 'held-opened', 2),
    'direct-slow-dns': ('dns', 'direct-slow-dns', 2),
    'full-replacement': ('replacement', 'full-replacement', 4),
    'upstream-failure': ('failure', 'upstream-failure', 7),
    'nested-retry': ('nested', 'nested-retry', 4),
}


def successful_counts(value, expected):
    for key in ('registered', 'attempted', 'executed', 'passed'):
        count(value.get(key), expected, 'case ' + key + ' denominator mismatch')
    for key in ('failed_or_pending', 'unobserved'):
        count(value.get(key), 0, 'case ' + key + ' is not zero')
    require(value.get('vm_started') is True, 'case VM start unobserved')


def check_bundle(root, manifest_path, manifest_sha256):
    evidence = Evidence(root)
    require(digest_string(manifest_sha256), 'invalid manifest SHA256')
    require(evidence.resolve(manifest_path) == evidence.resolve(MANIFEST), 'unexpected acceptance path')
    accepted = strict_json(evidence.read(manifest_path, manifest_sha256, content=True))
    require(isinstance(accepted, dict) and accepted.get('schema') == 'hoopa-source5-quality-acceptance-v1',
            'unexpected acceptance schema')
    require(accepted.get('scope') == 'restricted_source5_quality' and accepted.get('core_sha256') == CORE,
            'unexpected scope/core')
    expected = {name: dict(registered=n, attempted=n, executed=n, passed=n,
                           failed_or_pending=0, unobserved=0, vm_started=True)
                for name, (_, _, n) in CASES.items()}
    require(accepted.get('cases') == expected, 'accepted case set mismatch')
    for name, (_, _, n) in CASES.items():
        successful_counts(accepted['cases'][name], n)
    count(accepted.get('vms'), 6, 'VM denominator mismatch')
    count(accepted.get('slots'), 21, 'application denominator mismatch')
    require(accepted.get('task_T129_complete') is True, 'missing root acceptance')
    unreleased(accepted)
    for key, schema, task in (
            ('identity_acceptance', 'hoopa-source5-identity-acceptance-v1', 'task_T127_complete'),
            ('recovery_acceptance', 'hoopa-source5-recovery-acceptance-v1', 'task_T128_complete')):
        prior = read_report(evidence, accepted[key], schema)
        require(prior.get('core_sha256') == CORE and prior.get(task) is True, 'same-core prerequisite absent')
        unreleased(prior)

    matrix_ref = accepted['matrix_registration']
    matrix = read_report(evidence, matrix_ref, 'hoopa-source5-quality-matrix-registration-v1')
    require(matrix.get('phase') == 'formal' and matrix.get('core_sha256') == CORE and
            matrix.get('status') == 'all_registrations_frozen_before_first_VM', 'matrix not preregistered')
    require(set(matrix['cases']) == set(CASES) and
            set(matrix['families']) == {v[0] for v in CASES.values()}, 'registered inventory mismatch')
    require(evidence.resolve(matrix['contract']['path']) == evidence.resolve(CONTRACT), 'wrong quality contract')
    evidence.reference(matrix['contract'])
    count(matrix.get('registered_slots'), 21, 'registered denominator mismatch')
    registrations, ids = {}, []
    for name, (family, mode, n) in CASES.items():
        reg = read_report(evidence, matrix['families'][family]['registration'])
        require(reg.get('phase') == 'formal' and reg.get('core_sha256') == CORE and
                reg.get('status') == 'all_slots_registered_before_execution', 'non-formal registration')
        count(reg.get('registered_denominator'), n, 'family denominator mismatch')
        family_ids = [s['slot_id'] for s in reg['slots']]
        require(len(family_ids) == n and all(s.startswith('formal-' + family + '-') for s in family_ids),
                'foreign formal slot namespace')
        require(reg['cases'] == {mode: family_ids}, 'family mode or slot inventory mismatch')
        require(matrix['cases'][name] == dict(family=family, mode=mode, slot_ids=family_ids),
                'global case/family/mode binding mismatch')
        for field in ('image', 'initramfs'):
            evidence.reference(reg[field])
        registrations[family] = reg
        ids.extend(family_ids)
    require(len(ids) == len(set(ids)) == 21, 'global slot collision')

    require(accepted['report']['path'] == REPORT, 'unexpected formal report path')
    report = read_report(evidence, accepted['report'], 'hoopa-source5-quality-formal-validation-v1')
    require(report.get('status') == 'formal_evidence_verified_root_acceptance_separate' and
            report.get('core_sha256') == CORE and report.get('matrix_registration') == matrix_ref and
            report.get('task_T129_complete') is False, 'formal report binding/status mismatch')
    unreleased(report)
    require(set(report['cases']) == set(CASES), 'formal run inventory mismatch')
    runs = set()
    for name, (family, mode, n) in CASES.items():
        row = report['cases'][name]
        reg_ref = matrix['families'][family]['registration']
        require(row['family'] == family and row['mode'] == mode and row['registration'] == reg_ref and
                row['slot_ids'] == matrix['cases'][name]['slot_ids'], 'run registration binding mismatch')
        successful_counts(row, n)
        family_summary = read_report(evidence, row['family_summary'])
        require(evidence.resolve(row['family_summary']['path']) ==
                evidence.resolve(reg_ref['path']).parent / 'summary.json', 'foreign family summary')
        require(family_summary.get('phase') == 'formal' and family_summary.get('family') == family and
                family_summary.get('registration') == reg_ref and
                family_summary.get('task_T129_complete') is False, 'family summary binding/status mismatch')
        for field in ('registered', 'attempted', 'executed', 'passed'):
            count(family_summary.get(field), n, 'family ' + field + ' count mismatch')
        count(family_summary.get('failed_or_pending'), 0, 'family contains failed/pending slots')
        count(family_summary.get('vm_started'), 1, 'family VM count mismatch')
        require(len(family_summary['cases']) == 1, 'unexpected family case count')
        observed = family_summary['cases'][0]
        require(observed.get('mode') == mode and observed.get('vm_started') is True and
                observed.get('counting_errors') == [], 'family execution observation failed')
        for field in ('registered', 'attempted', 'executed', 'passed'):
            count(observed.get(field), n, 'observed ' + field + ' count mismatch')
        observation = observed['execution_observation']
        require(observation.get('counting_scope') == 'original_connect_call_start_only' and
                observation.get('observation_status') == 'all_registered_connect_starts_observed' and
                observation.get('executed_slot_ids') == row['slot_ids'] and
                observation.get('unobserved_slot_ids') == [] and observation.get('vm_started') is True,
                'original client connect starts unobserved or mismatched')
        count(observation.get('executed'), n, 'observed connection-start denominator mismatch')
        run = evidence.resolve(row['run'])
        require(run.is_dir() and run not in runs and
                run.parent == evidence.root / 'private/t129-source5-formal-vm' / family and
                run.name.startswith('vm-' + mode + '-'), 'run reused or outside formal family')
        runs.add(run)
        require(evidence.resolve(observed['run']) == run and observed['check'] == row['host_check'],
                'family observation belongs to another run')
        admission = read_report(evidence, row['matrix_admission'], 'hoopa-source5-quality-execution-admission-v1')
        require(admission.get('matrix_registration') == matrix_ref and admission.get('case') == name and
                admission.get('all_registrations_verified') is True and admission.get('case_not_claimed') is True,
                'missing before-execution matrix admission')
        launch = read_report(evidence, row['launch'])
        check = read_report(evidence, row['host_check'])
        require(evidence.resolve(row['launch']['path']) == run / 'launch.json' and
                evidence.resolve(row['host_check']['path']) == run / 'host-check.json', 'foreign run artifacts')
        require(launch.get('phase') == check.get('phase') == 'formal' and
                launch.get('mode') == check.get('mode') == mode and
                launch.get('registration') == check.get('registration') == reg_ref, 'run phase/mode mismatch')
        require([s['slot_id'] for s in launch['slots']] == row['slot_ids'] ==
                [s['slot_id'] for s in check['slots']], 'executed slot set mismatch')
        require(check.get('status') == 'all_registered_slots_verified' and check.get('errors') == [] and
                check.get('core_sha256') == CORE and check.get('task_T129_complete') is False,
                'original host verdict is not bounded success')
        count(check.get('registered'), n, 'host registered denominator mismatch')
        count(check.get('passed'), n, 'host pass denominator mismatch')
        require(all(launch.get(k) is False for k in
                    ('network_devices', 'host_mounts_shared', 'kernel_on_host_changed', 'host_cgroups_changed')),
                'VM isolation mismatch')
        require(check['image'] == registrations[family]['image'], 'host image binding mismatch')
        for artifact, field in (('guest-image.json', 'image'), ('initramfs.cpio.gz', 'initramfs')):
            ref = registrations[family][field]
            evidence.read(str(run / artifact), ref['sha256'], ref['bytes'])
        exported = read_report(evidence, row['export'])
        require(evidence.resolve(row['export']['path']) == run / 'export.json', 'foreign export census')
        names = [r['name'] for r in exported['files']]
        require(0 < len(names) <= 100 and len(names) == len(set(names)), 'invalid export census')
        require(all(type(r['bytes']) is int and r['bytes'] >= 0 for r in exported['files']) and
                sum(r['bytes'] for r in exported['files']) <= 16 * 1024 * 1024, 'invalid export byte budget')
        require(len(row['export_files']) == len(names), 'incomplete canonical exports')
        for item, ref in zip(exported['files'], row['export_files']):
            require(not Path(item['name']).is_absolute() and '..' not in Path(item['name']).parts,
                    'export escapes run')
            require(evidence.resolve(ref['path']) == run / 'evidence' / item['name'] and
                    ref['bytes'] == item['bytes'] and ref['sha256'] == item['sha256'], 'export reference mismatch')
        evidence.read(str(run / 'evidence.tar.gz'), exported['sha256'])

    checks = accepted['semantic_checks']
    require(set(checks) == {'source5_injected', 'http_adapter', 'fixed_boundaries'}, 'semantic inventory mismatch')
    for name, schema, top, total in (
            ('source5_injected', 'hoopa-source5-quality-injected-validation-v1', 2, 11),
            ('http_adapter', 'hoopa-source5-http-adapter-boundary-validation-v1', 1, 5)):
        value = read_report(evidence, checks[name], schema)
        count(value.get('top_level_passed'), top, 'semantic top-level count mismatch')
        count(value.get('passed_including_subtests'), total, 'semantic count mismatch')
        for field in ('failed', 'skipped', 'actual_core_samples', 'formal_t129_samples'):
            count(value.get(field), 0, 'semantic checks cannot become actual network samples')
        require(value.get('race') is True and bool(value.get('limits')), 'semantic scope/race missing')
    boundary = read_report(evidence, checks['fixed_boundaries'], 'hoopa-source5-fault-boundaries-preparation-v1')
    require(boundary.get('core_sha256') == CORE and boundary.get('runtime_VM_samples') == 0,
            'fixed boundaries core/scope mismatch')
    part = boundary['buffer_and_initial_write_checks']
    count(part.get('including_subtests_passed'), 15, 'fixed boundary denominator mismatch')
    for field in ('failed', 'skipped', 'race_exit'):
        count(part.get(field), 0, 'fixed boundary check incomplete')
    require(isinstance(accepted.get('limits'), list) and bool(accepted['limits']), 'bounded limits absent')
    decision = read_report(evidence, accepted['root_decision'])
    require(decision.get('decision') == 'accept_restricted_source5_quality' and
            decision.get('task_T129_complete') is True and decision.get('matrix_registration') == matrix_ref and
            decision.get('report') == accepted['report'], 'root decision missing or for another matrix')
    unreleased(decision)
    require(isinstance(accepted.get('historical_failures'), list) and bool(accepted['historical_failures']),
            'retained failure history absent')
    for ref in accepted['historical_failures']:
        evidence.reference(ref)
    evidence.nested(accepted)
    evidence.finish()
    return dict(schema=SCHEMA, passed=True, cases=expected, vms=6, slots=21, task_T129_complete=True,
                P0_released=False, P1_released=False, verified_files=len(evidence.cache), new_runtime_samples=0,
                verification='read_only_integrity_of_independently_accepted_evidence')


def main(argv=None):
    import json
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', required=True)
    parser.add_argument('--manifest-sha256', required=True)
    args = parser.parse_args(argv)
    try:
        result = check_bundle(ROOT, args.manifest, args.manifest_sha256)
    except Exception as exc:
        result = dict(schema=SCHEMA, passed=False, error=str(exc), task_T129_complete=False,
                      P0_released=False, P1_released=False, new_runtime_samples=0)
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
