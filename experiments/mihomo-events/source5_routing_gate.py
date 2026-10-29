#!/usr/bin/env python3
"""Read only the fixed, independently accepted T016 evidence; start no samples."""
import argparse
import hashlib
import json
from pathlib import Path
import sys
import types

IDENTITY_PATH = 'experiments/mihomo-events/source5_identity_gate.py'
IDENTITY_SHA = 'ae4fc6917b97df64e9b05358a86c56c8f28e4847f77b2ca118f85aecd76a482d'
_identity_file = Path(__file__).resolve().with_name('source5_identity_gate.py')
_identity_raw = _identity_file.read_bytes()
if _identity_file.is_symlink() or hashlib.sha256(_identity_raw).hexdigest() != IDENTITY_SHA:
    raise ValueError('fixed read-only evidence implementation changed')
# -B forbids cache writes, but still permits loading a timestamp-valid .pyc.
# Execute exactly the checked source bytes, independent of that mutable cache.
_identity = types.ModuleType('hoopa_verified_identity_evidence')
_identity.__file__ = str(_identity_file)
exec(compile(_identity_raw,str(_identity_file),'exec'),_identity.__dict__)
Evidence, digest_string, require, strict_json = _identity.Evidence, _identity.digest_string, _identity.require, _identity.strict_json

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = 'experiments/mihomo-events/source5-routing-acceptance.json'
SCHEMA = 'hoopa-source5-routing-acceptance-check-v1'
CORE = '5e934bdc28dd07e7b7f8cf372d1d4f7a553a3aa3bd835de24f982dfed011a388'
HELPER = '1c87609972b0ac5570101c50945bfe4aa09a4687a433adb9c538722871107f95'
F11_HELPER = 'dc706c0325d02aa0ef94c0b8a8d4ab468b09e3f13dd26887aad8d2c08b4856a8'
ADAPTERS = 'private/t016-routing-gate-preparation/original-adapters.json'
ADAPTERS_SHA = 'e1ebacfc1017deaa984a8db6f0afbf50d600799fd2a876a57c2f2df642dce7ee'
ADMISSION = 'finite_missing_proof_restore_and_sticky_refusal'
ADMISSION_PUBLIC = 'experiments/mihomo-events/source5-admission-validation.json'
ADMISSION_ROOT = 'private/t016-multi-entry-preparation/root-admission-runtime-acceptance.json'
ADMISSION_STATUS = 'finite_missing_proof_restore_and_sticky_refusal_accepted'
TOTALS = dict(VMs=39, application_slots=127, admitted_flows=124, source_epochs=59, mandatory_frames=805)
COUNT_KEYS = ('VMs', 'application_slots', 'admitted_flows', 'source_epochs', 'mandatory_frames', 'checkpoints', 'frames')


def count(value, want, label):
    require(type(value) is int and value == want, label + ' count mismatch')


def bounded(value):
    require(value.get('task_T016_complete') is False and value.get('P0_released') is False and
            value.get('P1_released') is False, 'historical record was promoted')


def same_ref(e, actual, expected):
    require(type(actual) is dict and type(expected) is dict, 'complete reference')
    require(e.resolve(actual['path']) == e.resolve(expected['path']) and
            all(type(actual[k]) is type(expected[k]) and actual[k] == expected[k] for k in ('bytes', 'sha256')), 'reference path/hash/bytes changed')


def nested(e, value, mapping=False):
    # Only F11 root-row records use an explicitly accepted path-keyed inventory.
    if mapping:
        require(type(value.get('references')) is dict, 'F11 original reference mapping')
        rest = dict(value); refs = rest.pop('references')
        require(refs and all(e.resolve(k) == e.resolve(v['path']) for k,v in refs.items()), 'F11 reference map binding')
        e.nested(rest)
        for ref in refs.values(): e.reference(ref)
    else:
        e.nested(value)


def report(e, ref, schema=None, status=None, mapping=False):
    value = strict_json(e.reference(ref, content=True))
    require(type(value) is dict, 'original report object')
    if schema is not None: require(value.get('schema') == schema, 'exact report schema')
    if status is not None: require(value.get('status') == status, 'exact report status')
    nested(e, value, mapping)
    return value


def source_census(e, sources):
    require(type(sources) is list, 'original source inventory')
    total = dict(frames=0, mandatory_frames=0, checkpoints=0)
    epochs = set(); opened = set()
    for pair in sources:
        raw = e.reference(pair['frames'], content=True)
        manifest = report(e, pair['manifest'])
        epoch = manifest['source_epoch']
        require(epoch not in epochs, 'duplicate source epoch'); epochs.add(epoch)
        require(manifest['config_generation'] and manifest['binary_sha256'] == CORE and
                manifest['schema_version'] == 2 and manifest['source_schema_version'] == 5, 'original source candidate/schema')
        position = 0; sequence = 0
        while position < len(raw):
            require(position + 4 <= len(raw), 'truncated original frame header')
            size = int.from_bytes(raw[position:position+4], 'big')
            end = position + 4 + size + 32
            require(0 < size <= 8192 and end <= len(raw), 'complete bounded original frame')
            body = raw[position+4:position+4+size]
            require(hashlib.sha256(body).digest() == raw[end-32:end], 'original frame digest')
            event = strict_json(body); sequence += 1
            require(event['schema_version'] == 5 and event['source_epoch'] == epoch and
                    event['config_generation'] == manifest['config_generation'] and
                    event['source_seq'] == str(sequence), 'source sequence/epoch/generation binding')
            total['frames'] += 1
            total['checkpoints' if event['kind'] == 'bytes_checkpoint' else 'mandatory_frames'] += 1
            if event['kind'] == 'opened':
                require(event['flow_id'] not in opened, 'duplicate original admitted flow')
                opened.add(event['flow_id'])
            position = end
        require(sequence > 0, 'empty original source epoch')
    return dict(total, source_epochs=len(epochs), admitted_flows=len(opened)), epochs, opened


def verify_row(e, row, spec, family):
    # Every selected original, including its exact name, is part of the frozen
    # adapter table; manifest summaries cannot replace a missing row or run.
    require(row == spec, 'registered row/original bindings differ')
    run = e.resolve(row['run'])
    launch = report(e, row['launch']); reg = report(e, row['registration']); runtime = report(e, row['result'])
    require(e.resolve(row['launch']['path']) == run/'launch.json' and
            e.resolve(row['result']['path']) == run/'result.json', 'original run binding')
    same_ref(e, launch['registration'], row['registration'])
    claim = report(e, launch['claim']); same_ref(e, claim['registration'], row['registration'])
    require(e.resolve(claim['run']) == run, 'registered claim belongs to original run')
    for key in ('network_devices', 'host_mounts_shared', 'host_BPF_changed', 'host_cgroups_changed'):
        require(launch[key] is False, 'guest isolation changed')
    n = row['application_slots']
    count(launch['application_slots'], n, 'launch application')
    count(claim['application_slots'], n, 'claimed applications')
    if family != 'dual_owner_attach_prerequisite':
        require(type(launch['slots']) is list and len(launch['slots']) == n and
                claim['slots'] == launch['slots'], 'original claimed slot census')
        if family != 'F11_persistence_matrix_v2':
            require(reg['slots'] == launch['slots'], 'registered original slots')
    if family == 'F11_persistence_matrix_v2':
        cases = reg['matrix']['cases']; matches = [c for c in cases if c['id'] == row['name']]
        require(len(matches) == 1, 'registered F11 row missing')
        count(matches[0]['application_slots'], n, 'registered F11 applications')
        accepted = report(e, row['row_acceptance'], 'hoopa-f11-root-row-acceptance-v1',
                          'finite_row_independently_reviewed_and_root_accepted', mapping=True)
        bounded(accepted); same_ref(e, accepted['canonical'], row['canonical'])
        require(e.resolve(accepted['run']) == run and accepted['case_id'] == row['name'], 'F11 acceptance original run')
        require(launch['image_pins']['controlprobe'] == F11_HELPER, 'F11 paused helper binding')
    else:
        count(reg['registered_VMs'], 1, 'registered VM')
        count(reg['application_slots'], n, 'registered applications')
    for value in (reg, runtime): bounded(value)
    count(reg['attempted'], 0, 'preregistered attempted'); count(reg['executed'], 0, 'preregistered executed')
    count(runtime['application_slots'], n, 'runtime applications')
    require(runtime['status'] == 'vm_exited_unverified' and runtime['errors'] == [] and
            runtime['exit_code'] == 0 and type(runtime['exit_code']) is int, 'original VM failed')
    require(type(runtime['pid']) is int and runtime['pid'] > 1, 'actual VM process')
    # The two-owner prerequisite predates the explicit vm_started observation.
    if family != 'dual_owner_attach_prerequisite':
        require(runtime['vm_started'] is True and runtime['wait_reaped'] is True and runtime['process_absent'] is True, 'original VM lifecycle')
    canonical = report(e, row['canonical'], row['canonical_schema'], row['canonical_status'])
    require(row['independent_reviews'], 'independent review missing')
    for ref in row['independent_reviews']: e.reference(ref)
    census, epochs, flow_ids = source_census(e, row['sources'])
    if family == 'dual_owner_attach_prerequisite':
        count(canonical['exit_code'], 0, 'prerequisite canonical')
        same_ref(e, canonical['stdout'], row['prerequisite_stdout'])
        observed = report(e, row['prerequisite_stdout'], status='two_owner_attach_prerequisite_observed')
        bounded(observed)
        require(observed['actual_attach_passed'] is True, 'actual two-owner attach missing')
        count(observed['application_slots'], 0, 'prerequisite apps');count(observed['source_samples'], 0, 'prerequisite source')
        require(not epochs and n == 0, 'prerequisite cannot supply flows')
    else:
        bounded(canonical)
        if family in ('native_partial_bind_v4', 'native_typed_map_alias_v4'):
            count(canonical['formal_application_flows'], 0, 'native negative applications')
            count(canonical['source_frames'], census['frames'], 'native source frames')
            require(canonical['original_core_exit_code'] == 1 and canonical[
                'staged_startup_rejection' if family == 'native_partial_bind_v4' else 'actual_typed_map_alias_rejected'] is True,
                'actual native negative outcome missing')
        else:
            ids = [f['flow_id'] for f in canonical['flows']]
            require(len(ids) == len(set(ids)) and set(ids) == flow_ids, 'canonical/raw admitted flow census')
            count(canonical['source_frames'] if family == 'new_core_legacy_single_entry' else canonical['frames'], census['frames'], 'canonical frames')
        if 'epochs' in canonical:
            require({x['source_epoch'] for x in canonical['epochs']} == epochs and len(canonical['epochs']) == len(epochs), 'canonical epoch census')
        elif 'source_epoch' in canonical:
            require(epochs == {canonical['source_epoch']}, 'canonical source epoch')
    return dict(census, VMs=1, application_slots=n), run, epochs


def old_family(e, name, value, spec):
    require(set(value) == {'public_record', 'root_acceptance', 'rows'}, 'exact family manifest fields')
    for key in ('public_record', 'root_acceptance'):
        same_ref(e, value[key], spec[key])
    public = report(e, value['public_record'], spec['public_record']['schema'], spec['public_record']['status'])
    root = report(e, value['root_acceptance'], spec['root_acceptance']['schema'], spec['root_acceptance']['status'])
    bounded(public); bounded(root)
    if name == 'F11_persistence_matrix_v2':
        require(public['helper_sha256'] == F11_HELPER, 'historical F11 helper byte identity')
        require([r['case_id'] for r in public['rows']] == [r['name'] for r in spec['rows']] and
                [r['case_id'] for r in root['rows']] == [r['name'] for r in spec['rows']], 'F11 full 16-row census')
    elif name == 'ordered_routing_nine_rows':
        require([r['row'] for r in public['rows']] == [r['name'] for r in spec['rows']] and
                [r['row'] for r in root['rows']] == [r['name'] for r in spec['rows']], 'ordered full nine-row census')
    require(type(value['rows']) is list and len(value['rows']) == len(spec['rows']), 'missing registered row')
    totals = dict.fromkeys(COUNT_KEYS, 0); runs = set(); epochs = set()
    for row, expected in zip(value['rows'], spec['rows']):
        counts, run, observed = verify_row(e, row, expected, name)
        require(run not in runs and not epochs.intersection(observed), 'duplicate run/source epoch')
        runs.add(run);epochs.update(observed)
        for key in COUNT_KEYS: totals[key] += counts[key]
    for key, want in spec['expected_counts'].items(): count(totals[key], want, name+' '+key)
    return totals, runs, epochs


def admission_family(e, value):
    require(set(value) == {'public_record', 'root_acceptance', 'rows'}, 'exact admission fields')
    for key,path in (('public_record',ADMISSION_PUBLIC),('root_acceptance',ADMISSION_ROOT)):
        require(value[key]['path'] == path, 'fixed admission record path')
    public = report(e, value['public_record'], 'hoopa-t016-admission-validation-v1', ADMISSION_STATUS)
    root = report(e, value['root_acceptance'], 'hoopa-t016-admission-runtime-root-acceptance-v1', ADMISSION_STATUS)
    bounded(public);bounded(root)
    require(public['counts'] == root['counts'] and public['run'] == root['run'] and public['source_epoch'] == root['source_epoch'], 'admission root/public binding')
    require(len(value['rows']) == 1, 'one registered admission row')
    row = value['rows'][0]
    require(row['canonical_schema'] == 'hoopa-t016-admission-canonical-v1' and
            row['canonical_status'] == 'finite_admission_refusal_observed_pending_independent_review', 'fixed admission canonical contract')
    require(row['name'] == 'admission-baseline' and row['run'] == root['run'], 'fixed admission case/run')
    for value in (public,root):
        same_ref(e,value['canonical'],row['canonical'])
        require(value['independent_reviews']==row['independent_reviews'],'accepted admission original reviews binding')
    require(type(row['independent_reviews']) is list and row['independent_reviews'],'completed admission independent review required')
    for reference in row['independent_reviews']:
        reviewed=report(e,reference,'hoopa-t016-admission-independent-runtime-review-v1',
                        'complete_originals_reviewed_no_blocking_findings')
        require(e.resolve(reviewed['run'])==e.resolve(row['run']) and reviewed['source_epoch']==root['source_epoch'] and
                reviewed['blocking_findings']==[],'completed review of same original admission run')
        count(reviewed['actual_VM_samples'],0,'independent review creates no VM')
        same_ref(e,reviewed['canonical'],row['canonical'])
    # Admission uses the same original registration/launch/result/source fields.
    counts, run, epochs = verify_row(e, row, row, ADMISSION)
    expected = dict(VMs=1,application_slots=5,admitted_flows=2,source_epochs=1,mandatory_frames=13)
    for key,want in expected.items(): count(counts[key], want, 'admission '+key)
    require(0 <= counts['checkpoints'] <= 256 and epochs == {root['source_epoch']}, 'bounded actual admission source')
    for key in ('VMs','application_slots','source_epochs','mandatory_frames','checkpoints','frames'):
        count(root['counts'][key], counts[key], 'admission root '+key)
    count(root['counts']['flows'],2,'admission admitted');count(root['counts']['pre_admission_refusals'],3,'admission refused')
    require(len(root['flows']) == 2 and len(root['clients']) == 5, 'admission client/flow census')
    # Refused clients have no fabricated source event or final byte values.
    refused = [c for c in root['clients'] if c['index'] in (3,4,5)]
    require(len(refused) == 3 and {c['index'] for c in refused} == {3,4,5}, 'three original refusal attempts')
    for client in refused:
        require(all(client[k] is None for k in ('source_identity','source_terminal','source_bytes')), 'refusal fabricated source outcome')
    return counts, {run}, epochs


def check_bundle(root, manifest_path, manifest_sha256):
    e = Evidence(root)
    e.read(IDENTITY_PATH,IDENTITY_SHA)
    require(digest_string(manifest_sha256), 'manifest SHA256')
    require(e.resolve(manifest_path) == e.resolve(MANIFEST), 'fixed final acceptance path')
    accepted = strict_json(e.read(manifest_path, manifest_sha256, content=True))
    require(accepted['schema'] == 'hoopa-source5-routing-acceptance-v1' and accepted['scope'] == 'restricted_source5_multi_entry_routing' and
            accepted['core_sha256'] == CORE and accepted['helper_sha256'] == HELPER, 'fixed routing acceptance candidate')
    require(accepted['task_T016_complete'] is True and accepted['P0_released'] is False and accepted['P1_released'] is False, 'restricted root decision')
    count(accepted['new_runtime_samples'],0,'new runtime samples')
    require(accepted['helper_variants'] == {'F11_persistence_matrix_v2':F11_HELPER}, 'explicit accepted helper overlay')
    adapters = strict_json(e.read(ADAPTERS, ADAPTERS_SHA, content=True))['families']
    require(set(accepted['families']) == set(adapters)|{ADMISSION} and len(adapters) == 15, 'complete sixteen-family census')
    totals = dict.fromkeys(COUNT_KEYS,0); all_runs=set(); all_epochs=set(); results={}
    for name in list(adapters)+[ADMISSION]:
        counts,runs,epochs = (admission_family(e,accepted['families'][name]) if name == ADMISSION else old_family(e,name,accepted['families'][name],adapters[name]))
        require(not all_runs.intersection(runs) and not all_epochs.intersection(epochs), 'cross-family run/source epoch reused')
        all_runs.update(runs);all_epochs.update(epochs);results[name]=counts
        for key in COUNT_KEYS: totals[key]+=counts[key]
    for key,want in TOTALS.items(): count(totals[key],want,'observed total '+key)
    for key in COUNT_KEYS: count(accepted['counts'][key],totals[key],'manifest total '+key)
    require(totals['frames'] == totals['mandatory_frames']+totals['checkpoints'], 'complete frame census')
    decision = report(e, accepted['root_decision'], 'hoopa-source5-routing-root-decision-v1', 'accept_restricted_source5_multi_entry_routing')
    require(decision['families'] == accepted['families'] and decision['counts'] == accepted['counts'] and
            decision['task_T016_complete'] is True and decision['P0_released'] is False and decision['P1_released'] is False and
            decision['core_sha256'] == CORE and decision['helper_sha256'] == HELPER, 'final independent root decision binding')
    e.nested(accepted);e.finish()
    return dict(schema=SCHEMA,passed=True,counts=totals,families=results,verified_files=len(e.cache),
                task_T016_complete=True,P0_released=False,P1_released=False,new_runtime_samples=0,
                verification='read_only_integrity_of_independently_accepted_evidence')


def main(argv=None):
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--manifest',required=True);parser.add_argument('--manifest-sha256',required=True)
    args=parser.parse_args(argv)
    try: result=check_bundle(ROOT,args.manifest,args.manifest_sha256)
    except Exception as exc: result=dict(schema=SCHEMA,passed=False,error=str(exc),task_T016_complete=False,P0_released=False,P1_released=False,new_runtime_samples=0)
    print(json.dumps(result,sort_keys=True,allow_nan=False));return 0 if result['passed'] else 1

if __name__=='__main__': sys.exit(main())
