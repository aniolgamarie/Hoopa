"""Synthetic final manifests, distinct from the original accepted-family audit."""
import contextlib
import copy
import hashlib
import io
import json
import tempfile
import unittest
import os
import py_compile
import subprocess
import sys
from pathlib import Path
from unittest import mock

import source5_routing_gate as G

FLAGS=dict(task_T016_complete=False,P0_released=False,P1_released=False)

class Fixture:
    def __init__(self,root):
        self.root=Path(root);self.specs={};self.families={}
        self.raw(G.IDENTITY_PATH,(G.ROOT/G.IDENTITY_PATH).read_bytes())
        for i in range(15):
            name='synthetic-family-%02d'%i
            row=self.row(name,2)
            public=self.save('public/'+name+'.json',dict(schema='synthetic-public',status='synthetic-accepted',references=[],**FLAGS))
            accepted=self.save('root/'+name+'.json',dict(schema='synthetic-root',status='synthetic-accepted',references=[],**FLAGS))
            self.specs[name]=dict(public_record=dict(public,schema='synthetic-public',status='synthetic-accepted'),root_acceptance=dict(accepted,schema='synthetic-root',status='synthetic-accepted'),rows=[row],expected_counts=dict(VMs=1,application_slots=2,source_epochs=1,mandatory_frames=13,checkpoints=0))
            self.families[name]=dict(public_record=public,root_acceptance=accepted,rows=[row])
        row=self.row('admission-baseline',5,admission=True)
        counts=dict(VMs=1,application_slots=5,flows=2,pre_admission_refusals=3,source_epochs=1,mandatory_frames=13,checkpoints=0,frames=13)
        record=dict(status=G.ADMISSION_STATUS,counts=counts,run=row['run'],source_epoch='epoch:admission-baseline',canonical=row['canonical'],independent_reviews=row['independent_reviews'],flows=[dict(flow_id='flow:admission-baseline:'+str(i)) for i in range(2)],clients=[dict(index=i,source_identity=None,source_terminal=None,source_bytes=None) for i in range(1,6)],references=[row['launch'],row['registration'],row['result'],row['canonical']]+row['independent_reviews'],**FLAGS)
        public=self.save(G.ADMISSION_PUBLIC,dict(record,schema='hoopa-t016-admission-validation-v1'))
        accepted=self.save(G.ADMISSION_ROOT,dict(record,schema='hoopa-t016-admission-runtime-root-acceptance-v1'))
        self.families[G.ADMISSION]=dict(public_record=public,root_acceptance=accepted,rows=[row])
        adapter=self.save(G.ADAPTERS,dict(schema='hoopa-t016-routing-original-adapters-v1',families=self.specs));self.adapter_sha=adapter['sha256']
        self.totals=dict(VMs=16,application_slots=35,admitted_flows=32,source_epochs=16,mandatory_frames=208,checkpoints=0,frames=208)
        decision=dict(schema='hoopa-source5-routing-root-decision-v1',status='accept_restricted_source5_multi_entry_routing',families=self.families,counts=self.totals,task_T016_complete=True,P0_released=False,P1_released=False,core_sha256=G.CORE,helper_sha256=G.HELPER)
        self.manifest=dict(schema='hoopa-source5-routing-acceptance-v1',scope='restricted_source5_multi_entry_routing',core_sha256=G.CORE,helper_sha256=G.HELPER,helper_variants={'F11_persistence_matrix_v2':G.F11_HELPER},families=self.families,counts=self.totals,task_T016_complete=True,P0_released=False,P1_released=False,new_runtime_samples=0,root_decision=self.save('root/decision.json',decision))
    def save(self,path,value):
        raw=(json.dumps(value,sort_keys=True)+'\n').encode();return self.raw(path,raw)
    def raw(self,path,raw):
        p=self.root/path;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(raw)
        return dict(path=path,bytes=len(raw),sha256=hashlib.sha256(raw).hexdigest())
    def row(self,name,n,admission=False):
        run='private/synthetic/'+name;epoch='epoch:'+name;generation='gen:'+name
        reg=self.save(run+'/registration.json',dict(schema='synthetic-registration',registered_VMs=1,application_slots=n,attempted=0,executed=0,slots=list(range(n)),**FLAGS))
        claim=self.save(run+'/claim.json',dict(registration=reg,run=run,application_slots=n,slots=list(range(n))))
        launch=self.save(run+'/launch.json',dict(registration=reg,claim=claim,application_slots=n,slots=list(range(n)),network_devices=False,host_mounts_shared=False,host_BPF_changed=False,host_cgroups_changed=False))
        result=self.save(run+'/result.json',dict(status='vm_exited_unverified',errors=[],application_slots=n,exit_code=0,pid=90,vm_started=True,wait_reaped=True,process_absent=True,**FLAGS))
        events=[]
        for i in range(13):
            event=dict(schema_version=5,source_epoch=epoch,config_generation=generation,source_seq=str(i+1),kind='opened' if i<2 else 'epoch_closed' if i==12 else 'setup_finished',flow_id='flow:'+name+':'+str(i) if i<2 else None)
            body=json.dumps(event).encode();events.append(len(body).to_bytes(4,'big')+body+hashlib.sha256(body).digest())
        frames=self.raw(run+'/evidence/trace/source.frames',b''.join(events))
        manifest=self.save(run+'/evidence/trace/source.source.json',dict(source_epoch=epoch,config_generation=generation,binary_sha256=G.CORE,schema_version=2,source_schema_version=5))
        schema='hoopa-t016-admission-canonical-v1' if admission else 'synthetic-canonical'
        status='finite_admission_refusal_observed_pending_independent_review' if admission else 'synthetic-observed'
        canonical=self.save(run+'/canonical.json',dict(schema=schema,status=status,flows=[dict(flow_id='flow:'+name+':'+str(i)) for i in range(2)],frames=13,source_epoch=epoch,**FLAGS))
        review=self.save(run+'/independent-review.json',dict(synthetic=True,schema='hoopa-t016-admission-independent-runtime-review-v1',status='complete_originals_reviewed_no_blocking_findings',run=run,source_epoch=epoch,canonical=canonical,blocking_findings=[],actual_VM_samples=0) if admission else dict(synthetic=True,status='accepted'))
        return dict(name=name,run=run,canonical=canonical,canonical_schema=schema,canonical_status=status,registration=reg,launch=launch,result=result,independent_reviews=[review],sources=[dict(frames=frames,manifest=manifest)],application_slots=n)
    def check(self):
        manifest=self.save(G.MANIFEST,self.manifest)
        with mock.patch.object(G,'ADAPTERS_SHA',self.adapter_sha),mock.patch.object(G,'TOTALS',{k:self.totals[k] for k in G.TOTALS}):
            return G.check_bundle(self.root,G.MANIFEST,manifest['sha256'])

class Tests(unittest.TestCase):
    def test_complete_synthetic_bundle(self):
        with tempfile.TemporaryDirectory() as d:
            f=Fixture(d);result=f.check()
            self.assertTrue(result['passed']);self.assertEqual(result['counts'],f.totals);self.assertEqual(len(result['families']),16)
    def test_missing_family_row_duplicate_run_and_totals_fail_closed(self):
        for case in ('family','admission','row','duplicate-run','count','promotion','helper','path','hash'):
            with self.subTest(case=case),tempfile.TemporaryDirectory() as d:
                f=Fixture(d);name=next(iter(f.specs));family=f.manifest['families'][name]
                if case=='family':del f.manifest['families'][name]
                if case=='admission':del f.manifest['families'][G.ADMISSION]
                if case=='row':family['rows']=[]
                if case=='duplicate-run':family['rows'][0]['run']=f.manifest['families']['synthetic-family-01']['rows'][0]['run']
                if case=='count':f.manifest['counts']['application_slots']+=1
                if case=='promotion':f.manifest['P0_released']=True
                if case=='helper':f.manifest['helper_variants']={}
                if case=='path':family['public_record']['path']=family['root_acceptance']['path']
                if case=='hash':family['public_record']['sha256']='0'*64
                with self.assertRaises((ValueError,KeyError)):f.check()
    def test_original_mutation_cannot_be_reauthorized_by_manifest_hash(self):
        for case in ('status','count','source','review','registration'):
            with self.subTest(case=case),tempfile.TemporaryDirectory() as d:
                f=Fixture(d);family=f.manifest['families']['synthetic-family-00'];row=family['rows'][0]
                ref=(family['root_acceptance'] if case=='status' else row['result'] if case=='count' else row['sources'][0]['frames'] if case=='source' else row['independent_reviews'][0] if case=='review' else row['registration'])
                path=f.root/ref['path'];path.write_bytes(path.read_bytes()+b' ')
                with self.assertRaises(ValueError):f.check()
    def test_admission_root_status_and_missing_source_outcomes_fail(self):
        for case in ('status','source-bytes','missing-client','wrong-canonical','fake-CP'):
            with self.subTest(case=case),tempfile.TemporaryDirectory() as d:
                f=Fixture(d);family=f.manifest['families'][G.ADMISSION]
                if case=='wrong-canonical':family['rows'][0]['canonical_schema']='unknown'
                else:
                    for key in ('public_record','root_acceptance'):
                        ref=family[key];value=json.loads((f.root/ref['path']).read_text())
                        if case=='status':value['status']='accepted_some_other_case'
                        if case=='source-bytes':value['clients'][2]['source_bytes']=0
                        if case=='missing-client':value['clients'].pop()
                        if case=='fake-CP':value['counts']['checkpoints']=1
                        family[key]=f.save(ref['path'],value)
                with self.assertRaises(ValueError):f.check()
    def test_duplicate_epoch_and_frame_corruption(self):
        with tempfile.TemporaryDirectory() as d:
            f=Fixture(d);row=f.manifest['families']['synthetic-family-00']['rows'][0]
            row['sources'].append(copy.deepcopy(row['sources'][0]))
            with self.assertRaisesRegex(ValueError,'duplicate source epoch'):G.source_census(G.Evidence(d),row['sources'])
            pair=row['sources'][0];raw=bytearray((f.root/pair['frames']['path']).read_bytes());raw[8]^=1
            pair['frames']=f.raw(pair['frames']['path'],bytes(raw))
            with self.assertRaisesRegex(ValueError,'frame digest'):G.source_census(G.Evidence(d),[pair])
    def test_f11_and_ordered_omit_no_accepted_rows(self):
        adapters=json.loads((G.ROOT/G.ADAPTERS).read_text())['families']
        for name,key in (('F11_persistence_matrix_v2','case_id'),('ordered_routing_nine_rows','row')):
            spec=adapters[name]
            for side in ('public','root'):
                with self.subTest(family=name,side=side):
                    public=json.loads((G.ROOT/spec['public_record']['path']).read_text())
                    root=json.loads((G.ROOT/spec['root_acceptance']['path']).read_text())
                    (public if side=='public' else root)['rows'].pop()
                    value={k:spec[k] for k in ('public_record','root_acceptance','rows')}
                    with mock.patch.object(G,'report',side_effect=[public,root]),self.assertRaisesRegex(ValueError,'row census'):
                        G.old_family(G.Evidence(G.ROOT),name,value,spec)

    def test_cli_failure_returns_complete_json(self):
        with tempfile.TemporaryDirectory() as d,mock.patch.object(G,'ROOT',Path(d)):
            output=io.StringIO()
            with contextlib.redirect_stdout(output):code=G.main(['--manifest',G.MANIFEST,'--manifest-sha256','0'*64])
            result=json.loads(output.getvalue());self.assertEqual(code,1);self.assertFalse(result['passed']);self.assertFalse(result['task_T016_complete'])

    def test_review_pending_wrong_run_or_unaccepted_canonical_cannot_promote_admission(self):
        for case in ('pending','run','epoch','blocking','unbound-review','canonical','dependency'):
            with self.subTest(case=case),tempfile.TemporaryDirectory() as d:
                f=Fixture(d);family=f.families[G.ADMISSION];row=family['rows'][0]
                if case=='dependency':
                    path=f.root/G.IDENTITY_PATH;path.write_bytes(path.read_bytes()+b'\n')
                elif case=='canonical':
                    row['canonical']=f.save(row['run']+'/other-canonical.json',json.loads((f.root/row['canonical']['path']).read_text()))
                else:
                    reference=row['independent_reviews'][0];v=json.loads((f.root/reference['path']).read_text())
                    if case=='pending':v['status']='pending'
                    if case=='run':v['run']=f.families['synthetic-family-00']['rows'][0]['run']
                    if case=='epoch':v['source_epoch']='other'
                    if case=='blocking':v['blocking_findings']=['not resolved']
                    if case=='unbound-review':v['synthetic_extra']=True
                    changed=f.save(row['run']+'/changed-review.json',v);row['independent_reviews']=[changed]
                    if case!='unbound-review':
                        for key in ('public_record','root_acceptance'):
                            old=family[key];record=json.loads((f.root/old['path']).read_text());record['independent_reviews']=[changed]
                            family[key]=f.save(old['path'],record)
                with self.assertRaises(ValueError):f.check()

    def test_timestamp_valid_bytecode_cannot_replace_verified_helper_source(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);helper=root/'source5_identity_gate.py';gate=root/'source5_routing_gate.py'
            raw=(G.ROOT/G.IDENTITY_PATH).read_bytes();poison=b"raise RuntimeError('synthetic poisoned helper cache')\n"
            helper.write_bytes(poison+b' '*(len(raw)-len(poison)));mtime=1700000000
            os.utime(helper,(mtime,mtime));py_compile.compile(str(helper),doraise=True)
            helper.write_bytes(raw);os.utime(helper,(mtime,mtime))
            gate.write_bytes(Path(G.__file__).read_bytes())
            code="import runpy; v=runpy.run_path('source5_routing_gate.py'); assert v['Evidence'].__name__=='Evidence'; print('verified source executed')"
            result=subprocess.run([sys.executable,'-B','-c',code],cwd=root,capture_output=True,timeout=10)
            self.assertEqual(result.returncode,0,result.stderr);self.assertEqual(result.stdout,b'verified source executed\n')

if __name__=='__main__':unittest.main()
