"""Bounded interleaving research model; no kernel/runtime proof or native calls.

Each edge is one lock operation or one outside-lock observation/fault write.
Callbacks pause before admission, before writing, and before exit. The shared
lock makes admission/stop/exit/sampling atomic. Closed never reopens; admitted
callbacks finish every write before exit. Faults affect integrity, not drain.
"""
import argparse
import json
from collections import deque, namedtuple

SCHEMA = "sourceidentity-stop-model-v1"
PROTOCOLS = ("correct", "split_admission", "stale_map_update")
# Callback phases: 0 before admission, 1 admitted, 2 wrote, 3 exited,
# 4 rejected, 5 read open in the deliberately broken split admission model.
State = namedtuple("State", "closed active phases observations faults frozen saved")


def initial_state(callbacks):
    if callbacks not in (1, 2, 3):
        raise ValueError("bounded model supports 1, 2, or 3 callbacks")
    return State(False, 0, (0,) * callbacks, 0, 0, False, -1)


def successors(state, protocol="correct"):
    """Yield deterministic action labels and states, including failed samples."""
    if protocol not in PROTOCOLS:
        raise ValueError("unknown protocol")
    if not state.closed:
        if protocol == "stale_map_update":
            if state.saved == -1:
                yield "stop: userspace reads active", state._replace(saved=state.active)
            else:
                yield "stop: write whole map with stale active", state._replace(
                    closed=True, active=state.saved)
        else:
            yield "stop: close under lock", state._replace(closed=True)
    if not state.frozen:
        # Sampling closed and active is one lock-consistent read. In the split
        # model stop already happened, but its active-only wait is insufficient.
        if state.closed and state.active == 0:
            yield "sample: freeze_result", state._replace(frozen=True)
        else:
            yield "sample: no certificate", state
    for index, phase in enumerate(state.phases):
        def advance(new_phase, **changes):
            phases = list(state.phases)
            phases[index] = new_phase
            return state._replace(phases=tuple(phases), **changes)
        prefix = "callback {}: ".format(index)
        if phase == 0:
            if state.closed:
                yield prefix + "reject closed (no writes)", advance(4)
            elif protocol == "split_admission":
                yield prefix + "read open outside lock", advance(5)
            else:
                yield prefix + "admit under lock", advance(1, active=state.active + 1)
        elif phase == 5:
            yield prefix + "late increment active", advance(1, active=state.active + 1)
        elif phase == 1:
            yield prefix + "observation write", advance(2, observations=state.observations + 1)
            yield prefix + "fault write", advance(2, faults=state.faults + 1)
        elif phase == 2:
            yield prefix + "exit under lock", advance(3, active=state.active - 1)


def _trace(parents, state, action):
    actions = [action]
    while parents[state] is not None:
        state, previous_action = parents[state]
        actions.append(previous_action)
    return list(reversed(actions))


def explore(callbacks, protocol="correct"):
    """BFS with deduplicated states; retain shortest reproducible bad traces."""
    start = initial_state(callbacks)
    pending, parents = deque([start]), {start: None}
    transitions = certificates = fault_certificates = 0
    late_write = lost_count = None
    while pending:
        state = pending.popleft()
        admitted = sum(phase in (1, 2) for phase in state.phases)
        if protocol == "correct":
            assert state.active == admitted, "admission/exit accounting"
            assert state.active >= 0
            assert not state.frozen or (state.closed and state.active == 0)
        if state.frozen:
            certificates += 1
            fault_certificates += state.faults > 0
        for action, next_state in successors(state, protocol):
            transitions += 1
            writes = next_state.observations + next_state.faults
            if state.frozen and writes != state.observations + state.faults:
                if late_write is None:
                    late_write = _trace(parents, state, action)
                assert protocol != "correct", "write after freeze_result"
            if next_state.active != sum(p in (1, 2) for p in next_state.phases):
                if lost_count is None:
                    lost_count = _trace(parents, state, action)
            assert not state.closed or next_state.closed, "closed cannot reopen"
            if protocol == "correct" and "reject closed" in action:
                assert next_state.observations == state.observations
                assert next_state.faults == state.faults
            if next_state not in parents:
                parents[next_state] = (state, action)
                pending.append(next_state)
    return {
        "callbacks": callbacks, "protocol": protocol,
        "reachable_states": len(parents), "transitions": transitions,
        "frozen_states": certificates, "frozen_states_with_faults": fault_certificates,
        "completeness_failed_states": fault_certificates,
        "completeness_other_states": "unverified",
        "quiescence_invariants_passed": late_write is None and lost_count is None,
        "write_after_freeze_trace": late_write, "lost_active_count_trace": lost_count,
        "stop_certificate": "bounded_model_only" if protocol == "correct" else "invalid",
        "integrity_rule": "faults == 0; nonzero faults fail integrity, not quiescence",
    }


def research_report():
    return {
        "schema": SCHEMA, "runtime": "unverified", "verifier": "unverified",
        "association": "unverified",
        "scope": "bounded abstract state graph; not linearizability or kernel memory-model proof",
        "correct": [explore(count) for count in (1, 2, 3)],
        "counterexamples": [explore(2, protocol) for protocol in PROTOCOLS[1:]],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.parse_args()
    print(json.dumps(research_report(), indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
