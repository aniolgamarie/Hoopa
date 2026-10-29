"""Synthetic bundles only: no sockets, cgroups, BPF or native library calls."""
import copy
import json
import struct
import unittest

from calibration import check_bundle, parse_json


def bundle(count=2):
    generation, netns, cgroup, port = 2**63 + 1, 2**63 + 2, 300, 18991
    registration = dict(schema="sourceidentity-calibration-registration-v1",
                        generation=str(generation), netns_cookie=str(netns),
                        cgroup=str(cgroup), port=port,
                        tokens=["%032x" % (i + 1) for i in range(count)])
    raw_config = struct.pack("<QQQII", generation, cgroup, netns, port, 0)
    source = dict(schema="sourceidentity-observer-input-v1", profile="namespaced",
                  config_hex=raw_config.hex())
    maps = dict(connect_records=[], established_records=[], close_records=[],
                capture_faults=[dict(key_hex="00" * 4, value_hex="00" * 8)])
    session = dict(schema="sourceidentity-loader-session-v1", profile="namespaced",
                   status="observed_unverified", errors=[], attachment_observed=True,
                   writer_stop=dict(status="closed_and_drained_observed",
                                    gate=dict(closed=True, active="0", admitted="6",
                                              completed="6", poison=0)),
                   diagnostic_snapshot=maps)
    clients, servers = [], []
    for i, token in enumerate(registration["tokens"]):
        client, server, pid, client_port = 2**63 + 10 + i, 2**63 + 100 + i, 500 + i, 40000 + i
        comm = b"fixture\0".ljust(16, b"\0")
        connect = struct.pack("<6Q8I", generation, 100, client, netns, cgroup,
                              9000000000, 1, pid, pid, 4026531836, pid, 1, 1, 0) + comm * 2
        def record(cookie, raw):
            return dict(key_hex=struct.pack("<Q", cookie).hex(), value_hex=raw.hex(),
                        observation={"this_cache_is_not_authority": True})
        maps["connect_records"].append(record(client, connect))
        for cookie, op, lp, rp, when in ((client, 4, client_port, port, 200),
                                        (server, 5, port, client_port, 300)):
            established = struct.pack("<3Q2I", generation, when, cookie, 2, op)
            established += b"\x7f\0\0\1" * 2 + struct.pack("<IIQ", lp, rp, netns)
            maps["established_records"].append(record(cookie, established))
        shared = dict(schema="sourceidentity-fixture-v1", event="connection", token=token,
                      generation=str(generation), identity_capability="unverified")
        clients.append(dict(shared, role="client", socket_cookie=str(client),
                            netns_cookie=str(netns), local_endpoint="127.0.0.1:%d" % client_port,
                            remote_endpoint="127.0.0.1:%d" % port, pid=pid,
                            start_ticks="900", pidns_inode="4026531836"))
        witness = dict(schema="accepted-socket-witness-v1", generation=str(generation),
                       socket_cookie=str(server), netns_cookie=str(netns),
                       local_endpoint="127.0.0.1:%d" % port,
                       remote_endpoint="127.0.0.1:%d" % client_port,
                       association="unverified", identity_capability="unverified")
        servers.append(dict(shared, role="serve", witness=witness))
    ready = dict(schema="sourceidentity-fixture-v1", event="listening",
                 generation=str(generation), port=port)
    return registration, source, session, clients + [ready] + servers


class CalibrationTests(unittest.TestCase):
    def test_consistent_bundle_keeps_all_runtime_capabilities_unverified(self):
        result = check_bundle(*bundle())
        self.assertEqual(result["status"], "consistent_unverified")
        self.assertEqual(len(result["pairs"]), 2)
        self.assertEqual(result["pairs"][0]["client_cookie"], str(2**63 + 10))
        for field in ("identity_capability", "runtime", "completeness", "association",
                      "scope_isolation", "startup_identity", "preregistration"):
            self.assertEqual(result[field], "unverified")

    def test_missing_active_even_with_zero_fault_rejected(self):
        args = bundle()
        args[2]["diagnostic_snapshot"]["established_records"].pop(0)
        self.assertEqual(check_bundle(*args)["status"], "rejected")

    def test_wrong_real_witness_detected_by_independent_transport_token(self):
        args = bundle()
        args[3][3]["witness"], args[3][4]["witness"] = args[3][4]["witness"], args[3][3]["witness"]
        result = check_bundle(*args)
        self.assertEqual(result["status"], "rejected")
        self.assertIn("transport token", result["errors"][0])

    def test_raw_bytes_are_authority_not_decoded_cache(self):
        args = bundle()
        entry = args[2]["diagnostic_snapshot"]["connect_records"][0]
        raw = bytearray.fromhex(entry["value_hex"])
        struct.pack_into("<I", raw, 52, 1000)
        struct.pack_into("<I", raw, 64, 1000)
        entry["value_hex"] = raw.hex()
        self.assertEqual(check_bundle(*args)["status"], "rejected")

    def test_missing_extra_duplicate_token_and_cookie_are_rejected(self):
        for change in (lambda a: a[3].pop(),
                       lambda a: a[3].append(copy.deepcopy(a[3][0])),
                       lambda a: a[0]["tokens"].append("f" * 32),
                       lambda a: a[3][0].update(token="e" * 32),
                       lambda a: a[3][0].update(socket_cookie=a[3][1]["socket_cookie"])):
            with self.subTest(change=change):
                args = bundle(); change(args)
                self.assertEqual(check_bundle(*args)["status"], "rejected")

    def test_failed_session_fault_mixed_namespace_schema_and_unsafe_gate_rejected(self):
        for change in (lambda a: a[2].update(status="failed"),
                       lambda a: a[2].update(profile="gated"),
                       lambda a: a[2]["writer_stop"]["gate"].update(active="1"),
                       lambda a: a[2]["writer_stop"]["gate"].update(closed=False),
                       lambda a: a[2]["diagnostic_snapshot"]["capture_faults"][0].update(value_hex="01" + "00" * 7),
                       lambda a: a[3][0].update(netns_cookie="77"),
                       lambda a: a[3][3]["witness"].update(schema="other"),
                       lambda a: a[1].update(config_hex="00" * 32)):
            with self.subTest(change=change):
                args = bundle(); change(args)
                self.assertEqual(check_bundle(*args)["status"], "rejected")

    def test_duplicate_tuple_and_map_keys_rejected(self):
        for which in ("tuple", "key"):
            with self.subTest(which=which):
                args = bundle()
                records = args[2]["diagnostic_snapshot"]["established_records"]
                if which == "key":
                    records.append(copy.deepcopy(records[0]))
                else:
                    raw = bytearray.fromhex(records[2]["value_hex"])
                    struct.pack_into("<I", raw, 40, 40000)
                    records[2]["value_hex"] = raw.hex()
                self.assertEqual(check_bundle(*args)["status"], "rejected")

    def test_wrong_pid_pidns_and_noncanonical_numbers_rejected(self):
        for fields in (dict(pid=999), dict(pidns_inode="7"), dict(socket_cookie=2**63 + 10),
                       dict(start_ticks="0"), dict(socket_cookie="01"), dict(pid=True)):
            with self.subTest(fields=fields):
                args = bundle(); args[3][0].update(fields)
                self.assertEqual(check_bundle(*args)["status"], "rejected")

    def test_callback_order_and_unmatched_close_rejected(self):
        args = bundle()
        record = args[2]["diagnostic_snapshot"]["established_records"][1]
        raw = bytearray.fromhex(record["value_hex"]); struct.pack_into("<Q", raw, 8, 50)
        record["value_hex"] = raw.hex()
        self.assertEqual(check_bundle(*args)["status"], "rejected")
        args = bundle()
        raw = struct.pack("<3Q4I", int(args[0]["generation"]), 400, 999, 1, 1, 7, 0)
        args[2]["diagnostic_snapshot"]["close_records"].append(
            dict(key_hex=struct.pack("<Q", 999).hex(), value_hex=raw.hex()))
        self.assertEqual(check_bundle(*args)["status"], "rejected")

    def test_non_connection_events_and_count_bounds(self):
        args = bundle()
        self.assertEqual(check_bundle(*args)["status"], "consistent_unverified")
        args[3].append(dict(event="error"))
        self.assertEqual(check_bundle(*args)["status"], "rejected")
        for count in (0, 17):
            self.assertEqual(check_bundle(*bundle(count))["status"], "rejected")

    def test_listening_event_is_required_once_before_server_records(self):
        for change in (lambda a: a[3].pop(2),
                       lambda a: a[3].append(copy.deepcopy(a[3][2])),
                       lambda a: a[3].append(a[3].pop(2))):
            args = bundle(); change(args)
            self.assertEqual(check_bundle(*args)["status"], "rejected")

    def test_json_duplicate_members_and_nonfinite_values_rejected(self):
        for raw in ('{"generation":"1","generation":"2"}', '{"n":NaN}', '{"n":Infinity}', '{"n":1e309}'):
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                parse_json(raw)
        self.assertEqual(parse_json(json.dumps({"cookie": str(2**64-1)}))["cookie"], str(2**64-1))


if __name__ == "__main__":
    unittest.main()
