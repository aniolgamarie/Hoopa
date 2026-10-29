"""Executable acceptance checks for the bounded association research model."""
from dataclasses import replace
import json
import unittest

from association_model import (Flow, Witness, candidate_pairs, explore, initial_state,
                               research_report, sample_flows, successors)


class AssociationModelTests(unittest.TestCase):
    def replay(self, flows, trace, **options):
        state = initial_state(flows)
        for action in trace:
            choices = dict(successors(state, flows, **options))
            self.assertIn(action, choices)
            state = choices[action]
        return state

    def finish(self, flows, **options):
        state = initial_state(flows)
        while any(p < 4 for p in state.phases):
            choices = list(successors(state, flows, **options))
            self.assertTrue(choices)
            state = choices[0][1]
        return state

    def test_complete_coverage_all_bounded_graphs(self):
        previous = 0
        for count in (1, 2, 3):
            result = explore(sample_flows(count))
            self.assertEqual(result['wrong_candidate_states'], 0)
            self.assertGreater(result['candidate_states'], 0)
            self.assertGreater(result['reachable_states'], previous)
            self.assertTrue(result['complete_callback_coverage'])
            previous = result['reachable_states']

    def test_reused_tuple_rejects_whole_round_and_unsafe_mode_is_replayable(self):
        flows = sample_flows(2, reuse=True)
        good = self.finish(flows)
        self.assertTrue(good.faults)
        self.assertEqual(candidate_pairs(good, flows), ())
        result = explore(flows, mode='ignore_faults')
        state = self.replay(flows, result['wrong_candidate_trace'], mode='ignore_faults')
        self.assertTrue(state.faults)
        self.assertEqual(candidate_pairs(state, flows, 'ignore_faults')[1][1], flows[0].identity)
        self.assertNotEqual(flows[0].identity, flows[1].identity)
        self.assertIn('flow 0 Q', result['wrong_candidate_trace'][:4])

    def test_omitted_actual_active_callback_is_zero_fault_counterexample(self):
        flows = sample_flows(2, reuse=True)
        result = explore(flows, omitted=((1, 'A'),))
        state = self.replay(flows, result['wrong_candidate_trace'], omitted=((1, 'A'),))
        self.assertFalse(state.faults)
        self.assertFalse(result['complete_callback_coverage'])
        self.assertEqual(candidate_pairs(state, flows)[1][1], flows[0].identity)

    def test_connect_snapshot_does_not_predict_client_port(self):
        flow = sample_flows(1)[0]
        changed_port = replace(flow, client_port=flow.client_port + 7)
        first = dict(successors(initial_state((flow,)), (flow,)))['flow 0 C']
        second = dict(successors(initial_state((changed_port,)), (changed_port,)))['flow 0 C']
        self.assertEqual(first.connects, second.connects)
        self.assertEqual(dict(first.connects)[flow.client_cookie],
                         ((flow.generation, flow.netns, flow.server_port), flow.identity))
        active = dict(successors(first, (changed_port,)))['flow 0 A']
        self.assertIn(changed_port.key, dict(active.index))

    def test_wrong_real_accepted_fd_is_zero_fault_counterexample(self):
        first, second = sample_flows(2)
        flows = (first, replace(second, after=0, witness=first.original_witness))
        result = explore(flows)
        self.assertTrue(result['complete_callback_coverage'])
        self.assertGreater(result['wrong_candidate_states'], 0)
        state = self.replay(flows, result['wrong_candidate_trace'])
        self.assertFalse(state.faults)
        self.assertEqual(candidate_pairs(state, flows)[1][1], first.identity)
        self.assertNotEqual(candidate_pairs(state, flows)[1][1], second.identity)

    def test_witness_coordinates_must_match(self):
        flow = sample_flows(1)[0]
        for field in ('generation', 'netns', 'accepted_cookie'):
            witness = replace(flow.original_witness, **{field: getattr(flow.original_witness, field) + 99})
            state = self.finish((replace(flow, witness=witness),))
            self.assertTrue(state.faults)
            self.assertFalse(candidate_pairs(state, (flow,)))

    def test_namespace_generation_and_process_instances_stay_distinct(self):
        first, second = sample_flows(2)
        for field in ('netns', 'generation'):
            altered = replace(second, client_port=first.client_port,
                              **{field: getattr(first, field) + 1}, witness=None,
                              pid=first.pid, start=first.start + 1)
            flows = (first, altered)
            state = self.finish(flows)
            self.assertFalse(state.faults)
            pairs = candidate_pairs(state, flows)
            self.assertEqual(len(pairs), 2)
            self.assertNotEqual(pairs[0][1], pairs[1][1])

    def test_missing_connect_or_passive_refuses_partial_results(self):
        flows = sample_flows(2)
        for stage in ('C', 'P'):
            state = self.finish(flows, omitted=((1, stage),))
            self.assertTrue(state.faults)
            self.assertFalse(candidate_pairs(state, flows))

    def test_duplicate_cookies_are_insert_only_faults(self):
        first, second = sample_flows(2)
        for field in ('client_cookie', 'accepted_cookie'):
            flows = (first, replace(second, **{field: getattr(first, field)}, witness=None))
            state = self.finish(flows)
            self.assertTrue(state.faults)
            self.assertFalse(candidate_pairs(state, flows))

    def test_failed_connect_only_has_snapshot_and_never_pair(self):
        flows = (replace(sample_flows(1)[0], successful=False),)
        state = self.finish(flows)
        self.assertEqual(len(state.connects), 1)
        self.assertFalse(state.index)
        self.assertFalse(state.bindings)
        self.assertFalse(candidate_pairs(state, flows))

    def test_capacity_boundaries_and_sticky_faults(self):
        flows = sample_flows(2)
        self.assertFalse(self.finish(flows, capacities=(2, 2, 2)).faults)
        for capacities in ((1, 2, 2), (2, 1, 2), (2, 2, 1), (0, 0, 0)):
            state = self.finish(flows, capacities=capacities)
            self.assertTrue(state.faults)
            self.assertFalse(candidate_pairs(state, flows))
        start = initial_state(flows)
        self.assertFalse(candidate_pairs(start, flows))

    def test_close_is_noop_and_no_reclamation_exists(self):
        flows = sample_flows(1)
        state = self.finish(flows)
        self.assertEqual(dict(successors(state, flows))['TCP_CLOSE (no reclamation)'], state)

    def test_validation(self):
        flow = sample_flows(1)[0]
        for field, value in (('generation', 0), ('netns', 0), ('start', 0),
                             ('client_cookie', 0), ('client_port', 65536), ('pid', 0),
                             ('accepted_cookie', flow.client_cookie), ('generation', True),
                             ('netns', 2**64)):
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                initial_state((replace(flow, **{field: value}),))
        with self.assertRaises(ValueError):
            initial_state(())

    def test_report_claims_and_uint64_encoding(self):
        report = json.loads(json.dumps(research_report()))
        for field in ('runtime', 'verifier', 'identity_capability'):
            self.assertEqual(report[field], 'unverified')
        self.assertEqual(report['result_claim'], 'bounded_model_only')
        self.assertEqual(len(report['unsafe_counterexamples']), 3)
        cases = {case['scenario']: case for case in report['unsafe_counterexamples']}
        self.assertEqual(set(cases), {'ignore_sticky_fault', 'reused_tuple_missing_active',
                                     'wrong_original_fd'})
        self.assertTrue(cases['ignore_sticky_fault']['inputs']['reuse'])
        self.assertTrue(cases['reused_tuple_missing_active']['inputs']['reuse'])
        wrong_fd = cases['wrong_original_fd']['inputs']
        self.assertEqual(wrong_fd['witness_flow'], 1)
        self.assertEqual(wrong_fd['witness_source_flow'], 0)
        self.assertEqual(wrong_fd['witness_accepted_cookie'], wrong_fd['flow0_accepted_cookie'])
        self.assertNotEqual(wrong_fd['witness_accepted_cookie'], wrong_fd['flow1_accepted_cookie'])
        self.assertIsInstance(wrong_fd['witness_accepted_cookie'], str)
        self.assertTrue(report['missing_runtime_evidence'])
        self.assertIsInstance(report['example_candidate_pairs'][0]['accepted_cookie'], str)
        self.assertIsInstance(report['example_candidate_pairs'][0]['connector']['start'], str)
        self.assertEqual(len(report['implementation_sha256']), 64)


if __name__ == '__main__':
    unittest.main()
