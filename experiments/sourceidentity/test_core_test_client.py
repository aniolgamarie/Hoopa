"""Finite proxy/oracle preparation: no real socket, fork, exec, BPF or VM."""
import contextlib
import copy
import errno
import io
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
import threading
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import core_test_client as client

TOKEN = "a" * 32
FRAME = TOKEN.encode() + b"\n"
BOOT = "01234567-89ab-cdef-0123-456789abcdef"
HTTP = b"HTTP/1.1 200 Connection established\r\n\r\n"
SOCKS = b"\x05\x02\x01\x00\x05\x00\x00\x01\x7f\x00\x00\x01\x4a\x33"


def args(mode="rename-after-connect", protocol="http", **kw):
    values = dict(mode=mode, protocol=protocol, allow_loopback=True, generation="7", token=TOKEN,
                  port=18993, target="alpha.test", target_port=18995,
                  scope="/sys/fs/cgroup/hoopa-online-fixed", source_port=0,
                  oracle_output=None, resume_fd=None, resume_oracle=None)
    values.update(kw)
    return SimpleNamespace(**values)


def record(a):
    return dict(schema=client.SCHEMA, mode=a.mode, token=a.token, generation=a.generation,
                pid=72, tgid=72, connect_tid=72, start_ticks="300", pidns_inode="4026531836",
                leader_comm_before="hoopa-before", leader_comm_connect="hoopa-before", leader_comm_after=None,
                name_snapshot="unverified_nonatomic", process_path="unknown", identity_capability="unverified",
                socket_cookie="11", netns_cookie="3", local_endpoint=["127.0.0.1", 31001],
                remote_endpoint=["127.0.0.1", a.port], port=a.port, protocol=a.protocol, target=a.target,
                target_port=a.target_port, scope=a.scope, boot_id=BOOT,
                connect_boot_before_ns="3000000000", connect_boot_after_ns="3100000000",
                caller_comm_connect=dict(raw_hex=(b"hoopa-before"+bytes(4)).hex(), text="hoopa-before", status="unverified"))


class FakeSocket:
    family = socket.AF_INET
    def __init__(self, reply=HTTP+FRAME, fd=77):
        self.reply = reply; self.sent = bytearray(); self.closed = 0; self.fd = fd; self.trace = []
    def settimeout(self, timeout): self.trace.append(("timeout", timeout))
    def setsockopt(self, *values): self.trace.append(("option", values))
    def connect(self, endpoint): self.trace.append(("connect", endpoint, threading.get_native_id()))
    def send(self, data):
        count = min(3, len(data)); self.sent.extend(data[:count]); self.trace.append(("send", bytes(data[:count]), threading.get_native_id())); return count
    def recv(self, size):
        count = min(size, 2, len(self.reply)); part, self.reply = self.reply[:count], self.reply[count:]; return part
    def shutdown(self, how): self.trace.append(("shutdown", how))
    def close(self): self.closed += 1
    def fileno(self): return self.fd
    def getsockopt(self, level, option, size=None):
        if option == socket.SO_TYPE: return socket.SOCK_STREAM
        return (11 if option == 57 else 3).to_bytes(8, sys.byteorder)
    def getsockname(self): return ("127.0.0.1", 31001)
    def getpeername(self): return ("127.0.0.1", 18993)


class HandshakeTests(unittest.TestCase):
    def test_http_and_socks_fragmented_handshake_precedes_exact_frame_and_eof(self):
        for protocol, reply in (("http", HTTP), ("socks5", SOCKS)):
            with self.subTest(protocol=protocol):
                a = args(protocol=protocol); conn = FakeSocket(reply+FRAME)
                previous = client.helper.exchange
                with client.proxy_exchange(a): result = client.helper.exchange(conn, TOKEN)
                self.assertIs(client.helper.exchange, previous)
                self.assertTrue(result["proxy_ready"])
                self.assertEqual(result["echo_bytes"], 33)
                self.assertEqual(result["echo_token"], TOKEN)
                self.assertTrue(bytes(conn.sent).endswith(FRAME))
                self.assertIn(("shutdown", socket.SHUT_WR), conn.trace)
                if protocol == "http": self.assertTrue(bytes(conn.sent).startswith(b"CONNECT alpha.test:18995 HTTP/1.1\r\n"))
                else: self.assertTrue(bytes(conn.sent).startswith(b"\x05\x01\x02\x01\x06client"))
    def test_http_status_headers_bound_and_truncation_reject_before_payload(self):
        replies = [b"HTTP/1.1 407 Auth\r\n\r\n", b"HTTP/1.1 200 OK\r\nBad\r\n\r\n",
                   b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\n",
                   b"HTTP/1.1 200 OK\r\nX: a\r\nx: b\r\n\r\n",
                   b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n",
                   b"HTTP/1.1 200 OK\r\nX: \x00\r\n\r\n", b"HTTP/1.1 200 OK\r\nX: "+b"x"*4096,
                   b"HTTP/1.1 200 OK\r\n"]
        for reply in replies:
            with self.subTest(reply=reply[:50]):
                conn=FakeSocket(reply)
                with self.assertRaises(client.ClientError):
                    with client.proxy_exchange(args()): client.helper.exchange(conn, TOKEN)
                self.assertNotIn(FRAME, bytes(conn.sent))
    def test_socks_auth_connect_codes_types_and_short_frames(self):
        prefix=b"\x05\x02\x01\x00"
        replies=[b"\x05\x00",b"\x05\x02\x01\x01",b"\x05",prefix+b"\x05\x02\x00\x01",
                 prefix+b"\x05\x00\x01\x01",prefix+b"\x05\x00\x00\x09",
                 prefix+b"\x05\x00\x00\x03\x00",prefix+b"\x05\x00\x00\x01\x7f"]
        for reply in replies:
            with self.subTest(reply=reply):
                conn=FakeSocket(reply)
                with self.assertRaises(client.ClientError) as caught:
                    with client.proxy_exchange(args(protocol="socks5")): client.helper.exchange(conn,TOKEN)
                self.assertNotIn(FRAME,bytes(conn.sent))
                self.assertNotIn(client.PASSWORD.decode(),str(caught.exception))
        conn=FakeSocket(b"\x05\x00")
        with self.assertRaises(client.ClientError):client.proxy_handshake(conn,args(protocol="socks5"))
        self.assertEqual(bytes(conn.sent),b"\x05\x01\x02")
    def test_socks_consumes_complete_ipv6_and_domain_bound_addresses(self):
        for address in (b"\x04"+bytes(16)+b"\0\0",b"\x03\x0abound.test\0\0"):
            conn=FakeSocket(b"\x05\x02\x01\x00\x05\x00\x00"+address+FRAME)
            client.proxy_handshake(conn,args(protocol="socks5"))
            self.assertEqual(conn.reply,FRAME)
    def test_cumulative_deadline_and_bad_registration_precede_send(self):
        conn=FakeSocket()
        with patch.object(client.time,"monotonic",side_effect=[0,4]):
            with self.assertRaisesRegex(client.ClientError,"deadline"):client.proxy_handshake(conn,args())
        self.assertEqual(bytes(conn.sent),b"")
        for invalid in ("alpha.test\r\nX:x","Alpha.test","a..test","-bad.test","a"*254):
            conn=FakeSocket()
            with self.assertRaises(client.ClientError):client.proxy_handshake(conn,args(target=invalid))
            self.assertEqual(bytes(conn.sent),b"")


class ModeTests(unittest.TestCase):
    def run_fake(self,a,conn,emit):
        stack=contextlib.ExitStack();self.addCleanup(stack.close)
        stack.enter_context(patch.object(client.socket,"socket",return_value=conn))
        stack.enter_context(patch.object(client,"snapshot",side_effect=lambda c,a,b,*times:record(a)))
        stack.enter_context(patch.object(client.helper,"leader_comm",return_value="hoopa-after"))
        return stack
    def test_rename_order_capture_then_rename_then_proxy_and_cleanup(self):
        a=args();conn=FakeSocket();events=[];stack=self.run_fake(a,conn,events.append);order=[]
        stack.enter_context(patch.object(client,"set_name",side_effect=lambda name:order.append(name)))
        with patch.object(client,"snapshot",side_effect=lambda *v:(order.append("capture") or record(a))):
            result=client.run_connected(a,lambda e:(order.append(e["event"]),events.append(e)))
        self.assertEqual(order,["hoopa-before","capture","connected","hoopa-after","exchanged"])
        self.assertEqual(result["leader_comm_connect"],"hoopa-before")
        self.assertEqual(result["leader_comm_after"],"hoopa-after")
        self.assertEqual(conn.closed,1)
        self.assertEqual([e["event"]for e in events],["connected","exchanged"])
    def test_thread_connect_capture_and_protocol_run_in_native_worker(self):
        a=args(mode="thread");conn=FakeSocket();events=[];self.run_fake(a,conn,events.append)
        main_tid=threading.get_native_id()
        result=client.run_thread(lambda:client.run_connected(a,events.append))
        tids={e[2]for e in conn.trace if e[0]in("connect","send")}
        self.assertEqual(len(tids),1);self.assertNotIn(main_tid,tids)
        self.assertTrue(result["proxy_ready"]);self.assertEqual(conn.closed,1)
    def test_fork_writer_uses_patched_child_exchange_without_parent_payload(self):
        a=args(mode="fork-writer");conn=FakeSocket();events=[];stack=self.run_fake(a,conn,events.append)
        original=client.helper.exchange
        def child(c,a,stored):
            self.assertEqual(bytes(c.sent),b"")
            value=client.helper.exchange(c,a.token);value["writer_pid"]=900;return value
        stack.enter_context(patch.object(client.helper,"fork_exchange",side_effect=child))
        result=client.run_connected(a,events.append)
        self.assertEqual(result["writer_pid"],900)
        self.assertIs(client.helper.exchange,original)
        self.assertEqual(conn.closed,1)
    def test_scm_requires_closed_original_then_receives_and_reports_identity(self):
        a=args(mode="scm-writer");conn=FakeSocket();received=FakeSocket(fd=78);events=[];stack=self.run_fake(a,conn,events.append)
        original=client.helper.exchange
        previous_receive=Mock(return_value=received)
        stack.enter_context(patch.object(client.helper,"receive_socket",previous_receive))
        stat_call=stack.enter_context(patch.object(client.os,"fstat",side_effect=OSError(errno.EBADF,"closed")))
        def child(c,a,stored):
            self.assertEqual(bytes(c.sent),b"")
            actual=client.helper.receive_socket(object(),{})
            result=client.helper.exchange(actual,a.token);result["writer_pid"]=900;return result
        stack.enter_context(patch.object(client.helper,"fork_exchange",side_effect=child))
        result=client.run_connected(a,events.append)
        self.assertTrue(result["scm_inherited_fd_closed_before_receive"])
        self.assertEqual(result["scm_received_socket_cookie"],"11")
        self.assertEqual(result["scm_original_fd"],77);self.assertEqual(result["scm_received_fd"],78)
        stat_call.assert_called_once_with(77);previous_receive.assert_called_once()
        self.assertIs(client.helper.exchange,original);self.assertIs(client.helper.receive_socket,previous_receive)
        self.assertEqual(bytes(conn.sent),b"")
    def test_scm_still_open_or_wrong_errno_never_receives_and_restores_callbacks(self):
        for outcome in (SimpleNamespace(),OSError(errno.EIO,"io")):
            with self.subTest(outcome=outcome):
                previous=client.helper.receive_socket;exchange=client.helper.exchange
                with patch.object(client.os,"fstat",side_effect=outcome if isinstance(outcome,OSError) else None,return_value=outcome):
                    with self.assertRaises(client.ClientError):
                        with client.proxy_exchange(args(mode="scm-writer"),77):client.helper.receive_socket(object(),{})
                self.assertIs(client.helper.receive_socket,previous);self.assertIs(client.helper.exchange,exchange)
    def test_fork_cleanup_failure_keeps_connected_event_and_closes_parent(self):
        a=args(mode="fork-writer");conn=FakeSocket();events=[];stack=self.run_fake(a,conn,events.append)
        original=client.helper.exchange
        stack.enter_context(patch.object(client.helper,"fork_exchange",side_effect=client.ClientError("fork-cleanup","child reap failed")))
        with self.assertRaisesRegex(client.ClientError,"child reap failed"):client.run_connected(a,events.append)
        self.assertEqual([e["event"]for e in events],["connected"])
        self.assertEqual(conn.closed,1);self.assertIs(client.helper.exchange,original)


class ExecOracleTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.a=args(mode="exec-fd",oracle_output=self.temp.name,resume_fd=77,
                    resume_oracle=str(Path(self.temp.name)/"connector.json"))
        self.snapshot=record(self.a)
    def current(self,stack,stat_text="stat"):
        stack.enter_context(patch.object(client.os,"getpid",return_value=72))
        stack.enter_context(patch.object(client.os,"stat",return_value=SimpleNamespace(st_ino=4026531836)))
        stack.enter_context(patch.object(client.helper,"start_ticks",return_value="300"))
        stack.enter_context(patch.object(client.Path,"read_text",autospec=True,side_effect=lambda p:BOOT if str(p).endswith("boot_id") else stat_text))
    def save(self,value=None):
        if value is None:client.write_oracle(self.a,self.snapshot,77)
        else:
            path=Path(self.a.resume_oracle);path.write_text(json.dumps(value));path.chmod(0o600)
    def test_private_exclusive_oracle_roundtrip_then_registration_and_process_validation(self):
        self.save();self.assertEqual(Path(self.a.resume_oracle).stat().st_mode&0o777,0o600)
        with contextlib.ExitStack()as stack:
            self.current(stack)
            self.assertEqual(client.read_oracle(self.a),self.snapshot)
        with self.assertRaises(client.ClientError):client.write_oracle(self.a,self.snapshot,77)
    def test_real_capture_snapshot_fields_survive_private_exec_oracle_roundtrip(self):
        stat_text="72 (hoopa-before) "+" ".join(["S"]+["0"]*18+["300"])
        actual_start_ticks=client.helper.start_ticks
        with contextlib.ExitStack()as stack:
            self.current(stack,stat_text)
            stack.enter_context(patch.object(client.helper,"start_ticks",side_effect=actual_start_ticks))
            # Exercise the unchanged capture function, not a handwritten stand-in.
            stack.enter_context(patch("builtins.open",return_value=io.StringIO(stat_text)))
            stack.enter_context(patch.object(client.helper,"leader_comm",return_value="hoopa-before"))
            stack.enter_context(patch.object(client.threading,"get_native_id",return_value=72))
            stack.enter_context(patch.object(client,"caller_comm",return_value=self.snapshot["caller_comm_connect"]))
            actual=client.snapshot(FakeSocket(),self.a,"hoopa-before",3000000000,3100000000)
            self.assertEqual(set(actual),client.SNAPSHOT_FIELDS)
            self.assertEqual(actual,self.snapshot)
            client.write_oracle(self.a,actual,77)
            self.assertEqual(client.read_oracle(self.a),actual)
    def test_invalid_oracle_does_not_take_or_close_explicit_fd(self):
        original=dict(schema="sourceidentity-core-client-oracle-v1",registration=client.registration(self.a),snapshot=self.snapshot,socket_fd=77)
        mutations=[lambda v:v.update(extra=True),lambda v:v["registration"].update(target="other.test"),
                   lambda v:v["snapshot"].update(pid=73),lambda v:v["snapshot"].update(start_ticks="301"),
                   lambda v:v["snapshot"].update(socket_cookie="0"),lambda v:v["snapshot"].update(leader_comm_connect="after"),
                   lambda v:v.update(socket_fd=88),lambda v:v["snapshot"].update(connect_boot_before_ns="9999999999")]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                value=copy.deepcopy(original);mutate(value);self.save(value)
                with contextlib.ExitStack()as stack:
                    self.current(stack)
                    sock=stack.enter_context(patch.object(client.socket,"socket"));close=stack.enter_context(patch.object(client.os,"close"))
                    with self.assertRaises(client.ClientError):client.run_resume(self.a,Mock())
                    sock.assert_not_called();close.assert_not_called()
    def test_duplicate_json_and_nonprivate_file_rejected_without_socket_operations(self):
        self.save();path=Path(self.a.resume_oracle);raw=path.read_text()
        path.write_text(raw.replace('"socket_fd":77','"socket_fd":77,"socket_fd":88'))
        with self.assertRaises(client.ClientError):client.read_oracle(self.a)
        path.write_text(raw);path.chmod(0o644)
        with self.assertRaises(client.ClientError):client.read_oracle(self.a)
    def test_exec_failure_restores_socket_cloexec_then_outer_cleanup_closes_once(self):
        a=args(mode="exec-fd",oracle_output=self.temp.name);conn=FakeSocket();events=[]
        with contextlib.ExitStack()as stack:
            stack.enter_context(patch.object(client.socket,"socket",return_value=conn))
            stack.enter_context(patch.object(client,"snapshot",return_value=record(a)))
            stack.enter_context(patch.object(client,"set_name"))
            stack.enter_context(patch.object(client.helper,"leader_comm",return_value="hoopa-before"))
            stack.enter_context(patch.object(client,"no_extra_inheritance"))
            stack.enter_context(patch.object(client.os,"get_inheritable",return_value=False))
            flags=stack.enter_context(patch.object(client.os,"set_inheritable"))
            execute=stack.enter_context(patch.object(client.os,"execve",side_effect=OSError(errno.ENOEXEC,"exec failure")))
            with self.assertRaisesRegex(client.ClientError,"exec failure"):client.run_connected(a,events.append)
            self.assertEqual([c.args for c in flags.call_args_list],[(77,True),(77,False)])
            self.assertEqual(conn.closed,1);self.assertEqual([e["event"]for e in events],["connected"])
            executable,argv,env=execute.call_args[0]
            self.assertEqual(executable,sys.executable);self.assertEqual(argv[:4],[sys.executable,"-S","-B",str(Path(client.__file__).resolve())])
            self.assertIn("--resume-fd",argv);self.assertIn("--allow-loopback",argv)
    def test_exec_and_cloexec_restore_failures_both_retained_then_socket_closed(self):
        a=args(mode="exec-fd",oracle_output=self.temp.name);conn=FakeSocket();events=[]
        with contextlib.ExitStack()as stack:
            stack.enter_context(patch.object(client.socket,"socket",return_value=conn))
            stack.enter_context(patch.object(client,"snapshot",return_value=record(a)))
            stack.enter_context(patch.object(client,"set_name"))
            stack.enter_context(patch.object(client.helper,"leader_comm",return_value="hoopa-before"))
            stack.enter_context(patch.object(client,"no_extra_inheritance"))
            stack.enter_context(patch.object(client.os,"get_inheritable",return_value=False))
            flags=stack.enter_context(patch.object(client.os,"set_inheritable",side_effect=[None,OSError(errno.EIO,"restore failed")]))
            stack.enter_context(patch.object(client.os,"execve",side_effect=OSError(errno.ENOEXEC,"exec failed")))
            with self.assertRaises(client.ClientError)as caught:client.run_connected(a,events.append)
            self.assertIn("exec failed",str(caught.exception));self.assertIn("restore failed",str(caught.exception))
            self.assertEqual(flags.call_count,2);self.assertEqual(conn.closed,1)
    def test_resume_success_validates_original_socket_and_closes_owned_fd_once(self):
        conn=FakeSocket();events=[]
        with contextlib.ExitStack()as stack:
            stack.enter_context(patch.object(client,"read_oracle",return_value=self.snapshot))
            stack.enter_context(patch.object(client,"no_extra_inheritance"))
            flags=stack.enter_context(patch.object(client.os,"set_inheritable"))
            stack.enter_context(patch.object(client.socket,"socket",return_value=conn))
            stack.enter_context(patch.object(client.helper,"leader_comm",return_value="python3"))
            stack.enter_context(patch.object(client.helper,"start_ticks",return_value="300"))
            stack.enter_context(patch.object(client.Path,"read_text",return_value="stat"))
            stack.enter_context(patch.object(client.os,"getpid",return_value=72))
            result=client.run_resume(self.a,events.append)
            flags.assert_called_once_with(77,False)
            self.assertTrue(result["resume_socket_identity_matched"])
            self.assertEqual(result["leader_comm_resume"],"python3");self.assertEqual(result["leader_comm_connect"],"hoopa-before")
            self.assertEqual(result["resume_start_ticks"],"300");self.assertEqual(conn.closed,1)
            self.assertEqual([e["event"]for e in events],["exchanged"])
    def test_resume_ownership_errors_close_only_validated_socket_once(self):
        for mode in ("inheritance", "cloexec", "construct", "identity"):
            with self.subTest(mode=mode),contextlib.ExitStack()as stack:
                conn=FakeSocket();stack.enter_context(patch.object(client,"read_oracle",return_value=self.snapshot))
                inherit=stack.enter_context(patch.object(client,"no_extra_inheritance"))
                flags=stack.enter_context(patch.object(client.os,"set_inheritable"))
                factory=stack.enter_context(patch.object(client.socket,"socket",return_value=conn))
                close=stack.enter_context(patch.object(client.os,"close"))
                if mode=="inheritance":inherit.side_effect=client.ClientError("exec-preflight","extra inheritable")
                if mode=="cloexec":flags.side_effect=OSError(errno.EPERM,"flags")
                if mode=="construct":factory.side_effect=OSError(errno.ENOTSOCK,"not socket")
                if mode=="identity":stack.enter_context(patch.object(client.helper,"socket_identity",return_value={}))
                with self.assertRaises((client.ClientError,OSError)):client.run_resume(self.a,Mock())
                if mode=="identity":self.assertEqual(conn.closed,1);close.assert_not_called()
                else:close.assert_called_once_with(77);self.assertEqual(conn.closed,0)
    def test_extra_inheritance_scan_ignores_only_closed_observation_and_never_closes(self):
        with patch.object(client.threading,"active_count",return_value=1), \
             patch.object(client.Path,"iterdir",return_value=iter([Path("/proc/self/task/72")])), \
             patch.object(client.os,"listdir",return_value=["0","1","2","77","88","99"]), \
             patch.object(client.os,"get_inheritable",side_effect=[False,OSError(errno.EBADF,"closed")])as flags, \
             patch.object(client.os,"close")as close:
            client.no_extra_inheritance(77);self.assertEqual([c.args[0]for c in flags.call_args_list],[88,99]);close.assert_not_called()
        with patch.object(client.threading,"active_count",return_value=2):
            with self.assertRaises(client.ClientError):client.no_extra_inheritance(77)


class PreflightTests(unittest.TestCase):
    def test_default_deny_before_inputs_scope_or_socket(self):
        argv=["--mode","thread","--generation","7","--token",TOKEN,"--scope","/sys/fs/cgroup/hoopa-online-fixed",
              "--protocol","http","--port","18993","--target","alpha.test","--target-port","18995"]
        with patch.object(client.socket,"socket")as sock,patch.object(client.Path,"read_text")as read, \
             patch.object(client.Path,"write_text")as write,contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(client.main(argv),2)
            sock.assert_not_called();read.assert_not_called();write.assert_not_called()
    def test_input_validation_precedes_guest_read_and_socket(self):
        for values in (dict(generation="01"),dict(token="A"*32),dict(port=0),dict(target="other/host"),
                       dict(scope="/sys/fs/cgroup/hoopa-online-x/child")):
            with self.subTest(values=values),patch.object(client.Path,"read_text")as read:
                with self.assertRaises(client.ClientError):client.preflight(args(**values))
                read.assert_not_called()


if __name__ == "__main__":unittest.main()
