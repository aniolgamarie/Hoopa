"""Pure Python checks of the bounded stop protocol research model."""
import json
import unittest

from stop_model import explore, initial_state, research_report, successors


class StopModelTests(unittest.TestCase):
    def test_all_bounded_correct_graphs(self):
        previous = 0
        for count in (1, 2, 3):
            with self.subTest(callbacks=count):
                result = explore(count)
                self.assertTrue(result["quiescence_invariants_passed"])
                self.assertIsNone(result["write_after_freeze_trace"])
                self.assertIsNone(result["lost_active_count_trace"])
                self.assertGreater(result["reachable_states"], previous)
                self.assertGreater(result["transitions"], result["reachable_states"])
                self.assertGreater(result["frozen_states_with_faults"], 0)
                self.assertEqual(result["completeness_failed_states"],
                                 result["frozen_states_with_faults"])
                self.assertEqual(result["completeness_other_states"], "unverified")
                self.assertEqual(result["stop_certificate"], "bounded_model_only")
                previous = result["reachable_states"]

    def replay(self, trace, protocol):
        state = initial_state(2)
        for action in trace:
            choices = dict(successors(state, protocol))
            self.assertIn(action, choices)
            state = choices[action]
        return state

    def test_broken_protocols_have_replayable_late_writes(self):
        for protocol in ("split_admission", "stale_map_update"):
            with self.subTest(protocol=protocol):
                result = explore(2, protocol)
                trace = result["write_after_freeze_trace"]
                self.assertTrue(trace)
                before = self.replay(trace[:-1], protocol)
                after = self.replay(trace, protocol)
                self.assertTrue(before.frozen)
                self.assertGreater(after.observations + after.faults,
                                   before.observations + before.faults)
                self.assertEqual(result["stop_certificate"], "invalid")
                self.assertFalse(result["quiescence_invariants_passed"])

    def test_whole_map_update_loses_active_count(self):
        result = explore(2, "stale_map_update")
        state = self.replay(result["lost_active_count_trace"], "stale_map_update")
        self.assertNotEqual(state.active, sum(p in (1, 2) for p in state.phases))
        self.assertIn("stop: write whole map with stale active",
                      result["lost_active_count_trace"])

    def test_no_certificate_before_close_or_while_active(self):
        start = initial_state(1)
        admitted = dict(successors(start))["callback 0: admit under lock"]
        closed = dict(successors(admitted))["stop: close under lock"]
        for state in (start, admitted, closed):
            self.assertNotIn("sample: freeze_result", dict(successors(state)))

    def test_closed_callbacks_never_write(self):
        closed = dict(successors(initial_state(1)))["stop: close under lock"]
        rejected = dict(successors(closed))["callback 0: reject closed (no writes)"]
        self.assertEqual(rejected.active, 0)
        self.assertEqual(rejected.observations + rejected.faults, 0)
        self.assertFalse(any(action.startswith("callback") for action, _ in successors(rejected)))

    def test_report_is_json_and_keeps_runtime_claims_unverified(self):
        report = json.loads(json.dumps(research_report()))
        self.assertEqual(report["schema"], "sourceidentity-stop-model-v1")
        for field in ("runtime", "verifier", "association"):
            self.assertEqual(report[field], "unverified")


if __name__ == "__main__":
    unittest.main()
