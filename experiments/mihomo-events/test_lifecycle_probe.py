"""Pure protocol/evidence tests; no sockets, core, or subprocess started."""
import copy
import unittest
from unittest.mock import MagicMock, Mock, patch

import lifecycle_probe as probe


class PartialSocket:
    def __init__(self, reply):
        self.reply = bytearray(reply)
        self.sent = []

    def recv(self, size):
        # Force fragmented replies regardless of requested size.
        block = bytes(self.reply[:min(size, 1)])
        del self.reply[:len(block)]
        return block

    def sendall(self, data):
        self.sent.append(data)


def row(kind, seq, **extra):
    event = dict(schema_version=1, source_instance_id="source-1",
                 source_epoch="epoch-1", event_id=f"event-{seq}",
                 source_seq=str(seq), flow_id="flow-1", attempt_id="attempt-1",
                 observed_at="2026-10-23T00:00:00Z", config_generation="generation-1",
                 boundary_ref="mihomo_proxy_stream_v1", kind=kind)
    event.update(extra)
    return event


class LifecycleProbeTests(unittest.TestCase):
    def test_readiness_requires_forwarding_after_tcp_accepts(self):
        child = Mock()
        child.poll.return_value = None
        connection = MagicMock()
        observations = []
        with patch.object(probe.socket, "create_connection", return_value=connection), \
             patch.object(probe, "exchange", side_effect=[RuntimeError("partial_reply"), None]) as exchange, \
             patch.object(probe.time, "monotonic", return_value=0), \
             patch.object(probe.time, "sleep"):
            probe.wait_proxy_ready(12345, 23456, child, 1, observations)
        self.assertEqual(exchange.call_count, 2)
        self.assertEqual(observations, ["partial_reply", "forwarding_verified"])
        self.assertEqual(connection.__exit__.call_count, 2)

    def test_readiness_deadline_and_child_exit_fail_closed(self):
        child = Mock()
        child.poll.return_value = None
        with patch.object(probe.time, "monotonic", return_value=2), \
             patch.object(probe.socket, "create_connection") as connect:
            with self.assertRaisesRegex(RuntimeError, "^observation_timeout$"):
                probe.wait_proxy_ready(12345, 23456, child, 1, [])
            connect.assert_not_called()
        child.poll.return_value = 1
        with patch.object(probe.time, "monotonic", return_value=0), \
             patch.object(probe.socket, "create_connection") as connect:
            with self.assertRaisesRegex(RuntimeError, "^core_start_failed$"):
                probe.wait_proxy_ready(12345, 23456, child, 1, [])
            connect.assert_not_called()

    def test_failure_diagnostics_only_expose_fixed_reason_labels(self):
        self.assertEqual(probe.failure_details(RuntimeError("observation_timeout")),
                         {"failure_type": "RuntimeError", "failure_reason": "observation_timeout"})
        self.assertEqual(probe.failure_details(RuntimeError("private-token-example"))["failure_reason"], "unclassified")
        self.assertEqual(probe.failure_details(OSError("private-token-example"))["failure_reason"], "unclassified")

    def test_echo_initialization_failure_closes_owned_socket(self):
        sock = Mock()
        sock.bind.side_effect = PermissionError("synthetic")
        with patch.object(probe.socket, "socket", return_value=sock):
            with self.assertRaises(PermissionError):
                probe.Echo()
        sock.close.assert_called_once()

    def test_connect_and_same_packet_preload_partial_response(self):
        for preload in (False, True):
            sock = PartialSocket(b"HTTP/1.1 200 Connection established\r\n\r\nabc")
            probe.exchange(sock, "connect-preload" if preload else "connect", 23456, b"abc")
            self.assertTrue(sock.sent[0].startswith(b"CONNECT alpha.test:23456 "))
            self.assertEqual(sock.sent[0].endswith(b"\r\n\r\nabc"), preload)
            self.assertEqual(len(sock.sent), 1 if preload else 2)

    def test_socks_domain_and_fragmented_bound_address(self):
        for address in (b"\x01\x7f\0\0\x01", b"\x03\x03abc", b"\x04" + b"\0" * 16):
            sock = PartialSocket(b"\x05\0\x05\0\0" + address + b"\0\x50abc")
            probe.exchange(sock, "socks5", 23456, b"abc")
            self.assertEqual(sock.sent[0], b"\x05\x01\0")
            self.assertTrue(sock.sent[1].startswith(b"\x05\x01\0\x03\x0aalpha.test"))

    def test_incomplete_rejected_or_mismatched_replies(self):
        for kind, reply in (("connect", b"HTTP/1.1 403 Denied\r\n\r\n"),
                            ("connect", b"HTTP/1.1 200 OK\r\n"),
                            ("connect", b"HTTP/1.1 200 OK\r\n\r\nab"),
                            ("connect", b"HTTP/1.1 200 OK\r\n\r\nxxx"),
                            ("socks5", b"\x05\x02"),
                            ("socks5", b"\x05\0\x05\x05\0\x01")):
            with self.subTest(kind=kind, reply=reply):
                with self.assertRaises(RuntimeError):
                    probe.exchange(PartialSocket(reply), kind, 23456, b"abc")

    def test_counter_summary_exactness_and_no_additive_final(self):
        amount = 9007199254740993
        events = [row("opened", 1, source_addr="127.0.0.1:12345"),
                  row("bytes_checkpoint", 2, upload_bytes=str(amount), download_bytes=str(amount)),
                  row("closed", 3, upload_bytes=str(amount), download_bytes=str(amount))]
        self.assertTrue(probe.flow_facts(events, "127.0.0.1:12345", amount)["passed"])
        for field, value in (("upload_bytes", str(amount - 1)), ("source_seq", "1"),
                             ("upload_bytes", amount), ("download_bytes", None)):
            changed = copy.deepcopy(events)
            changed[1][field] = value
            self.assertFalse(probe.flow_facts(changed, "127.0.0.1:12345", amount)["passed"])
        self.assertFalse(probe.flow_facts(events[:2], "127.0.0.1:12345", amount)["passed"])

    def test_shutdown_summary_requires_null_tail_and_last_marker(self):
        events = [row("opened", 1), row("gap", 2, reason="source_shutdown_before_final",
                  dimensions=["bytes", "route_or_setup", "window_checkpoints"],
                  upload_bytes=None, download_bytes=None),
                  row("epoch_closed", 3, flow_id=None, attempt_id=None, reason="core_shutdown",
                      unfinished_flows="1", finality="incomplete")]
        self.assertTrue(probe.epoch_facts(events, True)["passed"])
        for change in (events + [row("closed", 4)], events[:-1],
                       [events[0], dict(events[1], upload_bytes="0"), events[2]],
                       events[1:],
                       [events[0], dict(events[1], flow_id="other-flow"), events[2]],
                       [events[0], dict(events[1], source_epoch="other-epoch"), events[2]],
                       [events[0], dict(events[1], source_seq="1"), events[2]],
                       [events[0], events[1], dict(events[2], source_epoch="other-epoch")]):
            self.assertFalse(probe.epoch_facts(change, True)["passed"])


if __name__ == "__main__":
    unittest.main()
