"""Pure tests. Fake aggregation is injected only into the internal post-hash stage."""
import ast
import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import types
import unittest
from contextlib import redirect_stdout
from unittest import mock

PATH=Path(__file__).with_name('source5_identity_probe.py')
SPEC=importlib.util.spec_from_file_location('source5_identity_probe_under_test',PATH)
P=importlib.util.module_from_spec(SPEC);SPEC.loader.exec_module(P)


def fake_evidence():
    """Explicitly synthetic 100-slot aggregate, never an actual runtime result."""
    slots=[];runs=[]
    for m,mode in enumerate(P.MODES):
        rows=[]
        for i in range(20):
            index=m*20+i+1;token=hashlib.sha256(str(index).encode()).hexdigest()[:32]
            target='alpha.test' if i%2==0 else 'beta.test';node='node:a' if i%2==0 else 'node:b'
            slot=dict(slot_id='fake-%03d'%index,index=index,mode=mode,protocol='http' if i%2==0 else 'socks5',target=target,token=token,expected_node=node)
            slots.append(slot)
            payload=(token+'\n').encode();payload_meta=dict(bytes=33,sha256=hashlib.sha256(payload).hexdigest())
            rows.append(dict(slot=slot,flow=dict(outcome='forwarded',actual_node=node,final_each_direction=33),
                original=dict(pid=index,tgid=index,connect_tid=index,start_ticks=str(index),boot_id='fake-boot',socket_cookie=str(index)),
                source_connector=dict(tgid_initial=index,caller_tid_initial=index,client_cookie=str(index)),
                terminal=dict(upload_bytes='33',download_bytes='33',unallocated_upload_bytes='0',unallocated_download_bytes='0',segments=[dict(upload_bytes='33',download_bytes='33')]),
                writer=dict(errors=[],writer_pid=index,writer_process=dict(pid=index),application_bytes_sent=33,application_bytes_received=33,protocol_ready=True,early_halfclose=True,peer_eof=True,request=copy.deepcopy(payload_meta),response=copy.deepcopy(payload_meta))))
        runs.append(dict(mode=mode,status='all_registered_positive_slots_evidence_verified',complete_epochs=1,flows=rows))
    registration=dict(phase='formal',registered_denominator=100,slots=slots)
    aggregate=dict(schema='hoopa-source5-formal-runtime-aggregation-v2',phase='formal',status='positive_evidence_verified_root_acceptance_separate',
                   totals=dict(registered_slots=100,verified_positive_flows=100,vms=5,complete_epochs=5),runs=runs,
                   task_T127_complete=False,boundary_matrix_complete=False,P0_released=False,P1_released=False)
    return registration,aggregate


class ProbeTests(unittest.TestCase):
    def test_python38_ast(self):
        ast.parse(PATH.read_text(),feature_version=8)
        compile(PATH.read_bytes(),str(PATH),'exec')

    def test_internal_unit_injection_positive100_and_readonly(self):
        reg,result=fake_evidence();before=copy.deepcopy((reg,result))
        with tempfile.TemporaryDirectory() as temporary:
            path=Path(temporary)/'registration.json';raw=json.dumps(reg).encode();path.write_bytes(raw)
            def aggregate(argument):
                self.assertEqual(argument,path)
                return result
            with mock.patch.object(P,'_load_verified_validator',side_effect=AssertionError('fake test must not execute validator')):
                summary=P._aggregate_verified(path,raw,Path('unit-only'),b'not executed',_unit_aggregate=aggregate)
            self.assertEqual(path.read_bytes(),raw)
            self.assertEqual(list(Path(temporary).iterdir()),[path])
        self.assertEqual((reg,result),before)
        self.assertTrue(summary['passed']);self.assertEqual(summary['positive_slots_passed'],100)
        self.assertEqual(summary['registered'],100);self.assertEqual(summary['source_processes'],100)
        self.assertFalse(summary['T127_complete']);self.assertFalse(summary['formal_g01_complete'])

    def test_phase_denominator_before_fake_aggregate(self):
        for change in ('phase','count','slots'):
            with self.subTest(change=change):
                reg,result=fake_evidence()
                if change=='phase':reg['phase']='preparation'
                elif change=='count':reg['registered_denominator']=99
                else:reg['slots'].pop()
                aggregate=mock.Mock(return_value=result)
                with self.assertRaises(ValueError):
                    P._aggregate_verified(Path('fake'),json.dumps(reg).encode(),Path('fake'),b'',_unit_aggregate=aggregate)
                aggregate.assert_not_called()

    def test_observed_census_and_evidence_rejections(self):
        mutations=[
            ('wrong result phase',lambda r,a:a.update(phase='preparation')),
            ('failed validator status',lambda r,a:a.update(status='failed')),
            ('reduced denominator',lambda r,a:a['totals'].update(registered_slots=99)),
            ('missing positive',lambda r,a:a['totals'].update(verified_positive_flows=99)),
            ('incomplete epoch',lambda r,a:a['totals'].update(complete_epochs=4)),
            ('missing mode',lambda r,a:a['runs'].pop()),
            ('missing flow',lambda r,a:a['runs'][0]['flows'].pop()),
            ('wrong original cookie',lambda r,a:a['runs'][0]['flows'][0]['source_connector'].update(client_cookie='999')),
            ('wrong actual node',lambda r,a:a['runs'][0]['flows'][0]['flow'].update(actual_node='node:b')),
            ('wrong terminal',lambda r,a:a['runs'][0]['flows'][0]['terminal'].update(upload_bytes='32')),
            ('missing writer EOF',lambda r,a:a['runs'][0]['flows'][0]['writer'].update(peer_eof=False)),
            ('wrong writer token',lambda r,a:a['runs'][0]['flows'][0]['writer']['request'].update(sha256='0'*64)),
            ('overclaimed task completion',lambda r,a:a.update(task_T127_complete=True)),
        ]
        for label,mutate in mutations:
            with self.subTest(label=label):
                reg,result=fake_evidence();mutate(reg,result)
                with self.assertRaises((ValueError,KeyError)):P._summarize(reg,result)

    def test_multiple_processes_and_domains_required(self):
        reg,result=fake_evidence()
        for run in result['runs']:
            for row in run['flows']:
                row['original'].update(pid=1,tgid=1,connect_tid=1,start_ticks='1')
                row['source_connector'].update(tgid_initial=1,caller_tid_initial=1)
        with self.assertRaises(ValueError):P._summarize(reg,result)
        reg,result=fake_evidence()
        for slot in reg['slots']:slot['target']='alpha.test'
        with self.assertRaises(ValueError):P._summarize(reg,result)

    def test_hash_guards_before_source_execution(self):
        with tempfile.TemporaryDirectory() as temporary:
            reg=Path(temporary)/'registration.json';val=Path(temporary)/'validator.py'
            reg.write_text('{}\n');val.write_text("raise AssertionError('must never execute')\n")
            digest=hashlib.sha256(reg.read_bytes()).hexdigest()
            for registration_hash,validator_hash in [(digest,'d785f0781cc361744c2b72d28b0bd397a4484550debd2cd249e747e27144367a'),('0'*64,P.VALIDATOR_SHA256),(digest,P.VALIDATOR_SHA256)]:
                with self.subTest(registration_hash=registration_hash,validator_hash=validator_hash),mock.patch.object(P,'VALIDATOR_PATH',val.resolve()),mock.patch.object(P,'_aggregate_verified') as execute:
                    before=(reg.read_bytes(),val.read_bytes())
                    with self.assertRaises(ValueError):P.probe(reg,registration_hash,val,validator_hash)
                    execute.assert_not_called();self.assertEqual((reg.read_bytes(),val.read_bytes()),before)

    def test_copied_validator_path_rejected_before_source_execution(self):
        with tempfile.TemporaryDirectory() as temporary:
            reg=Path(temporary)/'registration.json';val=Path(temporary)/'copied-validator.py'
            reg.write_text('{}\n');val.write_text('raise AssertionError("never execute")\n')
            digest=hashlib.sha256(reg.read_bytes()).hexdigest()
            with mock.patch.object(P,'_aggregate_verified') as execute:
                with self.assertRaisesRegex(ValueError,'current-repository validator path'):
                    P.probe(reg,digest,val,P.VALIDATOR_SHA256)
                execute.assert_not_called()

    def test_complete_failure_json_preserves100(self):
        for args in ([],['--unknown'],['--registration','/nonexistent','--registration-sha256','0'*64,'--validator','/nonexistent','--validator-sha256','0'*64]):
            with self.subTest(args=args),redirect_stdout(io.StringIO()) as output:
                self.assertEqual(P.main(args),1)
            result=json.loads(output.getvalue())
            self.assertEqual(result['schema'],P.SCHEMA);self.assertFalse(result['passed'])
            self.assertEqual(result['registered'],100);self.assertEqual(result['positive_slots_passed'],0)
            self.assertTrue(result['errors']);self.assertFalse(result['T127_complete']);self.assertFalse(result['boundary_matrix_complete'])

    def test_validator_exception_is_complete_failed_summary(self):
        with mock.patch.object(P,'probe',side_effect=ValueError('fake missing source evidence')),redirect_stdout(io.StringIO()) as output:
            code=P.main(['--registration','fake','--registration-sha256','0'*64,'--validator','fake','--validator-sha256',P.VALIDATOR_SHA256])
        self.assertEqual(code,1);summary=json.loads(output.getvalue());self.assertEqual(summary['registered'],100)
        self.assertEqual(summary['errors'][0]['detail'],'fake missing source evidence')

    def test_cli_has_no_unit_injection_or_hash_override_option(self):
        for option in ('--unit-aggregate','--allow-unpinned-validator','--validator-pin'):
            with self.subTest(option=option),redirect_stdout(io.StringIO()) as output:
                self.assertEqual(P.main([option,'fake']),1)
            self.assertFalse(json.loads(output.getvalue())['passed'])

    def test_isolated_module_cache_restored(self):
        name='_hoopa_source5_validator_'+P.VALIDATOR_SHA256
        old=sys.modules.get(name);sentinel=types.ModuleType('sentinel');sys.modules[name]=sentinel
        try:
            # This internal loader unit test bypasses the outer hash gate only
            # to inspect cache restoration. CLI probe() cannot accept this source.
            with P._load_verified_validator(Path('fake-unit-validator.py'),b'def aggregate(path):\n return 1\n') as aggregate:
                self.assertEqual(aggregate(None),1);self.assertIsNot(sys.modules[name],sentinel)
            self.assertIs(sys.modules[name],sentinel)
        finally:
            if old is None:sys.modules.pop(name,None)
            else:sys.modules[name]=old


if __name__=='__main__':unittest.main()
