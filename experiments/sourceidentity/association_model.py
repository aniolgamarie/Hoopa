"""Finite accepted-socket association research model, without native/network calls.

One edge is a complete callback, abstracting atomic BPF_NOEXIST publication,
not real map-helper execution, linearizability, verifier or memory ordering.
IPv4 TCP endpoints are fixed to 127.0.0.1; ports are normalized client/server.
C -> A -> P is conditional on ordinary handshake ordering; P -> Q is only
a model scheduling constraint, not a claim about accept or FD delivery;
all cross-flow interleavings are explored, subject to explicit reuse dependency.
Q is an abstract offline witness observation, not the instant accept returns.
Only final publication is interpreted after stable stop. This graph proves no
real accept visibility or synchronization and supplies no online identity.
TCP Fast Open, simultaneous open and TCP_REPAIR are outside this scope.
C snapshots generation/netns/configured server port and connector PID/start.
C cannot predict the client port; A takes the full tuple from its active event.
Current FD holders are irrelevant.
Q uses only the original accepted socket witness generation/netns/cookie.
No overwrite, delete, reclamation or TCP_CLOSE index mutation is modeled.

The conditional bounded claim requires complete callback coverage, fresh object
identity generation, true socket netns cookies, stable stop, ordinary handshake,
and a valid original accepted FD witness. Ground truth is only an exploration
oracle, never a lookup input. Zero faults alone cannot establish coverage: the
omitted active callback counterexample deliberately produces a wrong candidate.
A witness from another real accepted FD can also give a zero-fault wrong
candidate: the resolver cannot determine original FD provenance from numbers.
No output is a passed CapabilityRecord or kernel/runtime identity proof.
"""
from collections import deque, namedtuple
from dataclasses import dataclass, replace
import hashlib
import json
from pathlib import Path

SCHEMA = 'sourceidentity-association-model-v1'


@dataclass(frozen=True)
class Witness:
    generation: int
    netns: int
    accepted_cookie: int


@dataclass(frozen=True)
class Flow:
    generation: int
    netns: int
    client_port: int
    server_port: int
    client_cookie: int
    accepted_cookie: int
    pid: int
    start: int
    witness: Witness = None
    after: int = None
    successful: bool = True

    @property
    def key(self):
        return (self.generation, self.netns, self.client_port, self.server_port)

    @property
    def identity(self):
        return (self.pid, self.start)

    @property
    def original_witness(self):
        return self.witness or Witness(self.generation, self.netns, self.accepted_cookie)


State = namedtuple('State', 'phases connects index bindings queries faults')


def _positive(value, maximum):
    return type(value) is int and 0 < value <= maximum


def initial_state(flows):
    if len(flows) not in (1, 2, 3):
        raise ValueError('bounded model supports 1, 2, or 3 flows')
    for i, flow in enumerate(flows):
        if not isinstance(flow, Flow):
            raise ValueError('flow must be Flow')
        for field in ('generation', 'netns', 'client_cookie', 'accepted_cookie', 'start'):
            if not _positive(getattr(flow, field), 2**64 - 1):
                raise ValueError('invalid ' + field)
        if not all(_positive(p, 65535) for p in (flow.client_port, flow.server_port)):
            raise ValueError('invalid normalized loopback port')
        if not _positive(flow.pid, 2**32 - 1) or flow.client_cookie == flow.accepted_cookie:
            raise ValueError('invalid PID or non-independent socket cookies')
        if flow.after is not None and (type(flow.after) is not int or not 0 <= flow.after < i):
            raise ValueError('dependency must name an earlier flow')
        witness = flow.original_witness
        if not isinstance(witness, Witness) or not all(_positive(v, 2**64 - 1) for v in
                (witness.generation, witness.netns, witness.accepted_cookie)):
            raise ValueError('invalid original accepted socket witness')
        if type(flow.successful) is not bool:
            raise ValueError('successful must be bool')
    return State((0,) * len(flows), (), (), (), (), ())


def successors(state, flows, mode='correct', omitted=(), capacities=(3, 3, 3)):
    """Whole-callback edges; omissions simulate an actually present missed event.

    Missing events do not themselves increment faults; downstream missing lookup
    may do so. This intentionally avoids assuming completeness from fault count.
    """
    if mode not in ('correct', 'ignore_faults'):
        raise ValueError('unknown mode')
    if len(capacities) != 3 or any(type(c) is not int or not 0 <= c <= 3 for c in capacities):
        raise ValueError('capacities must be three integers from 0 to 3')
    if any(type(i) is not int or not 0 <= i < len(flows) or s not in ('C', 'A', 'P')
           for i, s in omitted):
        raise ValueError('invalid omitted callback')
    for i, flow in enumerate(flows):
        phase = state.phases[i]
        if phase == 4 or (phase == 0 and flow.after is not None and
                          state.phases[flow.after] != 4):
            continue
        stage = 'CAPQ'[phase]
        phases = list(state.phases)
        phases[i] = 4 if not flow.successful else phase + 1
        changes = dict(phases=tuple(phases))
        faults = list(state.faults)

        def fault(reason):
            faults.append('flow {} {}: {}'.format(i, stage, reason))

        def insert(field, key, value, capacity):
            table = dict(getattr(state, field))
            if key in table:
                fault(field + ' NOEXIST conflict')
            elif len(table) >= capacity:
                fault(field + ' capacity')
            else:
                table[key] = value
                changes[field] = tuple(sorted(table.items()))

        if (i, stage) not in omitted:
            if stage == 'C':
                # This record, not the oracle flow->connector relation, supplies A.
                context = (flow.generation, flow.netns, flow.server_port)
                insert('connects', flow.client_cookie, (context, flow.identity), capacities[0])
            elif stage == 'A':
                snapshot = dict(state.connects).get(flow.client_cookie)
                context = (flow.generation, flow.netns, flow.server_port)
                if snapshot is None or snapshot[0] != context:
                    fault('missing or inconsistent connect snapshot')
                else:
                    insert('index', flow.key, flow.client_cookie, capacities[1])
            elif stage == 'P':
                client = dict(state.index).get(flow.key)
                if client is None:
                    fault('missing active index')
                else:
                    insert('bindings', (flow.generation, flow.netns, flow.accepted_cookie),
                           client, capacities[2])
            else:
                witness = flow.original_witness
                key = (witness.generation, witness.netns, witness.accepted_cookie)
                # Only pre-registered generation/netns are known to the resolver.
                # Cookie lookup cannot establish that this was the original FD.
                if key[:2] != (flow.generation, flow.netns):
                    fault('original accepted witness mismatch')
                else:
                    client = dict(state.bindings).get(key)
                    snapshot = dict(state.connects).get(client)
                    if snapshot is None:
                        fault('missing passive binding or connect snapshot')
                    else:
                        changes['queries'] = state.queries + ((i, snapshot[1]),)
        changes['faults'] = tuple(faults)
        yield 'flow {} {}'.format(i, stage), state._replace(**changes)
    if all(p == 4 for p in state.phases):
        yield 'TCP_CLOSE (no reclamation)', state


def candidate_pairs(state, flows, mode='correct'):
    """Whole-round publication only, after stable stop (assumed, not modeled).

    Does not consult oracle identities or presume observable coverage is proven.
    The deliberately unsafe ignore_faults mode demonstrates stale publication.
    """
    if mode not in ('correct', 'ignore_faults'):
        raise ValueError('unknown mode')
    if (any(p != 4 for p in state.phases) or not all(f.successful for f in flows)
            or len(state.queries) != len(flows) or (state.faults and mode == 'correct')):
        return ()
    return tuple(sorted(state.queries))


def _trace(parents, state):
    actions = []
    while parents[state] is not None:
        state, action = parents[state]
        actions.append(action)
    return list(reversed(actions))


def explore(flows, mode='correct', omitted=(), capacities=(3, 3, 3)):
    start = initial_state(flows)
    parents, pending = {start: None}, deque([start])
    transitions = candidates = wrong = faulted = 0
    bad_trace = None
    while pending:
        state = pending.popleft()
        pairs = candidate_pairs(state, flows, mode)
        if all(p == 4 for p in state.phases):
            faulted += bool(state.faults)
        if pairs:
            candidates += 1
            # Ground truth enters only here, after candidate lookup/publication.
            if any(identity != flows[i].identity for i, identity in pairs):
                wrong += 1
                if bad_trace is None:
                    bad_trace = _trace(parents, state)
        for action, following in successors(state, flows, mode, omitted, capacities):
            transitions += 1
            assert following.faults[:len(state.faults)] == state.faults
            for field in ('connects', 'index', 'bindings'):
                assert dict(getattr(state, field)).items() <= dict(getattr(following, field)).items()
            if following not in parents:
                parents[following] = (state, action)
                pending.append(following)
    return dict(flows=len(flows), mode=mode, reachable_states=len(parents),
                transitions=transitions, candidate_states=candidates,
                faulted_terminal_states=faulted, wrong_candidate_states=wrong,
                wrong_candidate_trace=bad_trace, complete_callback_coverage=not omitted,
                omitted_callbacks=[dict(flow=i, stage=s) for i, s in omitted])


def sample_flows(count, reuse=False):
    return tuple(Flow(1, 10, 40000 if reuse else 40000 + i, 8080,
                      100 + i * 2, 101 + i * 2, 200, 1000 + i,
                      after=i - 1 if reuse and i else None) for i in range(count))


def research_report():
    flows = sample_flows(1)
    state = initial_state(flows)
    for stage in 'CAPQ':
        state = dict(successors(state, flows))['flow 0 ' + stage]
    examples = [dict(generation=str(flows[i].generation), netns=str(flows[i].netns),
                     accepted_cookie=str(flows[i].accepted_cookie),
                     connector=dict(pid=identity[0], start=str(identity[1])))
                for i, identity in candidate_pairs(state, flows)]
    first, second = sample_flows(2)
    wrong_fd_flows = (first, replace(second, after=0, witness=first.original_witness))
    return dict(schema=SCHEMA, runtime='unverified', verifier='unverified',
                identity_capability='unverified', result_claim='bounded_model_only',
                implementation_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                scope=__doc__,
                required_assumptions=['complete callback coverage', 'fresh object generation',
                    'true socket netns cookie', 'ordinary handshake ordering',
                    'original accepted FD witness', 'stable completed stop before publication'],
                missing_runtime_evidence=['callback coverage', 'map helper atomicity and ordering',
                    'generation and namespace provenance', 'verifier acceptance',
                    'accepted FD witness provenance', 'stable stop integration'],
                correct=[explore(sample_flows(n)) for n in (1, 2, 3)],
                reuse_with_faults_rejected=explore(sample_flows(2, reuse=True)),
                unsafe_counterexamples=[
                    dict(explore(sample_flows(2, reuse=True), mode='ignore_faults'),
                         scenario='ignore_sticky_fault',
                         inputs=dict(sample_flows_count=2, reuse=True)),
                    dict(explore(sample_flows(2, reuse=True), omitted=((1, 'A'),)),
                         scenario='reused_tuple_missing_active',
                         inputs=dict(sample_flows_count=2, reuse=True)),
                    dict(explore(wrong_fd_flows), scenario='wrong_original_fd',
                         inputs=dict(sample_flows_count=2, reuse=False,
                             flow1_after=0, witness_flow=1, witness_source_flow=0,
                             generation=str(first.generation), netns=str(first.netns),
                             flow0_accepted_cookie=str(first.accepted_cookie),
                             flow1_accepted_cookie=str(second.accepted_cookie),
                             witness_accepted_cookie=str(first.accepted_cookie)))],
                unsafe_counterexample_meanings=['ignoring sticky faults permits stale reuse',
                    'missing active coverage permits zero-fault stale reuse',
                    'wrong real accepted FD permits zero-fault identity misattribution'],
                witness_warning='Original accepted FD provenance requires independent evidence; '
                    'a resolver cannot establish it by comparing numeric cookie values.',
                coverage_warning='Zero faults does not prove callback coverage or correct identity.',
                example_candidate_pairs=examples)


if __name__ == '__main__':
    print(json.dumps(research_report(), indent=2, sort_keys=True))
