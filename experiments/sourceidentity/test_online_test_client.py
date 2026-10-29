"""Client framing/ownership tests: no real sockets, forks, or BPF operations."""
import array
import contextlib
import io
import json
import os
import signal
import socket
import struct
import threading
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import online_test_client as client

TOKEN = "a" * 32
FRAME = TOKEN.encode() + b"\n"
IDENTITY = dict(socket_cookie="11", netns_cookie="3",
                local_endpoint=["127.0.0.1", 32000], remote_endpoint=["127.0.0.1", 18991])


def args(mode="plain", source_port=0):
    return SimpleNamespace(mode=mode, generation="7", port=18991, token=TOKEN, source_port=source_port)


class MemorySocket:
    family = socket.AF_INET
    def __init__(self, echo=FRAME, chunk=7):
        self.echo, self.chunk = echo, chunk
        self.sent = bytearray()
        self.trace = []
        self.closed = False
        self.cookie, self.netns = 11, 3

    def settimeout(self, value): self.trace.append(("timeout", value))
    def setsockopt(self, *values): self.trace.append(("setsockopt", values))
    def bind(self, value): self.trace.append(("bind", value))
    def connect(self, value): self.trace.append(("connect", value))
    def send(self, data):
        self.trace.append(("send", bytes(data)))
        n = min(len(data), self.chunk); self.sent.extend(data[:n]); return n
    def recv(self, size):
        self.trace.append(("recv", size))
        n = min(size, self.chunk, len(self.echo)); result, self.echo = self.echo[:n], self.echo[n:]; return result
    def shutdown(self, value): self.trace.append(("shutdown", value))
    def close(self): self.closed = True; self.trace.append(("close", None))
    def fileno(self): return 77
    def getsockopt(self, level, option, size=None):
        if option == socket.SO_TYPE: return socket.SOCK_STREAM
        return struct.pack("=Q", self.cookie if option == 57 else self.netns)
    def getsockname(self): return tuple(IDENTITY["local_endpoint"])
    def getpeername(self): return tuple(IDENTITY["remote_endpoint"])


class FramingTests(unittest.TestCase):
    def test_partial_writes_and_reads_require_exact_frame_and_both_eofs(self):
        conn = MemorySocket(chunk=4)
        result = client.exchange(conn, TOKEN)
        self.assertEqual(bytes(conn.sent), FRAME)
        self.assertEqual(result, dict(writer_pid=os.getpid(), echo_bytes=33, echo_token=TOKEN))
        names = [x[0] for x in conn.trace]
        self.assertLess(names.index("send"), names.index("shutdown"))
        self.assertLess(names.index("shutdown"), names.index("recv"))
        self.assertEqual(conn.trace[-1], ("recv", 1))

    def test_truncation_wrong_payload_and_extra_byte_reject(self):
        for echo in (b"", FRAME[:-1], b"b" * 32 + b"\n", FRAME + b"x"):
            with self.subTest(echo=echo), self.assertRaises(client.ClientError):
                client.exchange(MemorySocket(echo=echo), TOKEN)

    def test_timeout_no_eof_and_invalid_send_do_not_succeed(self):
        conn = MemorySocket()
        with patch.object(conn, "recv", side_effect=socket.timeout("EOF missing")):
            with self.assertRaises(socket.timeout): client.exchange(conn, TOKEN)
        for result in (0, -1, 34, True):
            conn = MemorySocket()
            with self.subTest(result=result), patch.object(conn, "send", return_value=result), \
                    self.assertRaises(client.ClientError):
                client.exchange(conn, TOKEN)
            self.assertFalse(any(n == "recv" for n, _ in conn.trace))
        with patch.object(client.time, "monotonic", side_effect=(1, 5)):
            with self.assertRaisesRegex(client.ClientError, "deadline"):
                client.exchange(MemorySocket(), TOKEN)

    def test_proc_field22_uses_last_comm_parenthesis_not_whitespace(self):
        fields = ["S"] + ["0"] * 18 + ["18446744073709551615"] + ["99"]
        raw = "123 (odd ) name (with spaces)) " + " ".join(fields) + "\n"
        self.assertEqual(client.start_ticks(raw, 123), "18446744073709551615")
        for value in (raw.replace("123 (", "124 (", 1), "123 no comm", "123 (x) S 0",
                      raw.replace("18446744073709551615", "18446744073709551616"),
                      raw.replace("18446744073709551615", "-1"), "x" * 4097):
            with self.subTest(value=value), self.assertRaises(client.ClientError):
                client.start_ticks(value, 123)

    def test_capture_keeps_connector_tgid_distinct_from_native_connect_thread(self):
        raw = "123 (leader ) with spaces) " + " ".join(["S"] + ["0"] * 18 + ["4567"])
        with patch("builtins.open", return_value=io.StringIO(raw)), \
                patch.object(client.os, "getpid", return_value=123), \
                patch.object(client.threading, "get_native_id", return_value=124), \
                patch.object(client.os, "stat", return_value=SimpleNamespace(st_ino=4026531836)), \
                patch.object(client, "leader_comm", return_value="leader"):
            result = client.capture(MemorySocket(), args("thread"), "leader-before")
        self.assertEqual((result["pid"], result["tgid"], result["connect_tid"]), (123, 123, 124))
        self.assertEqual(result["start_ticks"], "4567")
        self.assertEqual(result["pidns_inode"], "4026531836")
        self.assertEqual(result["leader_comm_before"], "leader-before")
        self.assertEqual(result["leader_comm_connect"], "leader")
        self.assertIsNone(result["leader_comm_after"])
        self.assertEqual(result["identity_capability"], "unverified")


class InputAndRunTests(unittest.TestCase):
    def argv(self, **changes):
        values = dict(mode="plain", generation="7", port="18991", token=TOKEN, source_port="0")
        values.update(changes)
        return [word for key, value in values.items() for word in ("--" + key.replace("_", "-"), value)]

    def test_opt_in_and_invalid_input_refuse_before_proc_socket_or_fork(self):
        changes = [dict(generation=x) for x in ("0", "01", "-1", "1 ", str(2**64), "9" * 21)]
        changes += [dict(port=x) for x in ("0", "65536")]
        changes += [dict(source_port=x) for x in ("-1", "65536")]
        changes += [dict(token=x) for x in ("A" * 32, "a" * 31, "a" * 32 + "\n")]
        with patch.object(client.socket, "socket") as tcp, patch.object(client.socket, "socketpair") as pair, \
                patch.object(client.os, "fork") as fork, patch.object(client.Path, "read_text") as proc:
            with contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(client.main(self.argv()), 2)
                for change in changes:
                    with self.subTest(change=change):
                        self.assertEqual(client.main(self.argv(**change) + ["--allow-loopback"]), 2)
            tcp.assert_not_called(); pair.assert_not_called(); fork.assert_not_called(); proc.assert_not_called()

    def test_dedicated_guest_marker_is_an_exact_token(self):
        with patch.object(client.Path, "read_text", return_value="console=x hoopa_lab=t132evil"), \
                patch.object(client.socket, "socket") as tcp, contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(client.main(self.argv() + ["--allow-loopback"]), 2)
        tcp.assert_not_called()

    def test_connected_is_emitted_before_payload_and_reuse_options_are_ordered(self):
        conn = MemorySocket(); events = []
        snapshot = dict(IDENTITY, pid=12, tgid=12, connect_tid=34, leader_comm_after=None)
        def emit(event):
            if event["event"] == "connected": self.assertEqual(bytes(conn.sent), b"")
            events.append(event)
        with patch.object(client.socket, "socket", return_value=conn), \
                patch.object(client, "capture", return_value=snapshot), \
                patch.object(client, "leader_comm", return_value="leader"):
            result = client.run_connected(args(source_port=32000), emit)
        self.assertEqual([e["event"] for e in events], ["connected", "exchanged"])
        self.assertEqual(result["pid"], 12)
        self.assertEqual(result["writer_pid"], os.getpid())
        self.assertEqual(result["leader_comm_after"], "leader")
        names = [x[0] for x in conn.trace]
        reuse = ("setsockopt", (socket.SOL_SOCKET, socket.SO_REUSEADDR, 1))
        linger = ("setsockopt", (socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0)))
        self.assertLess(conn.trace.index(reuse), names.index("bind"))
        self.assertLess(names.index("bind"), names.index("connect"))
        self.assertLess(conn.trace.index(linger), names.index("close"))

    def test_later_error_preserves_connected_event_closes_socket_and_reports_stage(self):
        conn = MemorySocket(); output, err = io.StringIO(), io.StringIO()
        with patch.object(client.Path, "read_text", return_value="hoopa_lab=t132"), \
                patch.object(client.socket, "socket", return_value=conn), \
                patch.object(client, "capture", return_value=dict(IDENTITY, pid=12)), \
                patch.object(client, "leader_comm", return_value=None), \
                patch.object(client, "exchange", side_effect=OSError("peer failed")), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(err):
            self.assertEqual(client.main(self.argv() + ["--allow-loopback"]), 1)
        self.assertEqual([json.loads(x)["event"] for x in output.getvalue().splitlines()], ["connected"])
        self.assertIn("plain-exchange: peer failed", err.getvalue())
        self.assertTrue(conn.closed)

    def test_close_attempts_socket_even_if_linger_fails_and_fd_close_never_retries(self):
        conn = MemorySocket()
        with patch.object(conn, "setsockopt", side_effect=OSError("linger failed")):
            with self.assertRaisesRegex(client.ClientError, "linger"):
                client.close_tcp(conn, True)
        self.assertTrue(conn.closed)
        slots = [321]
        with patch.object(client.os, "close", side_effect=InterruptedError("close")) as close:
            with self.assertRaises(InterruptedError): client.close_fd(slots, 0)
            client.close_fd(slots, 0)
        close.assert_called_once_with(321)
        self.assertEqual(slots, [None])

    def test_native_thread_runs_operation_and_propagates_failure_and_join_timeout(self):
        parent_tid = threading.get_native_id()
        self.assertNotEqual(client.run_thread(threading.get_native_id), parent_tid)
        failure = client.ClientError("worker", "failed")
        def fail(): raise failure
        with self.assertRaises(client.ClientError) as caught: client.run_thread(fail)
        self.assertIs(caught.exception, failure)
        fake = Mock(); fake.is_alive.return_value = True
        with patch.object(client.threading, "Thread", return_value=fake) as constructor:
            with self.assertRaisesRegex(client.ClientError, "deadline"):
                client.run_thread(lambda: None)
        self.assertTrue(constructor.call_args.kwargs["daemon"])
        fake.join.assert_called_once_with(8)


class TransferAndChildTests(unittest.TestCase):
    def channel(self, fds, flags=0, payload=b"F"):
        channel = Mock()
        channel.recvmsg.return_value = (payload, [(socket.SOL_SOCKET, socket.SCM_RIGHTS,
                                                  array.array("i", fds).tobytes())], flags, None)
        return channel

    def test_received_fd_uses_cloexec_and_same_actual_socket_identity(self):
        conn, channel = MemorySocket(), self.channel([321])
        with patch.object(client.os, "get_inheritable", return_value=False), \
                patch.object(client.socket, "socket", return_value=conn) as wrap, \
                patch.object(client.os, "close") as close:
            self.assertIs(client.receive_socket(channel, IDENTITY), conn)
        wrap.assert_called_once_with(fileno=321)
        self.assertEqual(channel.recvmsg.call_args.args[2], socket.MSG_CMSG_CLOEXEC)
        close.assert_not_called(); self.assertFalse(conn.closed)

    def test_extra_truncated_or_inheritable_fds_are_closed_without_wrapping(self):
        cases = [([321, 322], 0, b"F", False), ([321], socket.MSG_CTRUNC, b"F", False),
                 ([321], 0, b"X", False), ([321], 0, b"F", True)]
        for fds, flags, payload, inheritable in cases:
            with self.subTest(fds=fds, flags=flags, payload=payload, inheritable=inheritable), \
                    patch.object(client.os, "get_inheritable", return_value=inheritable), \
                    patch.object(client.socket, "socket") as wrap, patch.object(client.os, "close") as close:
                with self.assertRaises(client.ClientError):
                    client.receive_socket(self.channel(fds, flags, payload), IDENTITY)
                self.assertEqual([c.args[0] for c in close.call_args_list], fds)
                wrap.assert_not_called()

    def test_received_socket_mismatch_closes_wrapper_without_closing_fd_twice(self):
        conn = MemorySocket(); conn.cookie = 99
        with patch.object(client.os, "get_inheritable", return_value=False), \
                patch.object(client.socket, "socket", return_value=conn), patch.object(client.os, "close") as close:
            with self.assertRaisesRegex(client.ClientError, "identity mismatch"):
                client.receive_socket(self.channel([321]), IDENTITY)
        self.assertTrue(conn.closed); close.assert_not_called()

    def test_parent_fork_modes_send_no_tcp_payload_and_close_pipe_ends_once(self):
        for mode in ("fork-writer", "scm-writer"):
            conn = MemorySocket(); controls = [Mock(), Mock()]; controls[0].sendmsg.return_value = 1
            def waited(child, timeout): child.owned = False; child.status = 0; return 0
            report = json.dumps(dict(writer_pid=123, echo_bytes=33, echo_token=TOKEN)).encode() + b"\n"
            with self.subTest(mode=mode), patch.object(client.os, "pipe2", return_value=(100, 101)), \
                    patch.object(client.os, "fork", return_value=123), patch.object(client.os, "read", return_value=report), \
                    patch.object(client.os, "close") as close, patch.object(client.os, "kill") as kill, \
                    patch.object(client.OwnedChild, "wait", waited), \
                    patch.object(client.socket, "socketpair", return_value=controls):
                result = client.fork_exchange(conn, args(mode), IDENTITY)
            self.assertEqual(result["writer_pid"], 123)
            self.assertEqual(bytes(conn.sent), b"")
            self.assertEqual([c.args[0] for c in close.call_args_list], [101, 100])
            kill.assert_not_called()
            if mode == "scm-writer":
                cmsg = controls[0].sendmsg.call_args.args[1][0]
                self.assertEqual(cmsg[:2], (socket.SOL_SOCKET, socket.SCM_RIGHTS))
                self.assertEqual(list(array.array("i", cmsg[2])), [77])

    def test_child_scm_closes_inherited_socket_before_receiving_and_reports_privately(self):
        inherited, received = MemorySocket(), MemorySocket()
        channels = [Mock(), Mock()]; writes = []
        class Exited(BaseException): pass
        def receive(channel, expected):
            self.assertTrue(inherited.closed)
            self.assertIs(channel, channels[1]); self.assertEqual(expected, IDENTITY)
            return received
        def write(fd, raw): writes.append((fd, raw)); return len(raw)
        with patch.object(client, "receive_socket", side_effect=receive), \
                patch.object(client.os, "close") as close, patch.object(client.os, "write", side_effect=write), \
                patch.object(client.os, "_exit", side_effect=Exited) as exit:
            with self.assertRaises(Exited):
                client.child_exchange(inherited, args("scm-writer"), IDENTITY, [100, 101], channels)
        self.assertEqual(bytes(inherited.sent), b"")
        self.assertEqual(bytes(received.sent), FRAME)
        self.assertTrue(received.closed)
        self.assertEqual([c.args[0] for c in close.call_args_list], [100, 101])
        self.assertEqual(writes[0][0], 101)
        self.assertEqual(json.loads(writes[0][1])["echo_bytes"], 33)
        exit.assert_called_once_with(0)

    def test_parent_control_failure_kills_and_reaps_only_owned_child(self):
        controls = [Mock(), Mock()]
        controls[0].sendmsg.side_effect = BrokenPipeError("child control closed")
        with patch.object(client.os, "pipe2", return_value=(100, 101)), \
                patch.object(client.os, "fork", return_value=123), \
                patch.object(client.socket, "socketpair", return_value=controls), \
                patch.object(client.os, "close") as close, patch.object(client.os, "kill") as kill, \
                patch.object(client.os, "waitpid", side_effect=[(0, 0), (123, signal.SIGKILL)]) as waited:
            with self.assertRaisesRegex(BrokenPipeError, "child control closed"):
                client.fork_exchange(MemorySocket(), args("scm-writer"), IDENTITY)
        kill.assert_called_once_with(123, signal.SIGKILL)
        self.assertTrue(all(c.args == (123, os.WNOHANG) for c in waited.call_args_list))
        self.assertEqual([c.args[0] for c in close.call_args_list], [101, 100])

    def test_owned_child_only_signals_unreaped_own_pid_and_reaps_after_kill(self):
        child = client.OwnedChild(); child.pid = 123; child.owned = True
        with patch.object(client.os, "waitpid", side_effect=[(0, 0), (123, signal.SIGKILL)]), \
                patch.object(client.os, "kill") as kill:
            child.cleanup(clock=lambda: 0, sleep=lambda delay: None)
        kill.assert_called_once_with(123, signal.SIGKILL)
        self.assertFalse(child.owned)
        with patch.object(client.os, "kill") as kill:
            child.cleanup()
        kill.assert_not_called()
        child = client.OwnedChild(); child.pid = 123; child.owned = True
        with patch.object(client.os, "waitpid", side_effect=ChildProcessError()), \
                patch.object(client.os, "kill") as kill:
            with self.assertRaisesRegex(client.ClientError, "no longer waitable"): child.cleanup()
            child.cleanup()
        kill.assert_not_called()

    def test_owned_child_wait_deadline_and_fork_failure_preserve_cleanup(self):
        child = client.OwnedChild(); child.pid = 123; child.owned = True
        with patch.object(client.os, "waitpid", return_value=(0, 0)):
            with self.assertRaisesRegex(client.ClientError, "deadline"):
                child.wait(1, clock=iter((0, 2)).__next__, sleep=lambda delay: None)
        self.assertTrue(child.owned)
        with patch.object(client.os, "pipe2", return_value=(100, 101)), \
                patch.object(client.os, "fork", side_effect=OSError("fork failed")), \
                patch.object(client.os, "close") as close, patch.object(client.os, "kill") as kill:
            with self.assertRaisesRegex(OSError, "fork failed"):
                client.fork_exchange(MemorySocket(), args("fork-writer"), IDENTITY)
        self.assertEqual([c.args[0] for c in close.call_args_list], [100, 101])
        kill.assert_not_called()


if __name__ == "__main__":
    unittest.main()
