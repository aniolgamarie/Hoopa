"""Synthetic files test the read-only gate, never stand in for VM evidence."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import source5_quality_gate as G


class Bundle:
    def __init__(self, root, mutate=None):
        self.root = Path(root)
        self.mutate = mutate or (lambda tag, value: None)
        self.rows = {}
        self.build()

    def raw(self, path, raw):
        p = self.root / path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(raw)
        return dict(path=path, bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest())

    def save(self, tag, path, value):
        self.mutate(tag, value)
        self.rows[tag] = value
        return self.raw(path, (json.dumps(value, sort_keys=True) + '\n').encode())

    def build(self):
        bounded = dict(P0_released=False, P1_released=False)
        prior = {}
        for name, task in (('identity', 'task_T127_complete'), ('recovery', 'task_T128_complete')):
            prior[name + '_acceptance'] = self.save(name, 'prior-' + name + '.json',
                dict(schema='hoopa-source5-' + name + '-acceptance-v1', core_sha256=G.CORE,
                     **{task: True}, **bounded))
        families, declared, contexts = {}, {}, {}
        counts = {}
        for name, (family, mode, n) in G.CASES.items():
            base = 'private/t129-source5-formal-vm/' + family
            run = base + '/vm-' + mode + '-synthetic-unit'
            slots = [dict(slot_id='formal-%s-%03d' % (family, i + 1)) for i in range(n)]
            ids = [x['slot_id'] for x in slots]
            image = self.raw(base + '/image/guest-image.json', b'{"unit_test_only":true}\n')
            archive = self.raw(base + '/image/guest-initramfs.cpio.gz', b'synthetic image, no executable\n')
            reg = self.save(family + '.registration', base + '/registration/registration.json',
                dict(phase='formal', core_sha256=G.CORE, status='all_slots_registered_before_execution',
                     registered_denominator=n, slots=copy.deepcopy(slots), cases={mode: list(ids)},
                     image=image, initramfs=archive))
            families[family] = dict(registration=reg)
            declared[name] = dict(family=family, mode=mode, slot_ids=list(ids))
            counts[name] = dict(registered=n, attempted=n, executed=n, passed=n,
                                failed_or_pending=0, unobserved=0, vm_started=True)
            self.raw(run + '/guest-image.json', (self.root / image['path']).read_bytes())
            self.raw(run + '/initramfs.cpio.gz', (self.root / archive['path']).read_bytes())
            launch = self.save(family + '.launch', run + '/launch.json',
                dict(phase='formal', mode=mode, registration=reg, slots=copy.deepcopy(slots),
                     network_devices=False, host_mounts_shared=False, kernel_on_host_changed=False,
                     host_cgroups_changed=False))
            host = self.save(family + '.host', run + '/host-check.json',
                dict(phase='formal', mode=mode, registration=reg, slots=copy.deepcopy(slots),
                     status='all_registered_slots_verified', errors=[], core_sha256=G.CORE,
                     task_T129_complete=False, registered=n, passed=n, image=image))
            raw = self.raw(run + '/evidence/synthetic.txt', b'synthetic unit-test evidence\n')
            tar = self.raw(run + '/evidence.tar.gz', b'synthetic archive\n')
            exported = self.save(family + '.export', run + '/export.json',
                dict(sha256=tar['sha256'], files=[dict(name='synthetic.txt', bytes=raw['bytes'],
                                                     sha256=raw['sha256'])]))
            observation = dict(executed=n, executed_slot_ids=list(ids), unobserved_slot_ids=[],
                               vm_started=True, counting_scope='original_connect_call_start_only',
                               observation_status='all_registered_connect_starts_observed')
            summary = self.save(family + '.summary', base + '/registration/summary.json',
                dict(phase='formal', family=family, registration=reg, task_T129_complete=False,
                     registered=n, attempted=n, executed=n, passed=n, failed_or_pending=0, vm_started=1,
                     cases=[dict(mode=mode, registered=n, attempted=n, executed=n, passed=n,
                                 vm_started=True, counting_errors=[], execution_observation=observation,
                                 run=run, check=host)]))
            contexts[name] = dict(family=family, mode=mode, registration=reg, slot_ids=list(ids),
                run=run, launch=launch, host_check=host, export=exported, export_files=[raw],
                family_summary=summary, **counts[name])
        contract = self.raw(G.CONTRACT, b'Synthetic fixture contract; no actual registrations.\n')
        matrix = self.save('matrix', 'matrix.json',
            dict(schema='hoopa-source5-quality-matrix-registration-v1', phase='formal',
                 core_sha256=G.CORE, status='all_registrations_frozen_before_first_VM',
                 registered_slots=21, families=families, cases=declared, contract=contract))
        for name, row in contexts.items():
            row['matrix_admission'] = self.save(name + '.admission', row['run'] + '/admission.json',
                dict(schema='hoopa-source5-quality-execution-admission-v1', matrix_registration=matrix,
                     case=name, all_registrations_verified=True, case_not_claimed=True))
        report = self.save('report', G.REPORT,
            dict(schema='hoopa-source5-quality-formal-validation-v1',
                 status='formal_evidence_verified_root_acceptance_separate', core_sha256=G.CORE,
                 matrix_registration=matrix, cases=contexts, task_T129_complete=False, **bounded))
        semantic = {}
        for name, schema, top, total in (
                ('source5_injected', 'hoopa-source5-quality-injected-validation-v1', 2, 11),
                ('http_adapter', 'hoopa-source5-http-adapter-boundary-validation-v1', 1, 5)):
            semantic[name] = self.save(name, name + '.json',
                dict(schema=schema, top_level_passed=top, passed_including_subtests=total,
                     failed=0, skipped=0, actual_core_samples=0, formal_t129_samples=0,
                     race=True, limits=['synthetic unit fixture']))
        semantic['fixed_boundaries'] = self.save('boundaries', 'boundaries.json',
            dict(schema='hoopa-source5-fault-boundaries-preparation-v1', core_sha256=G.CORE,
                 runtime_VM_samples=0, buffer_and_initial_write_checks=dict(
                     including_subtests_passed=15, failed=0, skipped=0, race_exit=0)))
        decision = self.save('decision', 'decision.json',
            dict(decision='accept_restricted_source5_quality', task_T129_complete=True,
                 matrix_registration=matrix, report=report, **bounded))
        history = self.raw('historical-failure.txt', b'Synthetic retained failure\n')
        self.manifest = self.save('acceptance', G.MANIFEST,
            dict(schema='hoopa-source5-quality-acceptance-v1', scope='restricted_source5_quality',
                 core_sha256=G.CORE, cases=counts, vms=6, slots=21, task_T129_complete=True,
                 matrix_registration=matrix, report=report, semantic_checks=semantic,
                 root_decision=decision, historical_failures=[history], limits=['unit fixture only'],
                 **prior, **bounded))

    def check(self):
        return G.check_bundle(self.root, self.manifest['path'], self.manifest['sha256'])


class QualityGateTests(unittest.TestCase):
    def test_complete_synthetic_file_graph(self):
        with tempfile.TemporaryDirectory() as temp:
            result = Bundle(temp).check()
            self.assertTrue(result['passed'])
            self.assertEqual((result['slots'], result['vms'], result['new_runtime_samples']), (21, 6, 0))

    def test_consistently_hashed_false_claims_rejected(self):
        # All mutated JSON receives its actual hash. Failure must be semantic,
        # not merely a stale checksum after editing a referenced document.
        mutations = [
            ('matrix', lambda d: d['cases']['http-ready'].update(family='socks'), 'binding mismatch'),
            ('http.launch', lambda d: d.update(phase='preparation'), 'phase/mode mismatch'),
            ('http.summary', lambda d: d.update(executed=0), 'family executed count'),
            ('http.summary', lambda d: d.update(vm_started=0), 'family VM count'),
            ('http.summary', lambda d: d['cases'][0].update(counting_errors=['missing']), 'observation failed'),
            ('http.summary', lambda d: d['cases'][0]['execution_observation'].update(
                unobserved_slot_ids=['formal-http-001']), 'connect starts unobserved'),
            ('http.summary', lambda d: d['cases'][0]['execution_observation'].update(
                executed_slot_ids=['formal-http-001', 'formal-http-001']), 'connect starts unobserved'),
            ('http.host', lambda d: d.update(passed=1), 'host pass denominator'),
            ('report', lambda d: d['cases']['http-ready'].update(vm_started=False), 'VM start unobserved'),
            ('source5_injected', lambda d: d.update(actual_core_samples=11), 'actual network samples'),
            ('http_adapter', lambda d: d.update(skipped=1), 'actual network samples'),
            ('acceptance', lambda d: d['cases']['http-ready'].update(failed_or_pending=False), 'not zero'),
            ('acceptance', lambda d: d.update(P0_released=True), 'release flags'),
            ('decision', lambda d: d.update(task_T129_complete=False), 'root decision missing'),
        ]
        for tag, change, message in mutations:
            with self.subTest(tag=tag, message=message), tempfile.TemporaryDirectory() as temp:
                def mutate(actual_tag, value):
                    if actual_tag == tag:
                        change(value)
                bundle = Bundle(temp, mutate)
                with self.assertRaisesRegex(ValueError, message):
                    bundle.check()

    def test_missing_or_changed_original_evidence_rejected(self):
        for mutation in ('delete', 'change'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temp:
                bundle = Bundle(temp)
                p = Path(temp) / 'private/t129-source5-formal-vm/http/vm-held-opened-synthetic-unit/evidence/synthetic.txt'
                if mutation == 'delete':
                    p.unlink()
                else:
                    p.write_bytes(b'changed after acceptance\n')
                with self.assertRaises((ValueError, FileNotFoundError)):
                    bundle.check()


if __name__ == '__main__':
    unittest.main(verbosity=2)
