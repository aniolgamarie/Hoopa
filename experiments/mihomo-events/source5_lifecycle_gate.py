#!/usr/bin/env python3
"""Read-only integrity gate for independently accepted finite T015 evidence.

No private script, decoder, VM, proxy, receiver or recovery writer is executed.
"""
import argparse
import hashlib
from pathlib import Path
import re
import sys
from source5_identity_gate import CORE, Evidence, digest_string, require, strict_json
from source5_recovery_gate import count, read_report, unreleased

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = 'experiments/mihomo-events/source5-lifecycle-acceptance.json'
SCHEMA = 'hoopa-source5-lifecycle-acceptance-check-v1'
HELPER = 'ad4a8069691e6668c658eb98c18c845bc3b2f5f903a08fe1db28560c2c92e13f'
# group, package, mode, application attempts, forwarded, control, filesystem, host status
CASES = {
 'takeover': ('owner','t015-controller-owner-calibration','takeover',2,2,0,0,'finite_control_takeover_observed'),
 'spawn-window': ('owner','t015-controller-owner-calibration','spawn-window',2,2,0,0,'finite_control_takeover_observed'),
 'foreign-controller': ('owner','t015-controller-owner-calibration','foreign-controller',0,0,1,0,'foreign_controller_startup_refused_zero_bytes'),
 'missing-claim': ('f14','t015-f14-v2-vm','missing-claim',2,2,1,0,'finite_f14_observed'),
 'core-pid-reuse': ('f14','t015-f14-v2-vm','core-pid-reuse',1,1,1,0,'finite_f14_observed'),
 'collector-reopen': ('collector','t015-collector-control-v2-vm','collector-reopen',2,2,0,0,'collector_crash_same_core_reopen_observed'),
 'management-data-auth': ('g07','t015-g07-vm','management-data-auth',6,2,11,0,'all_finite_g07_case_evidence_verified'),
 'spool-permissions': ('g07','t015-g07-vm','spool-permissions',1,1,0,14,'all_finite_g07_case_evidence_verified'),
 'frontend-quit-login-logout': ('logout','t015-logout-vm','frontend-quit-login-logout',2,2,0,0,'finite_frontend_quit_and_interactive_logout_observed'),
 'ssh-disconnect': ('ssh','t015-ssh-newhelper-vm','ssh-disconnect-takeover',2,2,0,0,'finite_ssh_control_takeover_observed'),
 'guest-s3': ('s3','t015-suspend-newhelper-vm','takeover',2,2,0,0,'finite_control_takeover_observed'),
}
PUBLIC = {g:'experiments/mihomo-events/control-0017-'+n+'-calibration-validation.json' for g,n in
          [('owner','controller-owner'),('f14','f14'),('collector','collector'),('g07','g07'),
           ('logout','logout'),('ssh','ssh-newhelper'),('s3','suspend-newhelper')]}
HISTORICAL_TOKEN = {'takeover','spawn-window','foreign-controller','missing-claim','core-pid-reuse'}
OMIT_TOKEN = {'management-data-auth','frontend-quit-login-logout','ssh-disconnect','guest-s3'}
TOTALS = dict(vms=11,slots=22,forwarded_flows=18,control_probes=14,filesystem_probes=14)


def same_ref(e, a, b):
    require(e.resolve(a['path']) == e.resolve(b['path']) and
            all(a[k] == b[k] for k in ('bytes','sha256')), 'reference belongs to another original')
    e.reference(a); e.reference(b)


def original(e, row, key, path):
    require(e.resolve(row[key]['path']) == path, 'foreign '+key+' artifact')
    return read_report(e,row[key])


def independent_review(e,ref,name):
    if name!='collector-reopen':return read_report(e,ref)
    # The original collector review uses `references` for prose citations,
    # without byte/hash claims. Its complete JSON is pinned by ref; canonical
    # artifacts are independently pinned by the public report and case row.
    review=strict_json(e.reference(ref,content=True))
    require(isinstance(review,dict) and isinstance(review.get('references'),list) and review['references'],
            'legacy collector review citations absent')
    for citation in review['references']:
        require(isinstance(citation,dict) and {'path','observation'}<=set(citation) and
                set(citation)<={'path','observation','lines'} and
                isinstance(citation['observation'],str) and
                ('lines' not in citation or isinstance(citation['lines'],str)),
                'legacy collector review citation shape changed')
        e.resolve(citation['path'])
    e.nested({k:v for k,v in review.items() if k!='references'})
    return review


def bounded(value, task=False):
    unreleased(value)
    if task: require(value.get('task_T015_complete') is False, 'original task flag changed')
    for key in ('source_fsync_proven','SQL_tested'):
        if key in value: require(value[key] is False, 'unsupported '+key+' claim')


def case_counts(row, spec):
    n, fwd, cp, fs = spec[3:7]
    for key in ('registered','attempted','executed','passed'): count(row.get(key),n,'case '+key+' mismatch')
    for key in ('failed_or_pending','unobserved'): count(row.get(key),0,'case incomplete')
    require(row.get('vm_started') is True,'VM start not accepted')
    for key,want in [('forwarded_flows',fwd),('control_probes',cp),('filesystem_probes',fs)]:
        count(row.get(key),want,'case '+key+' mismatch')


def decimal(value):
    require(isinstance(value,str) and re.fullmatch('[1-9][0-9]*',value) is not None,
            'noncanonical original timestamp/identity')
    return int(value)


def clients(scenario, slots, name):
    rows = scenario.get('data_slots',[]) if name in ('management-data-auth','spool-permissions') else scenario.get('clients',[])
    require(len(rows)==len(slots),'original client inventory differs from registered attempts')
    observed=[]; pids=set(); cookies=set()
    for index,(client,slot) in enumerate(zip(rows,slots)):
        if 'slot' in client: require(client['slot']==slot,'client slot/token differs from registration')
        require(client['exit_code']==0 and client['wait_reaped'] is True and client['proc_absent'] is True and
                not client.get('cleanup_killed'), 'original client did not finish')
        events=client['rows']; require(events[0]['event']=='before_connect','missing original connection boundary')
        conn=[x for x in events if x['event'] in ('connected','tcp_connected')]
        require(len(conn)==1,'missing/duplicate actual connected row')
        c=conn[0]; identity=events[0]['identity']; pid=client['pid']; start=identity['start_ticks']
        require(type(pid) is int and pid>1 and pid==identity['pid'] and pid not in pids,'client identity reused')
        decimal(start); pids.add(pid)
        ind=client.get('independent_before_connect',client.get('independent'))
        independent=ind.get('identity',ind.get('process'))
        require(independent['pid']==pid and independent['start_ticks']==start,'independent original client mismatch')
        fields=dict(x.split(':',1) for x in ind['status_raw'].splitlines() if ':' in x)
        require(fields['Uid'].split()==fields['Gid'].split()==['65534']*4 and not fields['Groups'].strip() and
                fields['NoNewPrivs'].strip()=='1' and all(int(fields[x].strip(),16)==0 for x in
                ('CapInh','CapPrm','CapEff','CapBnd','CapAmb')),'client UID/caps not original unprivileged instance')
        calls=[x for x in events if x['event']=='connect_call']
        if name in ('management-data-auth','spool-permissions','frontend-quit-login-logout'):
            require(len(calls)==1,'original connect call absent'); decimal(calls[0]['connect_boot_before_ns'])
        else: require(len(calls)<=1,'duplicate original connect call')
        oracle=c.get('oracle'); cookie=oracle['socket_cookie'] if oracle else c['cookie']
        require(isinstance(cookie,str) and re.fullmatch('[1-9][0-9]*',cookie) is not None and cookie not in cookies,'cookie collision')
        cookies.add(cookie)
        require((oracle['pid'] if oracle else c['identity']['pid'])==pid and
                (oracle['start_ticks'] if oracle else c['identity']['start_ticks'])==start,'connected identity mismatch')
        forward=slot.get('expected_forwarded',True)
        require(type(forward) is bool,'forwarded flag must be boolean')
        expected=slot['expected_bytes']; require(type(expected) is int,'slot byte count must be integer')
        final=events[-1]
        if name in ('management-data-auth','spool-permissions'):
            result=final['result'];require(final['event']=='result' and result['protocol_ready'] is forward and
                result['sent']==result['received']==expected and result['termination'] in ('eof','reset'),'auth attempt result mismatch')
            require(type(result['sent']) is int and type(result['received']) is int,'application counters must be integers')
            if not forward:
                require(client['source_before']==client['source_after'] and client['ledger_before']==client['ledger_after'],
                        'denied attempt forwarded or created source')
            if slot['protocol']=='http': require(result['http_status']=={'http-valid':200,'http-missing':407,'http-wrong':403}[slot['auth_variant']], 'HTTP denial branch differs')
        else: require(type(final['sent']) is int and type(final['received']) is int and final['event']=='closed' and final['sent']==final['received']==expected,'terminal client byte count mismatch')
        observed.append(dict(pid=pid,start_ticks=start,cookie=cookie,forwarded=forward,bytes=expected))
    require(sum(x['forwarded'] for x in observed)==CASES[name][4],'forwarded/denied denominator mismatch')
    return observed


def sources(e,row,run,observed):
    require(isinstance(row['sources'],list) and len(row['sources'])==1,'one original epoch required')
    source=row['sources'][0]
    for key in ('frames','manifest'):
        path=e.resolve(source[key]['path']); require(run/'evidence' in path.parents,'source outside original exports')
        e.reference(source[key])
    for key in ('decoded','recovery'):
        require(e.resolve(source[key]['path']).parent==run,'canonical source report outside run')
    manifest=read_report(e,source['manifest']); decoded=read_report(e,source['decoded']); recovery=read_report(e,source['recovery'])
    require(manifest['schema_version']==2 and manifest['identity_profile']=='linux_amd64_loopback_connect_v1' and manifest['binary_sha256']==CORE and manifest['source_schema_version']==5 and decoded['strict'] is True,
            'original fixed source5 evidence missing')
    require(recovery['frames_sha256']==source['frames']['sha256'] and recovery['manifest_sha256']==source['manifest']['sha256'] and
            recovery['source_epoch']==manifest['source_epoch'] and recovery['config_generation']==manifest['config_generation'] and
            recovery['epoch_finality']=='complete' and recovery['recording_status']=='unverified','canonical source recovery binding mismatch')
    frames=decoded['frames']; require(frames and [x['source_seq'] for x in frames]==[str(i) for i in range(1,len(frames)+1)] and
        len({x['event_id'] for x in frames})==len(frames) and all(x['source_epoch']==manifest['source_epoch'] and
        x['config_generation']==manifest['config_generation'] and x['schema_version']==5 and x['kind']!='gap' for x in frames),
        'source sequence/epoch/generation differs')
    require(frames[-1]['kind']=='epoch_closed' and frames[-1]['finality']=='complete' and recovery['last_source_seq']==str(len(frames)),'source epoch incomplete')
    opened=[x for x in frames if x['kind']=='opened']; forwarded=[x for x in observed if x['forwarded']]
    require(len(opened)==len(recovery['flows'])==len(forwarded),'source flow denominator mismatch')
    flow_ids={x['flow_id'] for x in opened};require(len(flow_ids)==len(opened) and all(x['kind']=='epoch_closed' or x.get('flow_id') in flow_ids for x in frames),'orphan source event')
    for client in observed:
        found=[x for x in opened if x['connector']['client_cookie']==client['cookie']]
        require(len(found)==int(client['forwarded']),'denied flow/source cookie mismatch')
        if not found:continue
        frame=found[0];c=frame['connector'];require(c['tgid_initial']==client['pid'] and
            int(c['leader_start_boottime_ns'])//10000000==int(client['start_ticks']) and
            c['executable']['program_name']=='python3' and c['executable']['path_text']=='/bin/python3','original source connector mismatch')
        final=[x for x in frames if x.get('flow_id')==frame['flow_id'] and x['kind']=='closed']
        history=[x for x in recovery['flows'] if x['flow_id']==frame['flow_id']]
        require(len(final)==len(history)==1 and history[0]['state']=='closed' and history[0]['tail_bytes']==0 and
                history[0]['gap_dimensions']==[] and final[0]['upload_bytes']==final[0]['download_bytes']==str(client['bytes']) and
                history[0]['final']['upload_bytes']==history[0]['final']['download_bytes']==str(client['bytes']),'source terminal differs')
    return manifest


def token_metadata(meta):
    require(meta['type']=='file' and meta['uid']==meta['gid']==0 and meta['mode']==0o600 and meta['size']==65 and
            type(meta['inode']) is int and meta['inode']>0 and digest_string(meta['sha256']),'private token metadata absent')


def token_omitted(e, refs, meta):
    # The case export census already bounds these original members to 16 MiB.
    # Lookahead includes every window in longer hex runs; no candidate is logged.
    # This detects the original token bytes, not arbitrary encoded secrets.
    for ref in refs:
        raw=e.read(ref['path'],ref['sha256'],ref['bytes'],content=True)
        for match in re.finditer(rb'(?=([0-9a-f]{64}))',raw):
            require(hashlib.sha256(match.group(1)+b'\n').hexdigest()!=meta['sha256'],
                    'original control token found in exported content')


def semantic_case(name,scenario,host,public_row):
    events={}
    for event in scenario.get('observations',[]): events.setdefault(event['event'],[]).append(event)
    def one(key):
        require(len(events.get(key,[]))==1,'missing/duplicate '+key); return events[key][0]
    if name=='foreign-controller':
        count(host.get('foreign_received_bytes'),0,'foreign received application bytes')
        count(host.get('foreign_accepts'),1,'actual wrong-owner connection absent')
        require(host.get('actual_wrong_owner_connection_observed') is True and host.get('core_wait_status')=='unobserved','foreign branch/status claim mismatch')
    elif name in ('missing-claim','core-pid-reuse'):
        refused=one('stop_refused'); require(refused['reply']['status']==503 and refused['operation']['phase']=='degraded','F14 refusal lost')
        require(one('status_refused')['reply']['status']==503,'F14 status not unknown refusal')
        if name=='missing-claim':
            require(one('claim_removed')['claim_absent'] is True and one('claim_restored')['held_absent'] is True and
                one('recovered')['operation']['operation_id']=='f14-stop' and one('recovered')['operation']['phase']=='committed' and
                one('recovered')['active']==dict(version=1,revision=2,desired='stopped') and
                one('original_core_exited')['numeric_exit_status']=='unobserved','missing-claim recovery/wait boundary differs')
        else:
            original=one('initial')['core'];foreign=one('allocation')['actual'];require(original['pid']==foreign['pid'] and original['start_ticks']!=foreign['start_ticks'],'actual PID reuse absent')
            require(scenario['foreign_exit']['normal_stdin_stop'] is True and scenario['foreign_exit']['exit_code']==0,'foreign cleanup changed')
            require(len(scenario['foreign_rows'])==6 and [x['event'] for x in scenario['foreign_rows']]==['ready']+['snapshot']*4+['stopped'],'foreign response census missing')
            require(all(x['signals']==[] and x['observed_signal_numbers']==[1,2,15,10,12] for x in scenario['foreign_rows']),'foreign signal ledger boundary differs')
    elif name=='collector-reopen':
        count(host['delivery']['actual_duplicate_receipt'],1,'same journal original receipt replay absent')
        require(host['delivery']['SQL_tested'] is False,'receiver is not SQL')
    elif name in ('management-data-auth','spool-permissions'):
        count(host['summary']['forwarded_flows'],CASES[name][4],'G07 forwarded denominator')
        count(host['summary']['denied_data_slots'],4 if name=='management-data-auth' else 0,'G07 denied denominator')
        require(len(scenario['probes'])==CASES[name][5]+CASES[name][6],'G07 original probe census missing')
        require(len({p['probe']['probe_id'] for p in scenario['probes']})==len(scenario['probes']),'G07 duplicate probe')
        for key in ('probes_registered','probes_executed','probes_passed'):count(host[key],len(scenario['probes']),'G07 probe canonical count')
    elif name=='frontend-quit-login-logout':
        t=host['trigger_result'];require(t['frontend_ordinary_quit_observed'] is True and t['interactive_login_shell_logout_observed'] is True and
            scenario['frontend']['shell_exit']==dict(exit_code=0) and scenario['shell_exit']['proc_absent'] is True and scenario['ssh_client']['exit_code']==23,'frontend/logout triggers conflated')
        if t['separate_explicit_handoff']:require(scenario['handoff']['signal']['pidfd_original'] is True,'handoff is not owned separate action')
    elif name=='ssh-disconnect':
        require(scenario['ssh_client']['exit_code']==-9 and scenario['ssh_client']['signal']==9 and scenario['remote_exit']['proc_absent'] is True,'real SSH disconnect/remote exit missing')
        require(int(scenario['remote_exit']['finished_monotonic_ns'])<int(one('forwarded_manager_offline')['monotonic_ns']),'offline traffic ordering differs')
    elif name=='guest-s3':
        require(host['guest_OS_shutdown_tested'] is False,'host quit is not guest shutdown')
        one('guest_S3_returned')
    elif name in ('takeover','spawn-window'):
        a=one('core_before_offline')['proof'];b=one('forwarded_manager_offline')['proof'];c=one('same_core_takeover')['proof']
        require(a['instance']==b['instance']==c['instance'] and a['source_epoch']==b['source_epoch']==c['source_epoch'],'same core takeover not preserved')


def check_bundle(root,manifest_path,manifest_sha256):
    e=Evidence(root);require(digest_string(manifest_sha256),'invalid manifest SHA256')
    require(e.resolve(manifest_path)==e.resolve(MANIFEST),'unexpected acceptance path')
    accepted=strict_json(e.read(manifest_path,manifest_sha256,content=True))
    require(isinstance(accepted,dict) and accepted.get('schema')=='hoopa-source5-lifecycle-acceptance-v1' and
            accepted.get('scope')=='restricted_source5_lifecycle' and accepted.get('core_sha256')==CORE and
            accepted.get('helper_sha256')==HELPER,'unexpected lifecycle schema/scope/candidate')
    require(accepted.get('task_T015_complete') is True,'root acceptance missing'); bounded(accepted)
    require(accepted.get('source_fsync_proven') is False and accepted.get('SQL_tested') is False,'unsupported acceptance guarantee')
    for key,want in dict(TOTALS,new_runtime_samples=0).items():count(accepted.get(key),want,'total '+key+' mismatch')
    require(set(accepted['cases'])==set(CASES),'case census differs')
    quality=read_report(e,accepted['quality_acceptance'],'hoopa-source5-quality-acceptance-v1')
    require(e.resolve(accepted['quality_acceptance']['path'])==e.resolve('experiments/mihomo-events/source5-quality-acceptance.json') and
            quality.get('scope')=='restricted_source5_quality' and quality.get('core_sha256')==CORE and quality.get('task_T129_complete') is True,'same-core accepted T129 prerequisite missing');unreleased(quality)
    count(quality.get('vms'),6,'quality VM prerequisite');count(quality.get('slots'),21,'quality slot prerequisite')
    require(set(accepted['public_reports'])==set(PUBLIC),'public report census differs')
    reports={}
    for group,path in PUBLIC.items():
        ref=accepted['public_reports'][group];require(e.resolve(ref['path'])==e.resolve(path),'wrong public report')
        report=read_report(e,ref);bounded(report,True)
        require(report.get('candidate_core_sha256')==CORE and isinstance(report.get('status'),str) and report['status'].startswith('accepted_'),'public report not accepted candidate')
        require(isinstance(report.get('references'),list) and report['references'],'public reference inventory absent')
        specs=[s for s in CASES.values() if s[0]==group]
        for key,want in [('registered_VMs',len(specs)),('actual_VMs',len(specs))]+[(k,sum(s[3] for s in specs)) for k in ('registered','attempted','executed','passed')]+[('failed',0),('pending',0)]:count(report.get(key),want,'public '+key+' mismatch')
        if group!='collector':require(report.get('helper_sha256',report.get('managed_helper_sha256'))==HELPER,'public helper mismatch')
        else:
            require(report.get('control_helper_launched') is False,'collector secretly launched helper')
            require('runs' not in report,'legacy collector summary shape changed')
        if group in ('owner','f14'):
            for key in ('registered_control_negative_probes','executed_control_negative_probes','passed_control_negative_probes'):count(report.get(key),sum(x[5] for x in specs),'public control probe mismatch')
        if group=='g07':
            for prefix in ('registered','executed','passed'):
                count(report.get(prefix+'_control_probes'),11,'public G07 control mismatch');count(report.get(prefix+'_filesystem_probes'),14,'public G07 filesystem mismatch')
        if group!='collector':
            require(isinstance(report.get('runs'),list) and len(report['runs'])==len(specs) and
                    len({e.resolve(x['run']) for x in report['runs']})==len(specs),'public run census differs')
        reports[group]=report
    runs=set();global_slots=set();result_cases={};historical_runs={}
    for name,spec in CASES.items():
        group,package,mode,n,fwd,cp,fs,status=spec;row=accepted['cases'][name];case_counts(row,spec)
        require(row['mode']==mode,'case/mode mismatch');run=e.resolve(row['run'])
        require(run.is_dir() and run.parent==e.root/'private'/package and run.name.startswith('vm-'+mode+'-') and run not in runs,'run reused or wrong candidate package');runs.add(run)
        reg=read_report(e,row['registration']);regpath=e.resolve(row['registration']['path'])
        require(regpath.parent==run.parent/'calibration-v1' and regpath.name=='registration.json','foreign registration')
        regclaim=original(e,row,'registration_claim',regpath.parent/'registration-claim.json');same_ref(e,regclaim['registration'],row['registration'])
        require(reg['status']=='all_cases_registered_before_first_VM' and reg['core_sha256']==CORE,'registration not original fixed candidate')
        pkg_specs=[x for x in CASES.values() if x[1]==package];reg_total=sum(x[3] for x in pkg_specs)
        bounded(reg,True)
        for record in (reg,regclaim):
            count(record.get('registered_VMs'),len(pkg_specs),'original total registration VM count')
            count(record.get('registered_flows',record.get('registered_application_slots')),reg_total,'original total registration app count')
        for key in ('attempted','executed','passed','actual_VM_samples'):count(reg.get(key),0,'registration was rescored')
        slots=reg['cases'][mode];require(len(slots)==n and row['slot_ids']==[s['slot_id'] for s in slots] and len(set(row['slot_ids']))==n,'registered slot census differs')
        require(not global_slots.intersection(row['slot_ids']),'cross-case slot collision');global_slots.update(row['slot_ids'])
        claim=original(e,row,'claim',regpath.parent/('claim-'+mode+'.json'));same_ref(e,claim['registration'],row['registration'])
        require(claim['mode']==mode and claim['slots']==slots and e.resolve(claim['run'])==run,'case claim belongs elsewhere');count(claim['attempted'],n,'claim attempt mismatch')
        count(claim.get('registered_flows',claim.get('registered_application_slots')),n,'claim registration mismatch')
        launch=original(e,row,'launch',run/'launch.json');runtime=original(e,row,'result',run/'result.json');host=original(e,row,'host_check',run/'host-check.json')
        same_ref(e,launch['registration'],row['registration']);same_ref(e,launch['case_claim'],row['claim'])
        require(launch['mode']==host['mode']==mode and launch['registered_case']==slots,'original launch/canonical mode mismatch')
        require(all(launch.get(k) is False for k in ('network_devices','host_mounts_shared','kernel_on_host_changed','host_cgroups_changed')),'VM isolation changed')
        require(launch['image_pins']['/work/mihomo-identity']==CORE and host['status']==status,'canonical host verdict not successful original')
        for value in (runtime,host):bounded(value,True)
        require(runtime['status']=='vm_exited_unverified' and runtime['errors']==[] and type(runtime['pid']) is int and runtime['pid']>1,'original VM start/exit evidence absent');count(runtime['exit_code'],0,'VM failed')
        if name!='collector-reopen':require(runtime.get('vm_started') is True,'VM start flag unobserved')
        for key in ('registered','attempted'):count(runtime[key],n,'original wrapper denominator changed')
        for key in ('executed','passed'):count(runtime[key],0,'original wrapper score was rewritten')
        if 'errors' in host:require(host['errors']==[],'canonical host errors')
        if group=='f14':count(host['application_slots'],n,'F14 app denominator')
        else:
            for key in ('registered','attempted','executed','passed'):count(host[key],n,'canonical '+key+' mismatch')
        for artifact,key in [('guest-image.json','image'),('initramfs.cpio.gz','initramfs')]:e.read(str(run/artifact),reg[key]['sha256'],reg[key]['bytes'])
        image=strict_json(e.read(str(run/'guest-image.json'),content=True));require(image['pins']==launch['image_pins'],'image/launch pins differ')
        require(image['sha256']==reg['initramfs']['sha256'],'image/archive binding differs')
        exported=original(e,row,'export',run/'export.json');items=exported['files'];require(0<len(items)<=100 and len({x['name'] for x in items})==len(items),'invalid export member census')
        require(all(type(x['bytes']) is int and x['bytes']>=0 for x in items) and sum(x['bytes'] for x in items)<=16*1024*1024,'export byte budget invalid')
        require(len(row['export_files'])==len(items),'omitted canonical export member');names=set()
        for item,ref in zip(items,row['export_files']):
            rel=Path(item['name']);require(not rel.is_absolute() and '..' not in rel.parts and str(rel)==item['name'],'unsafe export member')
            require(e.resolve(ref['path'])==run/'evidence'/rel and all(ref[k]==item[k] for k in ('bytes','sha256')),'export original hash/order mismatch');e.reference(ref);names.add(item['name'])
        e.read(str(run/'evidence.tar.gz'),exported['sha256'])
        scenario=strict_json(e.read(str(run/'evidence/scenario.json'),content=True));require(scenario['status']=='observed_pending_host_check' and scenario['errors']==[] and scenario['mode']==mode,'original guest not complete')
        require('scenario.json' in names and 'readiness.json' in names,'original guest records omitted')
        if name not in ('collector-reopen','spool-permissions'):
            require(launch['image_pins']['/work/controlprobe']==reg['helper_sha256']==HELPER,'wrong managed helper')
            assets=strict_json(e.read(str(run/'evidence/assets/assets.json'),content=True));require(assets['launcher']['sha256']==HELPER,'assets refer to old helper')
            if 'helper_sha256' in host:require(host['helper_sha256']==HELPER,'canonical helper mismatch')
        else:require(scenario.get('managers',[])==[],'direct-core case acquired a manager')
        export_paths={e.resolve(ref['path']) for ref in row['export_files']}
        require(all(e.resolve(src[k]['path']) in export_paths for src in row['sources'] for k in ('frames','manifest')),'source is not an original exported member')
        if name not in ('collector-reopen','spool-permissions'):require('assets/assets.json' in names,'managed assets not exported')
        observed=clients(scenario,slots,name);manifest=sources(e,row,run,observed)
        host_epoch=host['source'][0]['source_epoch'] if name=='foreign-controller' else host['source']['source_epoch'] if group=='f14' else host['source_epoch']
        require(host_epoch==manifest['source_epoch'],'canonical host/source epoch differs')
        core_instance=host.get('core_instance',scenario.get('core_before',{}).get('instance'))
        if core_instance is not None:require(manifest['pid']==core_instance['pid'] and manifest['start_ticks']==core_instance['start_ticks'] and manifest['config_generation']==core_instance['generation'],'canonical original core differs from source manifest')
        if group!='g07' and name!='foreign-controller':
            flows=host['source']['flows'] if group=='f14' else host['flows'];require(len(flows)==fwd,'canonical host flow census differs')
            for client in observed:
                matched=[flow for flow in flows if flow['cookie']==client['cookie']]
                require(len(matched)==1 and matched[0]['pid']==client['pid'] and matched[0]['start_ticks']==client['start_ticks'] and matched[0]['bytes_per_direction']==client['bytes'],'canonical host/client flow binding differs')
        if name=='guest-s3':require(runtime['suspend']['host_requested_QMP_quit'] is True and runtime['suspend']['export_marker_observed'] is True and runtime['suspend']['guest_OS_shutdown_tested'] is False,'S3 QMP teardown evidence absent')
        root_accept=original(e,row,'root_acceptance',run/'root-runtime-acceptance.json');bounded(root_accept,True)
        require(isinstance(root_accept.get('status'),str) and root_accept['status'].startswith('accepted_'),'case not root accepted')
        same_ref(e,root_accept['host_check'],row['host_check']);same_ref(e,root_accept['independent_review'],row['independent_review'])
        review=independent_review(e,row['independent_review'],name);require(review and review.get('passed') is not False and review.get('status') not in ('failed','rejected'),'independent review rejected')
        for key in ('registered','attempted','executed','passed'):count(root_accept[key],n,'root canonical denominator differs')
        if cp and group!='g07':count(root_accept['control_negative_probes_passed'],cp,'root negative control denominator differs')
        if group=='g07':
            for key in ('registered_probes','executed_probes','passed_probes'):count(root_accept[key],cp+fs,'root G07 probe denominator differs')
        public=reports[group];pub_refs={e.resolve(x['path']) for x in public['references']}
        require(all(e.resolve(row[k]['path']) in pub_refs for k in ('launch','result','host_check','root_acceptance','export')),'public report omits case originals')
        public_rows=public.get('runs',[]);public_row=next((x for x in public_rows if e.resolve(x['run'])==run),None)
        if group=='collector':require(public['summary']==host,'collector public summary differs')
        else:
            require(public_row is not None,'public run inventory differs')
            if group=='owner':
                # This original summary embeds the complete per-run host check;
                # original host/root paths are in the references checked above.
                require('mode' not in public_row and 'root_acceptance' not in public_row,
                        'legacy owner summary shape changed')
                require(public_row['host_check']==host,'legacy public host summary differs')
            else:
                require(public_row['mode']==mode,'public run mode differs')
                same_ref(e,public_row['host_check'],row['host_check'])
                same_ref(e,public_row['root_acceptance'],row['root_acceptance'])
        semantic_case(name,scenario,host,public_row)
        if cp and group!='g07':count(host.get('control_negative_probes',host.get('control_negative_probes_passed')),cp,'control probe denominator differs')
        if name in OMIT_TOKEN or name in ('collector-reopen','spool-permissions'):
            require('instance/control.token' not in names and not (run/'evidence/instance/control.token').exists(),'plaintext token omission violated')
        if name=='management-data-auth':
            a=scenario['peercred_original']['members']['control.token'];b=scenario['peercred_restored']['members']['control.token'];token_metadata(a);require(a==b,'G07 token metadata changed')
        elif name in OMIT_TOKEN:
            a=scenario['token_metadata_before'];token_metadata(a);require(a==scenario['token_metadata_after'],'token metadata/hash changed')
            require(('token-metadata.json' if name=='frontend-quit-login-logout' else 'token-metadata.jsonl') in names,'token metadata not exported')
            if name=='frontend-quit-login-logout': require(strict_json(e.read(str(run/'evidence/token-metadata.json'),content=True))==dict(before=a,after=a),'token metadata original missing')
            else:require([strict_json(x) for x in e.read(str(run/'evidence/token-metadata.jsonl'),content=True).splitlines()]==[dict(phase=p,metadata=a) for p in ('before','after')],'token metadata original missing')
        if name in OMIT_TOKEN:token_omitted(e,row['export_files'],a)
        if name in HISTORICAL_TOKEN:
            require('instance/control.token' in names,'historical token evidence was removed');historical_runs[run]=row
            if group=='f14':require(root_accept.get('credential_export_protection_passed') is False,'F14 export protection falsely awarded')
        result_cases[name]={k:row[k] for k in ('registered','attempted','executed','passed','failed_or_pending','unobserved','vm_started','forwarded_flows','control_probes','filesystem_probes')}
    hygiene=read_report(e,accepted['export_hygiene_decision']);bounded(hygiene,True)
    require(hygiene['status']=='historical_private_guest_credential_export_scope_recorded' and hygiene['original_results_modified'] is False,'historical hygiene decision absent')
    rows=hygiene['rows'];require(len(rows)==5 and {e.resolve(x['run']) for x in rows}==set(historical_runs),'historical hygiene scope differs')
    for item in rows:
        row=historical_runs[e.resolve(item['run'])];same_ref(e,item['export_manifest'],row['export']);ref=item['archived_guest_credential_metadata'];require(e.resolve(ref['path'])==e.resolve(item['run'])/'evidence/instance/control.token','wrong historical credential metadata');e.reference(ref)
        count(ref['bytes'],65,'historical token byte count');count(item['qemu_exit_code'],0,'historical VM failed');require(item['original_guest_process_absent'] is True and item['credential_export_protection_passed'] is False,'historical hygiene was rescored')
    require(accepted['f14_limits']==dict(foreign_handled_signal_scope=[1,2,15,10,12],missing_claim_core_wait_status='unobserved',credential_export_protection_passed=False),'F14 scope expanded')
    require(all(type(x) is int for x in accepted['f14_limits']['foreign_handled_signal_scope']),'signal scope must be integer')
    decision=read_report(e,accepted['root_decision']);bounded(decision)
    require(decision.get('decision')=='accept_restricted_source5_lifecycle' and decision.get('task_T015_complete') is True and decision.get('core_sha256')==CORE and decision.get('helper_sha256')==HELPER,'final root acceptance absent')
    for key,want in dict(TOTALS,new_runtime_samples=0).items():count(decision.get(key),want,'root total differs')
    require(set(decision['public_reports'])==set(PUBLIC),'root public report census differs')
    for group in PUBLIC:same_ref(e,decision['public_reports'][group],accepted['public_reports'][group])
    require(set(decision['cases'])==set(CASES),'root case census differs')
    for name,row in accepted['cases'].items():same_ref(e,decision['cases'][name],row['root_acceptance'])
    same_ref(e,decision['export_hygiene_decision'],accepted['export_hygiene_decision'])
    require(isinstance(accepted.get('limits'),list) and accepted['limits'] and accepted.get('historical_failures'),'bounded limits/failure history absent')
    e.nested(accepted);e.finish()
    return dict(schema=SCHEMA,passed=True,cases=result_cases,**TOTALS,new_runtime_samples=0,task_T015_complete=True,
                P0_released=False,P1_released=False,verified_files=len(e.cache),verification='read_only_integrity_of_independently_accepted_evidence')


def main(argv=None):
    import json
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--manifest',required=True);p.add_argument('--manifest-sha256',required=True);a=p.parse_args(argv)
    try:result=check_bundle(ROOT,a.manifest,a.manifest_sha256)
    except Exception as exc:result=dict(schema=SCHEMA,passed=False,error=str(exc),task_T015_complete=False,P0_released=False,P1_released=False,new_runtime_samples=0)
    print(json.dumps(result,sort_keys=True,allow_nan=False));return 0 if result['passed'] else 1
if __name__=='__main__':sys.exit(main())
