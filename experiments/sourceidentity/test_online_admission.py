"""No sockets or BPF: verify original-socket and application-I/O ordering."""
import socket
import struct
import unittest

from online_admission import AdmissionOwner


CONFIG = struct.pack("<QQQII", 7, 20, 3, 18991, 0)
LOOPBACK = int.from_bytes(b"\x7f\0\0\1", "little")


class FakeBackend:
    def __init__(self):
        self.owned_links = set()
        self.trace = []
        self.failure = None
        self.binding_changes = {}
        self.stopped = False
        self.closed = False
        self.next_fd = 50

    def open_object(self): self.trace.append("open")
    def load_object(self): self.trace.append("load")
    def verify_loaded(self): self.trace.append("verify"); return {}
    def initialize_config(self, raw): self.trace.append("initialize")
    def freeze_config(self, raw): self.trace.append("freeze")

    def create_link(self, name, fd):
        if self.failure == "create_second" and name == "hoopa_lifecycle":
            raise ValueError("second link create failed")
        self.trace.append("attach:" + name)
        self.next_fd += 1
        self.owned_links.add(self.next_fd)
        return self.next_fd

    def verify_link(self, fd, name, root):
        if fd not in self.owned_links:
            raise ValueError("link missing")
        if self.failure == "verify_second" and name == "hoopa_lifecycle":
            raise ValueError("second link mismatch")
        self.trace.append("verify:" + name)
        return {"cgroup_id": str(root)}

    def online_lookup(self, cookie, raw):
        self.trace.append("lookup:" + cookie)
        if self.failure == "lookup" or self.stopped or raw != CONFIG:
            raise ValueError("backend rejected")
        # An exact backend key query must use this accepted socket, not a
        # caller-provided client cookie or token.
        if cookie != "22":
            raise ValueError("accepted binding missing")
        fields = [7, 3, 22, 11, LOOPBACK, LOOPBACK, 32000, 18991, 1, 0]
        for index, value in self.binding_changes.items(): fields[index] = value
        connect = struct.pack("<6Q8I16s16s", 7, 234000000, 11, 3, 20, 123000000,
                              1, 333, 334, 4026531836, 333, 1, 1, 0,
                              b"worker", b"fixture")
        return {"binding": struct.pack("<QQQQIIIIII", *fields), "connect": connect,
                "gate": struct.pack("<IIQQQII", 0, 0, 0, 6, 6, 0, 0), "faults": bytes(8)}

    def stop_writers(self):
        self.trace.append("stop")
        self.stopped = True
        if self.failure == "stop": raise ValueError("stop failed")
        return {"status": "closed_and_drained_observed"}

    def _lookup(self, name, key): return bytes(8)

    def close_link(self, fd):
        self.trace.append("close:" + str(fd))
        self.owned_links.remove(fd)

    def close_object(self): self.closed = True; self.trace.append("close_object")


class FakeConn:
    family = socket.AF_INET
    def __init__(self):
        self.cookie = 22
        self.netns = 3
        self.peer = ("127.0.0.1", 32000)
        self.local = ("127.0.0.1", 18991)
        self.parts = [b"a" * 32 + b"\n", b""]
        self.reads = self.writes = self.closes = 0
        self.written = bytearray()

    def getsockopt(self, level, option, size=None):
        if option == socket.SO_TYPE: return socket.SOCK_STREAM
        return struct.pack("=Q", self.cookie if option == 57 else self.netns)

    def getsockname(self): return self.local
    def getpeername(self): return self.peer
    def settimeout(self, timeout): pass
    def recv(self, size): self.reads += 1; return self.parts.pop(0)
    def send(self, data):
        self.writes += 1
        count = min(len(data), 7)
        self.written.extend(data[:count])
        return count
    def shutdown(self, how): pass
    def close(self): self.closes += 1


class FakeListener:
    def __init__(self, conn): self.conn = conn
    def accept(self): return self.conn, self.conn.peer


class AdmissionTests(unittest.TestCase):
    def setup_owner(self, cancelled=lambda: False):
        backend = FakeBackend()
        events = []
        owner = AdmissionOwner(backend, CONFIG, 1, events.append, cancelled)
        owner.start(9)
        return owner, backend, events, FakeConn()

    def assert_rejected_without_io(self, record, conn):
        self.assertEqual(record["status"], "rejected")
        self.assertIsNone(record["grant_monotonic_ns"])
        self.assertIsNone(record["identity"])
        self.assertEqual((conn.reads, conn.writes, conn.closes), (0, 0, 1))
        self.assertEqual((record["application_read_bytes"], record["application_write_bytes"]), (0, 0))
        self.assertIsNone(record["first_read_monotonic_ns"])
        self.assertIsNone(record["first_write_monotonic_ns"])

    def test_success_captures_owned_socket_then_grants_before_token_io(self):
        owner, backend, events, conn = self.setup_owner()
        record = owner.serve_one(FakeListener(conn))
        self.assertEqual(record["status"], "exchanged")
        self.assertEqual(record["identity"]["connector"]["tgid_initial"], 333)
        self.assertEqual(record["identity"]["binding"]["accepted_cookie"], "22")
        self.assertEqual(backend.trace.count("lookup:22"), 1)
        self.assertEqual(record["token"], "a" * 32)
        self.assertEqual(bytes(conn.written), b"a" * 32 + b"\n")
        self.assertEqual((record["application_read_bytes"], record["application_write_bytes"]), (33, 33))
        self.assertLessEqual(int(record["grant_monotonic_ns"]), int(record["first_read_monotonic_ns"]))
        self.assertLessEqual(int(record["first_read_monotonic_ns"]), int(record["first_write_monotonic_ns"]))
        self.assertEqual(len(events), 1)
        self.assertEqual(conn.closes, 1)
        self.assertEqual(owner.close()["errors"], [])
        self.assertFalse(backend.owned_links)
        self.assertTrue(backend.closed)

    def test_other_actual_socket_cannot_use_valid_binding(self):
        owner, backend, _, conn = self.setup_owner()
        conn.cookie = 23
        self.assert_rejected_without_io(owner.serve_one(FakeListener(conn)), conn)
        self.assertIn("lookup:23", backend.trace)

    def test_binding_coordinates_reject_before_application_io(self):
        for index, value in ((0, 8), (1, 4), (2, 23), (3, 22), (4, 0), (6, 32001), (7, 18992), (8, 2), (9, 1)):
            with self.subTest(index=index):
                owner, backend, _, conn = self.setup_owner()
                backend.binding_changes[index] = value
                self.assert_rejected_without_io(owner.serve_one(FakeListener(conn)), conn)

    def test_original_namespace_and_endpoint_reject_without_lookup(self):
        for field, value in (("netns", 4), ("local", ("127.0.0.1", 18992)), ("peer", ("127.0.0.2", 32000))):
            with self.subTest(field=field):
                owner, backend, _, conn = self.setup_owner()
                setattr(conn, field, value)
                self.assert_rejected_without_io(owner.serve_one(FakeListener(conn)), conn)
                self.assertFalse(any(item.startswith("lookup:") for item in backend.trace))

    def test_real_boundary_injection_paths_reject_without_application_io(self):
        for inject in ("stop-before-query", "close-connect-before-query", "close-lifecycle-before-query", "wrong-config-before-query"):
            with self.subTest(inject=inject):
                owner, backend, _, conn = self.setup_owner()
                self.assert_rejected_without_io(owner.serve_one(FakeListener(conn), inject), conn)
                self.assertEqual(owner.state, "revoked")
                self.assertEqual(owner.close()["errors"], [])
                self.assertFalse(backend.owned_links)

    def test_backend_error_and_cancel_reject(self):
        for kind in ("lookup", "cancel", "revoked"):
            with self.subTest(kind=kind):
                owner, backend, _, conn = self.setup_owner()
                if kind == "lookup": backend.failure = "lookup"
                elif kind == "cancel": owner.cancelled = lambda: True
                else: owner.revoke()
                self.assert_rejected_without_io(owner.serve_one(FakeListener(conn)), conn)

    def test_partial_start_failure_retains_owned_links_for_cleanup(self):
        backend = FakeBackend()
        backend.failure = "verify_second"
        owner = AdmissionOwner(backend, CONFIG, 1, lambda record: None)
        with self.assertRaisesRegex(ValueError, "second link"):
            owner.start(9)
        self.assertEqual(len(backend.owned_links), 2)
        self.assertEqual(owner.close()["errors"], [])
        self.assertTrue(backend.closed)
        self.assertFalse(backend.owned_links)

    def test_partial_acquisition_failure_closes_first_link_without_admission(self):
        backend = FakeBackend()
        backend.failure = "create_second"
        owner = AdmissionOwner(backend, CONFIG, 1, lambda record: None)
        with self.assertRaisesRegex(ValueError, "second link create"):
            owner.start(9)
        self.assertEqual(owner.state, "new")
        self.assertEqual(len(backend.owned_links), 1)
        self.assertEqual(owner.close()["errors"], [])
        self.assertFalse(backend.owned_links)
        self.assertTrue(backend.closed)

    def test_stop_failure_still_closes_both_links_and_object(self):
        owner, backend, _, _ = self.setup_owner()
        backend.failure = "stop"
        self.assertIn("stop failed", owner.close()["errors"][0])
        self.assertFalse(backend.owned_links)
        self.assertTrue(backend.closed)
        self.assertEqual(owner.state, "closed")


if __name__ == "__main__": unittest.main()
