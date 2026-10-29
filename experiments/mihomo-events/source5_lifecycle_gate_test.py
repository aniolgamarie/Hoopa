"""Complete synthetic integrity graphs; never new runtime or real source evidence."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import source5_lifecycle_gate as G

TOKEN=b'0123456789abcdef'*4


class Bundle:
    def __init__(self,root,mutate=None,alias_token=False):
        self.root=Path(root);self.mutate=mutate or (lambda tag,v:None);self.alias_token=alias_token;self.rows={};self.build()
    def raw(self,path,raw):
        p=self.root/path;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(raw)
        return dict(path=path,bytes=len(raw),sha256=hashlib.sha256(raw).hexdigest())
    def save(self,tag,path,value):
        self.mutate(tag,value);self.rows[tag]=value
        return self.raw(path,(json.dumps(value,sort_keys=True)+'\n').encode())
    def build(self):
        flags=dict(task_T015_complete=False,P0_released=False,P1_released=False)
        self.cases={};groups={g:[] for g in G.PUBLIC};hygiene=[]
        quality=self.save('quality','experiments/mihomo-events/source5-quality-acceptance.json',dict(schema='hoopa-source5-quality-acceptance-v1',core_sha256=G.CORE,scope='restricted_source5_quality',vms=6,slots=21,task_T129_complete=True,P0_released=False,P1_released=False))
        for idx,(name,spec) in enumerate(G.CASES.items()):
            group,pkg,mode,n,fwd,cp,fs,status=spec;base='private/'+pkg;run=base+'/vm-'+mode+'-synthetic-only';rbase=base+'/calibration-v1'
            slots=[dict(slot_id='synthetic-'+name+'-%03d'%i,index=i,expected_bytes=(7 if n==1 or group=='g07' else 41 if i==1 else 7)) for i in range(1,n+1)]
            if name=='management-data-auth':
                for s,variant in zip(slots,['http-valid','http-missing','http-wrong','socks-valid','socks-no-method2','socks-wrong-password']):
                    s.update(expected_forwarded=variant in ('http-valid','socks-valid'),expected_bytes=7 if variant in ('http-valid','socks-valid') else 0,protocol='http' if variant.startswith('http') else 'socks',auth_variant=variant)
            if name=='spool-permissions':slots[0].update(expected_forwarded=True,protocol='http',auth_variant='http-valid')
            # Shared source input records are ordinary labelled bytes, NOT an
            # encoded epoch or decoder invocation; every JSON report is synthetic.
            source_raw=self.raw(run+'/evidence/trace/unit.frames',b'synthetic source placeholder, not actual encoded runtime\n'+name.encode())
            epoch='synthetic-epoch-'+name;generation='synthetic-generation-'+name;frames=[];histories=[];clientrows=[]
            for i,s in enumerate(slots):
                pid=100+i;start=str(200+i);cookie=str(300+i);identity=dict(pid=pid,start_ticks=start);forward=s.get('expected_forwarded',True)
                stat='Uid:\t65534 65534 65534 65534\nGid:\t65534 65534 65534 65534\nGroups:\nNoNewPrivs:\t1\n'+''.join(k+':\t0000000000000000\n' for k in ('CapInh','CapPrm','CapEff','CapBnd','CapAmb'))
                rows=[dict(event='before_connect',identity=identity)]
                if group=='g07' or group=='logout':rows.append(dict(event='connect_call',connect_boot_before_ns='123'))
                conn=dict(event='tcp_connected' if group=='g07' else 'connected',identity=identity,cookie=cookie)
                if group=='g07':conn['oracle']=dict(pid=pid,start_ticks=start,socket_cookie=cookie)
                rows.append(conn)
                if group=='g07':rows.append(dict(event='result',result=dict(protocol_ready=forward,sent=s['expected_bytes'],received=s['expected_bytes'],termination='eof',http_status={'http-valid':200,'http-missing':407,'http-wrong':403}.get(s['auth_variant']))))
                else:rows.append(dict(event='closed',sent=s['expected_bytes'],received=s['expected_bytes']))
                client=dict(pid=pid,rows=rows,exit_code=0,wait_reaped=True,proc_absent=True,independent_before_connect=dict(identity=identity,status_raw=stat),source_before='',source_after='',ledger_before=0,ledger_after=int(forward))
                if group=='g07':client.update(slot=copy.deepcopy(s),independent=client.pop('independent_before_connect'))
                clientrows.append(client)
                if forward:
                    fid='synthetic-flow-'+str(i);opened=dict(kind='opened',flow_id=fid,connector=dict(client_cookie=cookie,tgid_initial=pid,leader_start_boottime_ns=str(int(start)*10000000),executable=dict(program_name='python3',path_text='/bin/python3')));final=dict(kind='closed',flow_id=fid,upload_bytes=str(s['expected_bytes']),download_bytes=str(s['expected_bytes']));frames.extend([opened,final]);histories.append(dict(flow_id=fid,state='closed',tail_bytes=0,gap_dimensions=[],final=dict(upload_bytes=str(s['expected_bytes']),download_bytes=str(s['expected_bytes']))))
            frames.append(dict(kind='epoch_closed',finality='complete'))
            for seq,x in enumerate(frames,1):x.update(source_seq=str(seq),event_id='synthetic-event-%d'%seq,source_epoch=epoch,config_generation=generation,schema_version=5)
            manifest=self.save(name+'.source_manifest',run+'/evidence/trace/unit.source.json',dict(schema_version=2,identity_profile='linux_amd64_loopback_connect_v1',pid=70,start_ticks='170',binary_sha256=G.CORE,source_schema_version=5,source_epoch=epoch,config_generation=generation))
            decoded=self.save(name+'.decoded',run+'/source.decoded.json',dict(strict=True,frames=frames,synthetic_unit_fixture=True))
            recovery=self.save(name+'.recovery',run+'/source.recovery.json',dict(frames_sha256=source_raw['sha256'],manifest_sha256=manifest['sha256'],source_epoch=epoch,config_generation=generation,epoch_finality='complete',recording_status='unverified',last_source_seq=str(len(frames)),flows=histories,synthetic_unit_fixture=True))
            token=dict(type='file',uid=0,gid=0,mode=0o600,size=65,device=1,inode=99,sha256=hashlib.sha256(TOKEN+b'\n').hexdigest())
            instance=dict(pid=70,start_ticks='170',generation=generation)
            proof=dict(instance=instance,source_epoch=epoch)
            scenario=dict(status='observed_pending_host_check',mode=mode,errors=[],managers=[] if group=='collector' or name=='spool-permissions' else [dict(pid=20)],observations=[],**({'data_slots':clientrows} if group=='g07' else {'clients':clientrows}))
            host=dict(status=status,mode=mode,registered=n,attempted=n,executed=n,passed=n,core_sha256=G.CORE,helper_sha256=G.HELPER,source_epoch=epoch,**flags)
            host['flows']=[dict(cookie=conn['rows'][-2].get('cookie',conn['rows'][1].get('cookie')),pid=conn['pid'],start_ticks=conn['rows'][0]['identity']['start_ticks'],bytes_per_direction=slots[i]['expected_bytes']) for i,conn in enumerate(clientrows) if slots[i].get('expected_forwarded',True)]
            if group=='owner' and n:
                scenario['observations']=[dict(event=k,proof=copy.deepcopy(proof)) for k in ('core_before_offline','forwarded_manager_offline','same_core_takeover')]
            if name=='foreign-controller':host['source']=[dict(source_epoch=epoch)];host.update(foreign_received_bytes=0,foreign_accepts=1,actual_wrong_owner_connection_observed=True,core_wait_status='unobserved',control_negative_probes=1)
            if group=='f14':
                host.update(application_slots=n,control_negative_probes=1,source=dict(source_epoch=epoch,flows=copy.deepcopy(host['flows'])))
                scenario['observations']=[dict(event='stop_refused',reply=dict(status=503),operation=dict(phase='degraded')),dict(event='status_refused',reply=dict(status=503))]
                if name=='missing-claim':scenario['observations'] += [dict(event='claim_removed',claim_absent=True),dict(event='claim_restored',held_absent=True),dict(event='recovered',operation=dict(operation_id='f14-stop',phase='committed'),active=dict(version=1,revision=2,desired='stopped')),dict(event='original_core_exited',numeric_exit_status='unobserved')]
                else:
                    scenario['observations'] += [dict(event='initial',core=dict(pid=70,start_ticks='170')),dict(event='allocation',actual=dict(pid=70,start_ticks='180'))]
                    scenario.update(foreign_exit=dict(normal_stdin_stop=True,exit_code=0),foreign_rows=[dict(event=k,signals=[],observed_signal_numbers=[1,2,15,10,12]) for k in ['ready']+['snapshot']*4+['stopped']])
            if group=='collector':host['delivery']=dict(actual_duplicate_receipt=1,SQL_tested=False)
            if group=='g07':
                host.update(summary=dict(forwarded_flows=fwd,denied_data_slots=n-fwd),probes_registered=cp+fs,probes_executed=cp+fs,probes_passed=cp+fs)
                scenario.update(probes=[dict(probe=dict(probe_id='synthetic-probe-%d'%i)) for i in range(cp+fs)])
                if name=='management-data-auth':scenario.update(peercred_original=dict(members={'control.token':token}),peercred_restored=dict(members={'control.token':copy.deepcopy(token)}))
                for c in clientrows:
                    if not c['slot']['expected_forwarded']:c['ledger_after']=0
            if group=='logout':
                host['trigger_result']=dict(frontend_ordinary_quit_observed=True,interactive_login_shell_logout_observed=True,separate_explicit_handoff=True)
                scenario.update(frontend=dict(shell_exit=dict(exit_code=0)),shell_exit=dict(proc_absent=True),ssh_client=dict(exit_code=23),handoff=dict(signal=dict(pidfd_original=True)))
            if group=='ssh':scenario.update(ssh_client=dict(exit_code=-9,signal=9),remote_exit=dict(proc_absent=True,finished_monotonic_ns='10'),observations=[dict(event='forwarded_manager_offline',monotonic_ns='11')])
            if group=='s3':host['guest_OS_shutdown_tested']=False;scenario['observations']=[dict(event='guest_S3_returned')]
            exports=[]
            def exp(path,raw):
                ref=self.raw(run+'/evidence/'+path,raw);exports.append(ref);return ref
            def exp_json(path,value):
                ref=self.save(name+'.'+path,run+'/evidence/'+path,value);exports.append(ref);return ref
            if name in G.OMIT_TOKEN and name!='management-data-auth':
                scenario.update(token_metadata_before=token,token_metadata_after=copy.deepcopy(token))
                if group=='logout':exp_json('token-metadata.json',dict(before=token,after=token))
                else:exp('token-metadata.jsonl',''.join(json.dumps(dict(phase=p,metadata=token))+'\n' for p in ('before','after')).encode())
            if name in G.HISTORICAL_TOKEN:old_token=exp('instance/control.token',b'x'*64+b'\n')
            if self.alias_token and name=='ssh-disconnect':exp('unrelated-diagnostic.log',b'f'+TOKEN+b'e')
            exp_json('scenario.json',scenario);exp_json('readiness.json',dict(synthetic_unit_fixture=True));exp_json('assets/assets.json',dict(launcher=dict(sha256=G.HELPER)));exports.extend([source_raw,manifest])
            pins={'/work/mihomo-identity':G.CORE,'/work/controlprobe':G.HELPER}
            archive=self.raw(base+'/image-'+mode+'/guest-initramfs.cpio.gz',b'synthetic image, never executable')
            image=self.save(name+'.image',base+'/image-'+mode+'/guest-image.json',dict(pins=pins,sha256=archive['sha256']))
            self.raw(run+'/guest-image.json',(self.root/image['path']).read_bytes());self.raw(run+'/initramfs.cpio.gz',(self.root/archive['path']).read_bytes())
            # One synthetic registration per case (mode-specific directory name
            # below is not allowed by the gate, so shared registration files are
            # assembled after all cases of a real package are known).
            hostref=self.save(name+'.host',run+'/host-check.json',host)
            review_value=dict(review_kind='synthetic_independent_only',passed=True)
            if group=='collector':review_value['references']=[dict(path=hostref['path'],observation='Synthetic prose citation, no digest claim.')]
            review=self.save(name+'.review','private/reviews/'+name+'.json',review_value)
            rootaccept=self.save(name+'.accept',run+'/root-runtime-acceptance.json',dict(status='accepted_synthetic_unit_fixture_only',host_check=hostref,independent_review=review,registered=n,attempted=n,executed=n,passed=n,credential_export_protection_passed=False,control_negative_probes_passed=cp,registered_probes=cp+fs,executed_probes=cp+fs,passed_probes=cp+fs,**flags))
            tar=self.raw(run+'/evidence.tar.gz',b'synthetic archive placeholder')
            exported=self.save(name+'.export',run+'/export.json',dict(sha256=tar['sha256'],files=[dict(name=str(Path(x['path']).relative_to(run+'/evidence')),bytes=x['bytes'],sha256=x['sha256']) for x in exports]))
            runtime=dict(status='vm_exited_unverified',errors=[],pid=500+idx,exit_code=0,registered=n,attempted=n,executed=0,passed=0,**flags)
            if name!='collector-reopen':runtime['vm_started']=True
            if group=='s3':runtime['suspend']=dict(host_requested_QMP_quit=True,export_marker_observed=True,guest_OS_shutdown_tested=False)
            result=self.save(name+'.result',run+'/result.json',runtime)
            self.cases[name]=dict(mode=mode,run=run,registered=n,attempted=n,executed=n,passed=n,failed_or_pending=0,unobserved=0,vm_started=True,forwarded_flows=fwd,control_probes=cp,filesystem_probes=fs,slot_ids=[x['slot_id'] for x in slots],slots=slots,image=image,archive=archive,result=result,host_check=hostref,root_acceptance=rootaccept,independent_review=review,export=exported,export_files=exports,sources=[dict(frames=source_raw,manifest=manifest,decoded=decoded,recovery=recovery)])
            groups[group].append(name)
            if name in G.HISTORICAL_TOKEN:hygiene.append(dict(run=run,export_manifest=exported,archived_guest_credential_metadata=old_token,qemu_exit_code=0,original_guest_process_absent=True,credential_export_protection_passed=False))
        # Shared package registrations: all cases bind the same image bytes.
        for pkg in {s[1] for s in G.CASES.values()}:
            names=[n for n,s in G.CASES.items() if s[1]==pkg];base='private/'+pkg;first=self.cases[names[0]]
            reg=self.save(pkg+'.registration',base+'/calibration-v1/registration.json',dict(status='all_cases_registered_before_first_VM',core_sha256=G.CORE,helper_sha256=G.HELPER,registered_VMs=len(names),registered_flows=sum(G.CASES[n][3] for n in names),attempted=0,executed=0,passed=0,actual_VM_samples=0,**flags,cases={self.cases[n]['mode']:self.cases[n]['slots'] for n in names},image=first['image'],initramfs=first['archive']))
            regclaim=self.save(pkg+'.registration_claim',base+'/calibration-v1/registration-claim.json',dict(registration=reg,registered_VMs=len(names),registered_flows=sum(G.CASES[n][3] for n in names)))
            for name in names:
                row=self.cases[name];mode=row['mode'];run=row['run'];slots=row.pop('slots');row.pop('image');row.pop('archive')
                # All synthetic image metadata uses identical pins/archive.
                row['registration']=reg;row['registration_claim']=regclaim
                row['claim']=self.save(name+'.claim',base+'/calibration-v1/claim-'+mode+'.json',dict(registration=reg,mode=mode,slots=slots,attempted=len(slots),registered_flows=len(slots),run=run))
                row['launch']=self.save(name+'.launch',run+'/launch.json',dict(mode=mode,registration=reg,case_claim=row['claim'],registered_case=slots,image_pins=copy.deepcopy(pins),network_devices=False,host_mounts_shared=False,kernel_on_host_changed=False,host_cgroups_changed=False))
        reports={}
        for group,names in groups.items():
            references=[self.cases[n][k] for n in names for k in ('launch','result','host_check','root_acceptance','export')]
            runs=[dict(run=self.cases[n]['run'],mode=self.cases[n]['mode'],host_check=self.cases[n]['host_check'],root_acceptance=self.cases[n]['root_acceptance']) for n in names]
            if group=='owner':
                for name,row in zip(names,runs):
                    row.pop('mode');row.pop('root_acceptance');row['host_check']=copy.deepcopy(self.rows[name+'.host'])
            n=sum(G.CASES[x][3] for x in names)
            value=dict(status='accepted_synthetic_unit_fixture_only',candidate_core_sha256=G.CORE,helper_sha256=G.HELPER,registered_VMs=len(names),actual_VMs=len(names),registered=n,attempted=n,executed=n,passed=n,failed=0,pending=0,runs=runs,references=references,**flags)
            if group in ('owner','f14'):value.update(registered_control_negative_probes=sum(G.CASES[x][5] for x in names),executed_control_negative_probes=sum(G.CASES[x][5] for x in names),passed_control_negative_probes=sum(G.CASES[x][5] for x in names))
            if group=='g07':value.update(registered_control_probes=11,executed_control_probes=11,passed_control_probes=11,registered_filesystem_probes=14,executed_filesystem_probes=14,passed_filesystem_probes=14)
            if group=='collector':
                value.pop('runs');value.update(control_helper_launched=False,summary=copy.deepcopy(self.rows[names[0]+'.host']))
            reports[group]=self.save(group+'.public',G.PUBLIC[group],value)
        hygiene_ref=self.save('hygiene','private/hygiene.json',dict(status='historical_private_guest_credential_export_scope_recorded',rows=hygiene,original_results_modified=False,**flags))
        accepted=dict(schema='hoopa-source5-lifecycle-acceptance-v1',scope='restricted_source5_lifecycle',core_sha256=G.CORE,helper_sha256=G.HELPER,quality_acceptance=quality,public_reports=reports,cases=self.cases,**G.TOTALS,new_runtime_samples=0,source_fsync_proven=False,SQL_tested=False,task_T015_complete=True,P0_released=False,P1_released=False,export_hygiene_decision=hygiene_ref,f14_limits=dict(foreign_handled_signal_scope=[1,2,15,10,12],missing_claim_core_wait_status='unobserved',credential_export_protection_passed=False),historical_failures=[self.raw('private/retained-failure.txt',b'synthetic retained failure')],limits=['Synthetic graph only; no actual runtime sample.'])
        accepted['root_decision']=self.save('decision','private/root-final-decision.json',dict(decision='accept_restricted_source5_lifecycle',core_sha256=G.CORE,helper_sha256=G.HELPER,task_T015_complete=True,P0_released=False,P1_released=False,**G.TOTALS,new_runtime_samples=0,public_reports=reports,cases={n:r['root_acceptance'] for n,r in self.cases.items()},export_hygiene_decision=hygiene_ref))
        self.manifest=self.save('manifest',G.MANIFEST,accepted)


class Gate(unittest.TestCase):
    def test_complete_synthetic_graph_positive(self):
        with tempfile.TemporaryDirectory() as tmp:
            b=Bundle(tmp);r=G.check_bundle(tmp,b.manifest['path'],b.manifest['sha256']);self.assertTrue(r['passed']);self.assertEqual(r['slots'],22);self.assertEqual(r['forwarded_flows'],18)
    def test_semantic_negatives_full_graph(self):
        faults={
          'bool-count':('manifest',lambda x:x.update(slots=True)),
          'case-count':('manifest',lambda x:x['cases']['management-data-auth'].update(executed=2)),
          'missing-case':('manifest',lambda x:x['cases'].pop('guest-s3')),
          'helper':('takeover.image',lambda x:x['pins'].update({'/work/controlprobe':'0'*64})),
          'claim-run':('foreign-controller.claim',lambda x:x.update(run='private/t015-controller-owner-calibration/vm-takeover-synthetic-only')),
          'host-failed':('missing-claim.host',lambda x:x.update(status='failed')),
          'not-accepted':('takeover.accept',lambda x:x.update(status='pending')),
          'wrapper-rescored':('takeover.result',lambda x:x.update(passed=2)),
          'VM-failed':('takeover.result',lambda x:x.update(exit_code=1)),
          'token-omission':('ssh-disconnect.export',lambda x:x['files'].append(dict(name='instance/control.token',bytes=65,sha256='a'*64))),
          'release':('manifest',lambda x:x.update(P0_released=True)),
          'task':('manifest',lambda x:x.update(task_T015_complete=False)),
          'core':('manifest',lambda x:x.update(core_sha256='0'*64)),
          'bool-registration':('t015-controller-owner-calibration.registration',lambda x:x.update(registered_VMs=True)),
          'case-slot':('manifest',lambda x:x['cases']['takeover']['slot_ids'].__setitem__(0,'foreign-slot')),
          'reused-run':('manifest',lambda x:x['cases']['spawn-window'].update(run=x['cases']['takeover']['run'])),
          'foreign-census':('core-pid-reuse.scenario.json',lambda x:x['foreign_rows'].pop()),
          'foreign-signal':('core-pid-reuse.scenario.json',lambda x:x['foreign_rows'][0]['signals'].append(15)),
          'review-failed':('takeover.review',lambda x:x.update(passed=False)),
          'source-epoch':('takeover.decoded',lambda x:x['frames'][0].update(source_epoch='wrong')),
          'recovery-hash':('takeover.recovery',lambda x:x.update(frames_sha256='0'*64)),
          'denied-admitted':('management-data-auth.scenario.json',lambda x:x['data_slots'][1]['rows'][-1]['result'].update(protocol_ready=True)),
          'VM-unobserved':('takeover.result',lambda x:x.update(vm_started=False)),
          'FS-count':('manifest',lambda x:x.update(filesystem_probes=13)),
          'token-metadata':('ssh-disconnect.scenario.json',lambda x:x['token_metadata_after'].update(sha256='b'*64)),
          'case-mode':('manifest',lambda x:x['cases']['guest-s3'].update(mode='spawn-window')),
          'root-count':('decision',lambda x:x.update(control_probes=0)),
          'hygiene-pass':('hygiene',lambda x:x['rows'][0].update(credential_export_protection_passed=True)),
          'SQL':('manifest',lambda x:x.update(SQL_tested=True)),
          'missing-export':('manifest',lambda x:x['cases']['management-data-auth']['export_files'].pop()),
          'legacy-client-order':('takeover.scenario.json',lambda x:x['clients'].reverse()),
          'legacy-public-wrong-host':('owner.public',lambda x:x['runs'][0].update(host_check=x['runs'][1]['host_check'])),
          'legacy-public-duplicate-run':('owner.public',lambda x:x['runs'].__setitem__(0,copy.deepcopy(x['runs'][1]))),
          'legacy-public-missing-root-ref':('owner.public',lambda x:x.update(references=[r for r in x['references'] if not r['path'].endswith('root-runtime-acceptance.json')])),
          'collector-extra-runs':('collector.public',lambda x:x.update(runs=[])),
          'collector-citation-mixed-shape':('collector-reopen.review',lambda x:x['references'][0].update(sha256='0'*64)),
          'collector-citation-empty':('collector-reopen.review',lambda x:x.update(references=[])),
        }
        for label,(tag,mutate) in faults.items():
            with self.subTest(label=label),tempfile.TemporaryDirectory() as tmp:
                b=Bundle(tmp,lambda k,v:mutate(v) if k==tag else None)
                if label=='legacy-client-order':
                    with self.assertRaisesRegex(ValueError,'terminal client byte count mismatch'):
                        G.check_bundle(tmp,b.manifest['path'],b.manifest['sha256'])
                else:
                    with self.assertRaises((ValueError,KeyError,FileNotFoundError)):G.check_bundle(tmp,b.manifest['path'],b.manifest['sha256'])
    def test_original_token_content_rejected_with_valid_graph(self):
        for fault in ('alias','scenario'):
            with self.subTest(fault=fault),tempfile.TemporaryDirectory() as tmp:
                def mutate(tag,value):
                    if fault=='scenario' and tag=='ssh-disconnect.scenario.json':
                        value['diagnostic']='f'+TOKEN.decode('ascii')+'e'
                b=Bundle(tmp,mutate,alias_token=fault=='alias')
                # All references are assembled after the leak is inserted. The
                # rejection must be the content predicate, not stale hashes.
                with self.assertRaisesRegex(ValueError,'original control token found in exported content'):
                    G.check_bundle(tmp,b.manifest['path'],b.manifest['sha256'])
    def test_missing_tampered_and_duplicate_JSON(self):
        for fault in ('missing','tamper','duplicate','bool-ref','materialized-token'):
            with self.subTest(fault=fault),tempfile.TemporaryDirectory() as tmp:
                b=Bundle(tmp);p=Path(tmp)/b.cases['takeover']['host_check']['path']
                if fault=='missing':p.unlink()
                elif fault=='tamper':p.write_bytes(p.read_bytes()+b' ')
                elif fault=='duplicate':
                    p=Path(tmp)/G.MANIFEST;p.write_bytes(b'{"schema":1,"schema":2}');b.manifest['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
                elif fault=='materialized-token':
                    q=Path(tmp)/b.cases['ssh-disconnect']['run']/'evidence/instance/control.token';q.parent.mkdir(parents=True,exist_ok=True);q.write_bytes(b'synthetic-unexpected')
                else:
                    v=b.rows['manifest'];v['cases']['takeover']['host_check']['bytes']=True;b.manifest=b.raw(G.MANIFEST,json.dumps(v).encode())
                with self.assertRaises((ValueError,FileNotFoundError)):G.check_bundle(tmp,b.manifest['path'],b.manifest['sha256'])
if __name__=='__main__':unittest.main(verbosity=2)
