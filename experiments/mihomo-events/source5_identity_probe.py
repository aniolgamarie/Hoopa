"""Read-only opt-in source5 positive census; this does not complete T127/G01."""
import argparse
from contextlib import contextmanager
import hashlib
import json
from pathlib import Path
import re
import sys
import types

VALIDATOR_SHA256 = '6682701bbfb57755a879a39128ba6bc18cc9b1c8adc246b88be8881b7eef711b'
VALIDATOR_PATH = Path(__file__).resolve().parents[2] / 'private/t127-preparation/document_runtime_v2.py'
SCHEMA = 'sourceidentity-source5-registered-identity-positives-v1'
MODES = ('ordinary', 'descendant', 'fork-writer', 'scm-writer', 'thread')


def require(ok, detail):
    if not ok:
        raise ValueError(detail)


def _strict_json(raw):
    def pairs(items):
        out = {}
        for key, value in items:
            require(key not in out, 'duplicate JSON key')
            out[key] = value
        return out
    def nonfinite(value):
        raise ValueError('nonfinite JSON')
    return json.loads(raw, object_pairs_hook=pairs, parse_constant=nonfinite)


def _failure():
    return dict(schema=SCHEMA, passed=False, phase='formal', registered=100,
                positive_slots_passed=0, source_processes=0, target_domains=0,
                T127_complete=False, boundary_matrix_complete=False,
                formal_g01_complete=False, P0_released=False, P1_released=False, errors=[])


@contextmanager
def _load_verified_validator(path, raw):
    """Internal: caller must have verified the exact fixed source hash first."""
    name = '_hoopa_source5_validator_' + VALIDATOR_SHA256
    previous = sys.modules.get(name)
    module = types.ModuleType(name)
    module.__file__ = str(path)
    module.__package__ = ''
    try:
        sys.modules[name] = module
        exec(compile(raw, str(path), 'exec'), module.__dict__)
        yield module.aggregate
    finally:
        if previous is None:
            sys.modules.pop(name, None)
        else:
            sys.modules[name] = previous


def _summarize(registration, result):
    require(registration['phase'] == 'formal' and registration['registered_denominator'] == 100
            and len(registration['slots']) == 100, 'formal complete 100-slot registration required')
    require(result['schema'] == 'hoopa-source5-formal-runtime-aggregation-v2'
            and result['phase'] == 'formal'
            and result['status'] == 'positive_evidence_verified_root_acceptance_separate',
            'verified formal positive aggregation required')
    totals = result['totals']
    require(totals['registered_slots'] == totals['verified_positive_flows'] == 100
            and totals['vms'] == totals['complete_epochs'] == 5, 'complete 100-slot five-epoch census required')
    require(result['task_T127_complete'] is False and result['boundary_matrix_complete'] is False
            and result['P0_released'] is False and result['P1_released'] is False, 'positive-only acceptance boundary')
    runs = result['runs']
    require(len(runs) == 5 and {r['mode'] for r in runs} == set(MODES), 'exact five-mode census')
    observed, processes, domains = [], set(), set()
    for run in runs:
        require(run['status'] == 'all_registered_positive_slots_evidence_verified'
                and run['complete_epochs'] == 1 and len(run['flows']) == 20, 'complete per-mode evidence')
        for row in run['flows']:
            slot, original, source, terminal, writer = [row[k] for k in
                ('slot', 'original', 'source_connector', 'terminal', 'writer')]
            observed.append(slot)
            require(slot['mode'] == run['mode'] and row['flow']['outcome'] == 'forwarded'
                    and row['flow']['actual_node'] == slot['expected_node']
                    and row['flow']['final_each_direction'] == 33, 'registered target and actual route')
            require(original['pid'] == original['tgid'] == source['tgid_initial']
                    and original['connect_tid'] == source['caller_tid_initial']
                    and original['socket_cookie'] == source['client_cookie'], 'original source identity evidence')
            require(terminal['upload_bytes'] == terminal['download_bytes'] == '33'
                    and terminal['unallocated_upload_bytes'] == terminal['unallocated_download_bytes'] == '0'
                    and len(terminal['segments']) == 1
                    and terminal['segments'][0]['upload_bytes'] == terminal['segments'][0]['download_bytes'] == '33',
                    'exact source integer terminal')
            require(writer['errors'] == [] and writer['writer_pid'] == writer['writer_process']['pid']
                    and writer['application_bytes_sent'] == writer['application_bytes_received'] == 33
                    and writer['protocol_ready'] is True and writer['early_halfclose'] is True and writer['peer_eof'] is True,
                    'complete writer terminal evidence')
            payload = (slot['token'] + '\n').encode('ascii')
            digest = hashlib.sha256(payload).hexdigest()
            require(writer['request'] == writer['response'] == dict(bytes=33, sha256=digest), 'exact writer token evidence')
            processes.add((original['boot_id'], original['pid'], original['start_ticks']))
            domains.add(slot['target'])
    require(len(observed) == 100 and len({s['slot_id'] for s in observed}) == 100
            and sorted(observed, key=lambda s:s['index']) == registration['slots'], 'all original slots preserved')
    require(len(processes) >= 2 and len(domains) >= 2, 'independent source processes and target domains')
    summary = _failure()
    summary.update(passed=True, positive_slots_passed=100, source_processes=len(processes),
                   target_domains=len(domains), vms=5, complete_epochs=5,
                   scope='read-only registered positives only; T127/G01 and boundary negatives remain separately assessed')
    return summary


def _aggregate_verified(registration_path, registration_raw, validator_path, validator_raw, *, _unit_aggregate=None):
    """Internal post-hash stage; _unit_aggregate is fake-only unit injection.

    The CLI never accepts or supplies an injection. Production callers enter
    through probe(), which pins and hashes both original files before this stage.
    """
    registration = _strict_json(registration_raw)
    require(registration['phase'] == 'formal' and registration['registered_denominator'] == 100
            and len(registration['slots']) == 100, 'formal complete 100-slot registration required')
    if _unit_aggregate is not None:
        result = _unit_aggregate(registration_path)
    else:
        with _load_verified_validator(validator_path, validator_raw) as aggregate:
            result = aggregate(registration_path)
    return _summarize(registration, result)


def probe(registration_path, registration_sha256, validator_path, validator_sha256):
    require(re.fullmatch('[0-9a-f]{64}', registration_sha256) is not None, 'canonical registration SHA256 required')
    require(validator_sha256 == VALIDATOR_SHA256, 'only the fixed reviewed validator SHA256 is allowed')
    registration_path = Path(registration_path).resolve(strict=True)
    validator_path = Path(validator_path).resolve(strict=True)
    require(validator_path == VALIDATOR_PATH.resolve(), 'only the fixed current-repository validator path is allowed')
    registration_raw = registration_path.read_bytes()
    validator_raw = validator_path.read_bytes()
    require(hashlib.sha256(registration_raw).hexdigest() == registration_sha256, 'registration SHA256 mismatch')
    require(hashlib.sha256(validator_raw).hexdigest() == VALIDATOR_SHA256, 'validator source SHA256 mismatch')
    result = _aggregate_verified(registration_path, registration_raw, validator_path, validator_raw)
    require(registration_path.read_bytes() == registration_raw, 'registration changed during read-only validation')
    result.update(registration_sha256=registration_sha256, validator_sha256=VALIDATOR_SHA256)
    return result


class _Parser(argparse.ArgumentParser):
    def error(self, message):
        raise ValueError('invalid command arguments')


def main(argv=None):
    try:
        parser = _Parser(description=__doc__)
        parser.add_argument('--registration', required=True)
        parser.add_argument('--registration-sha256', required=True)
        parser.add_argument('--validator', required=True)
        parser.add_argument('--validator-sha256', required=True)
        args = parser.parse_args(argv)
        result = probe(args.registration, args.registration_sha256, args.validator, args.validator_sha256)
        code = 0
    except Exception as exc:
        result = _failure()
        result['errors'] = [dict(type=type(exc).__name__, detail=str(exc))]
        code = 1
    print(json.dumps(result, sort_keys=True))
    return code


if __name__ == '__main__':
    raise SystemExit(main())
