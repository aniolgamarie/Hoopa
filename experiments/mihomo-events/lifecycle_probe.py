#!/usr/bin/env python3
"""Opt-in pinned lifecycle subexperiment; never a G01/G02 capability gate."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import tempfile
import threading
import time

from native_probe import free_port, private_write, read_source, snapshot_executable


COUNTER_FIELDS = set("schema_version source_instance_id source_epoch event_id source_seq flow_id attempt_id observed_at config_generation boundary_ref kind upload_bytes download_bytes".split())


def exact(sock, count):
    result = bytearray()
    while len(result) < count:
        block = sock.recv(count - len(result))
        if not block:
            raise RuntimeError("partial_reply")
        result.extend(block)
    return bytes(result)


def exchange(sock, kind, port, payload):
    if kind in ("connect", "connect-preload"):
        request = f"CONNECT alpha.test:{port} HTTP/1.1\r\nHost: alpha.test:{port}\r\n\r\n".encode()
        sock.sendall(request + (payload if kind == "connect-preload" else b""))
        header = bytearray()
        while not header.endswith(b"\r\n\r\n"):
            if len(header) >= 8192:
                raise RuntimeError("response_limit")
            header.extend(exact(sock, 1))
        status = header.split(b"\r\n", 1)[0].split()
        if len(status) < 2 or status[0] not in (b"HTTP/1.0", b"HTTP/1.1") or status[1] != b"200":
            raise RuntimeError("connect_rejected")
        if kind == "connect":
            sock.sendall(payload)
    elif kind == "socks5":
        sock.sendall(b"\x05\x01\0")
        if exact(sock, 2) != b"\x05\0":
            raise RuntimeError("socks_auth_rejected")
        host = b"alpha.test"
        sock.sendall(b"\x05\x01\0\x03" + bytes([len(host)]) + host + struct.pack(">H", port))
        prefix = exact(sock, 4)
        if prefix[:3] != b"\x05\0\0":
            raise RuntimeError("socks_connect_rejected")
        if prefix[3] == 1:
            exact(sock, 4)
        elif prefix[3] == 4:
            exact(sock, 16)
        elif prefix[3] == 3:
            exact(sock, exact(sock, 1)[0])
        else:
            raise RuntimeError("socks_address_invalid")
        exact(sock, 2)
        sock.sendall(payload)
    else:
        raise RuntimeError("protocol_out_of_scope")
    if exact(sock, len(payload)) != payload:
        raise RuntimeError("echo_mismatch")


def number(value):
    if not isinstance(value, str) or not value or not value.isascii() or not value.isdecimal() or (len(value) > 1 and value[0] == "0"):
        return None
    n = int(value)
    return n if n < 2**64 else None


def flow_facts(events, address, amount):
    opened = [e for e in events if e.get("kind") == "opened" and e.get("source_addr") == address]
    selected = [e for e in events if opened and e.get("flow_id") == opened[0]["flow_id"] and e.get("source_epoch") == opened[0]["source_epoch"]]
    checkpoints = [e for e in selected if e.get("kind") == "bytes_checkpoint"]
    finals = [e for e in selected if e.get("kind") == "closed"]
    exact_counters = bool(checkpoints) and len(finals) == 1
    for event in checkpoints + finals:
        exact_counters &= set(event) == COUNTER_FIELDS and number(event.get("upload_bytes")) == amount and number(event.get("download_bytes")) == amount
    ordered = len(opened) == 1 and number(opened[0].get("source_seq")) is not None and bool(checkpoints) and len(finals) == 1 and all(
        number(e.get("source_seq")) is not None and number(finals[0].get("source_seq")) is not None and
        number(opened[0]["source_seq"]) < number(e["source_seq"]) < number(finals[0]["source_seq"]) for e in checkpoints)
    passed = len(opened) == 1 and exact_counters and ordered and not any(e.get("kind") == "gap" for e in selected)
    return {"passed": bool(passed), "opened": len(opened), "checkpoints": len(checkpoints), "closed": len(finals), "exact_cumulative": bool(exact_counters), "checkpoint_before_final": bool(ordered)}


def epoch_facts(events, incomplete):
    markers = [e for e in events if e.get("kind") == "epoch_closed"]
    gaps = [e for e in events if e.get("kind") == "gap"]
    valid = len(markers) == 1 and events[-1] == markers[0]
    if markers:
        marker = markers[0]
        valid &= marker.get("flow_id") is None and marker.get("attempt_id") is None and marker.get("reason") == "core_shutdown"
        valid &= marker.get("finality") == ("incomplete" if incomplete else "complete")
        valid &= number(marker.get("unfinished_flows")) == (1 if incomplete else 0)
    if incomplete:
        opened = [e for e in events if e.get("kind") == "opened"]
        valid &= len(opened) == 1 and len(gaps) == 1 and not any(e.get("kind") == "closed" for e in events)
        if len(opened) == len(gaps) == len(markers) == 1:
            origin, gap, marker = opened[0], gaps[0], markers[0]
            valid &= gap.get("flow_id") == origin.get("flow_id") and bool(origin.get("flow_id"))
            valid &= all(e.get(key) == origin.get(key) for e in (gap, marker)
                         for key in ("source_epoch", "source_instance_id", "config_generation"))
            positions = [number(e.get("source_seq")) for e in (origin, gap, marker)]
            valid &= all(pos is not None for pos in positions) and 0 < positions[0] < positions[1] < positions[2]
            gap = gaps[0]
            valid &= gap.get("reason") == "source_shutdown_before_final" and gap.get("upload_bytes") is None and gap.get("download_bytes") is None
            valid &= set(gap.get("dimensions", [])) == {"bytes", "route_or_setup", "window_checkpoints"}
    else:
        valid &= not gaps
    return {"passed": bool(valid), "epoch_markers": len(markers), "gaps": len(gaps)}


def events_now(trace):
    try:
        return read_source(trace)
    except RuntimeError as exc:
        if str(exc) in ("source_incomplete_header", "source_incomplete_frame"):
            return []  # A live writer may be between frame writes; deadline remains fixed.
        raise


def wait_for(predicate, deadline):
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.03)
    raise RuntimeError("observation_timeout")


class Echo:
    def __init__(self):
        self.listener = socket.socket()
        try:
            self.listener.bind(("127.0.0.1", 0))
            self.listener.listen(8)
            self.listener.settimeout(0.1)
        except Exception:
            self.listener.close()
            raise
        self.port = self.listener.getsockname()[1]
        self.stop = threading.Event()
        self.clients = []
        self.threads = []
        self.thread = threading.Thread(target=self.serve, daemon=True)
        try:
            self.thread.start()
        except Exception:
            self.listener.close()
            raise

    def serve(self):
        while not self.stop.is_set():
            try:
                conn, _ = self.listener.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            self.clients.append(conn)
            thread = threading.Thread(target=self.echo, args=(conn,), daemon=True)
            self.threads.append(thread)
            thread.start()

    def echo(self, conn):
        try:
            with conn:
                conn.settimeout(2)
                while not self.stop.is_set():
                    try:
                        block = conn.recv(16384)
                    except socket.timeout:
                        continue
                    if not block:
                        return
                    conn.sendall(block)
        except OSError:
            return

    def close(self):
        self.stop.set()
        self.listener.close()
        self.thread.join(timeout=1)
        for conn in self.clients:
            try:
                conn.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            conn.close()
        for thread in self.threads:
            thread.join(timeout=1)
        return not self.thread.is_alive() and all(not t.is_alive() for t in self.threads)


def manifest_matches(trace, binary, digest, pids):
    manifests = [json.loads(p.read_text()) for p in trace.glob("*.source.json")]
    if not manifests:
        return False
    for m in manifests:
        if m.get("pid") not in pids:
            return False
        basis = f'{binary}|{digest}|{m["boot_id"]}|{m["pid"]}|{m["start_ticks"]}|{m["nonce"]}'
        if m.get("binary_path") != str(binary) or m.get("binary_sha256") != digest or m.get("source_epoch") != "epoch:" + hashlib.sha256(basis.encode()).hexdigest():
            return False
    return True



# Only fixed protocol/stage labels may escape an exception. Raw network/parser
# errors can contain private input and stay out of even the public summary.
_FAILURE_REASONS = frozenset({"observation_timeout", "core_start_failed", "partial_reply",
    "connect_rejected", "response_limit", "socks_auth_rejected", "socks_connect_rejected",
    "socks_address_invalid", "protocol_out_of_scope", "echo_mismatch", "unexpected_echo_tail"})


def failure_details(exc):
    reason = str(exc) if isinstance(exc, RuntimeError) and str(exc) in _FAILURE_REASONS else "unclassified"
    return {"failure_type": type(exc).__name__, "failure_reason": reason}


def wait_proxy_ready(port, target_port, child, deadline, observations):
    """Unmeasured, separately named inbound must forward, not just accept TCP.

    ApplyConfig opens listeners before OnRunning. These health attempts never
    enter the measured trace inbound or consume its expected flow count.
    """
    def ready():
        if child.poll() is not None:
            raise RuntimeError("core_start_failed")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2) as conn:
                exchange(conn, "connect", target_port, b"hoopa-ready")
        except OSError:
            observations.append("transport_unavailable")
            return False
        except RuntimeError as exc:
            # Protocol failures are observable preflight failures, never a
            # successful measured sample. Unexpected parser errors fail closed.
            if str(exc) not in _FAILURE_REASONS:
                raise
            observations.append(failure_details(exc)["failure_reason"])
            return False
        observations.append("forwarding_verified")
        return True
    wait_for(ready, deadline)


def scenario(base, binary, digest, mode, echo):
    directory = base / mode
    directory.mkdir(mode=0o700)
    runtime, trace = directory / "runtime", directory / "trace"
    runtime.mkdir(mode=0o700)
    trace.mkdir(mode=0o700)
    port = free_port()
    health_port = free_port()
    while health_port == port:
        health_port = free_port()
    config = runtime / "mihomo.yaml"
    private_write(config, f"""mode: rule
log-level: silent
ipv6: false
find-process-mode: off
listeners:
  - name: hoopa-lifecycle
    type: mixed
    listen: 127.0.0.1
    port: {port}
    udp: false
  - name: hoopa-lifecycle-readiness
    type: mixed
    listen: 127.0.0.1
    port: {health_port}
    udp: false
hosts:
  alpha.test: 127.0.0.1
rules:
  - MATCH,DIRECT
""")
    protocols = ["connect", "connect-preload", "socks5"] if mode == "normal" else ["connect"]
    truth = {"protocols": protocols, "upload_bytes": "257", "download_bytes": "257", "restart_requests": 1 if mode == "crash-restart" else 0, "identity_asserted": False,
             "startup_preflight": "separate_untraced_inbound_forwards_CONNECT_echo_before_measured_flow"}
    private_write(directory / "preregistered.json", json.dumps(truth) + "\n")
    env = {"PATH": "/usr/bin:/bin", "TZ": "UTC", "HOOPA_TRACE_DIR": str(trace), "HOOPA_TRACE_INBOUND": "hoopa-lifecycle", "HOOPA_TRACE_GENERATION": "lifecycle-generation"}
    cores, logs, clients = [], [], []
    deadline = time.monotonic() + 12
    facts = {"passed": False, "cleanup_passed": False}
    stage = "prepared"
    preflights = []

    def start():
        nonlocal stage
        stage = "core_start"
        log = open(directory / f"core-{len(cores)}.log", "xb", opener=lambda p, flags: os.open(p, flags, 0o600))
        logs.append(log)
        child = subprocess.Popen([str(binary), "-d", str(runtime), "-f", str(config)], env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        cores.append(child)

        stage = "core_forwarding_preflight"
        observations = []
        preflights.append(observations)
        wait_proxy_ready(health_port, echo.port, child, min(deadline, time.monotonic() + 4), observations)
        stage = "core_forwarding_ready"
        return child

    def finish(child, kill=False):
        nonlocal stage
        stage = "core_sigkill" if kill else "core_sigterm"
        if kill:
            child.kill()
        else:
            child.terminate()
        child.wait(timeout=max(0.1, min(3, deadline - time.monotonic())))

    def connect(kind):
        nonlocal stage
        stage = "client_connect"
        conn = socket.create_connection(("127.0.0.1", port), timeout=2)
        clients.append(conn)
        address = "%s:%s" % conn.getsockname()
        stage = "protocol_exchange"
        exchange(conn, kind, echo.port, bytes(range(256)) + b"!")
        stage = "checkpoint_wait"
        wait_for(lambda: any(e.get("kind") == "bytes_checkpoint" and any(o.get("kind") == "opened" and o.get("source_addr") == address and o.get("flow_id") == e.get("flow_id") for o in events_now(trace)) for e in events_now(trace)), deadline)
        stage = "checkpoint_observed"
        return conn, address

    def close_flow(conn, address):
        nonlocal stage
        stage = "flow_close"
        conn.shutdown(socket.SHUT_WR)
        if conn.recv(1):
            raise RuntimeError("unexpected_echo_tail")
        conn.close()
        wait_for(lambda: flow_facts(events_now(trace), address, 257)["closed"] == 1, deadline)

    try:
        core = start()
        addresses = []
        for protocol in protocols:
            conn, address = connect(protocol)
            addresses.append(address)
            if mode == "normal":
                close_flow(conn, address)
        if mode == "normal":
            finish(core)
            events = read_source(trace)
            flow_results = [flow_facts(events, addr, 257) for addr in addresses]
            facts = {"flows": flow_results, "epoch": epoch_facts(events, False), "passed": len([e for e in events if e.get("kind") == "opened"]) == 3 and all(f["passed"] for f in flow_results) and epoch_facts(events, False)["passed"]}
        elif mode == "active-shutdown":
            finish(core)
            events = read_source(trace)
            facts = epoch_facts(events, True)
        else:
            finish(core, kill=True)
            old_files = {p.name: p.read_bytes() for p in trace.glob("*.frames")}
            before = read_source(trace)
            old_epochs = {e["source_epoch"] for e in before}
            clients[-1].close()
            core = start()
            restart_conn, restart_addr = connect("connect")
            close_flow(restart_conn, restart_addr)
            finish(core)
            events = read_source(trace)
            unchanged = all((trace / name).read_bytes() == raw for name, raw in old_files.items())
            old_events = [e for e in events if e["source_epoch"] in old_epochs]
            pending = len([e for e in old_events if e["kind"] == "opened"]) == 1 and not any(e["kind"] in ("closed", "epoch_closed") for e in old_events)
            new_events = [e for e in events if e["source_epoch"] not in old_epochs]
            facts = {"old_frames_unchanged": unchanged, "old_event_ids_sequences_unchanged": old_events == before, "old_opened_without_final": pending, "reader_pending_assertion_executed": False, "core_recovery_exercised": bool(new_events), "restart_flow": flow_facts(new_events, restart_addr, 257), "new_epoch": epoch_facts(new_events, False)}
            facts["passed"] = unchanged and old_events == before and pending and facts["core_recovery_exercised"] and facts["restart_flow"]["passed"] and facts["new_epoch"]["passed"]
        facts["manifest_binding"] = manifest_matches(trace, binary, digest, {p.pid for p in cores})
        facts["passed"] &= facts["manifest_binding"]
        facts["events"] = len(events)
        private_write(directory / "events.private.json", json.dumps(events, sort_keys=True, indent=2) + "\n")
    except Exception as exc:
        facts = {"passed": False, "failure_stage": stage, **failure_details(exc)}
    finally:
        for conn in clients:
            conn.close()
        for child in cores:
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=1)
        for log in logs:
            log.close()
        facts["cleanup_passed"] = all(p.poll() is not None for p in cores)
        facts["startup_preflight"] = preflights
        facts["owned_children"] = len(cores)
        facts["passed"] &= facts["cleanup_passed"]
        private_write(directory / "summary.private.json", json.dumps(facts, sort_keys=True, indent=2) + "\n")
    return facts


def run(args):
    candidate = Path(args.candidate)
    if not args.allow_loopback or not candidate.is_absolute() or candidate.is_symlink() or not candidate.is_file() or len(args.sha256) != 64 or any(c not in "0123456789abcdef" for c in args.sha256):
        raise RuntimeError("explicit_pin_and_loopback_required")
    base = Path(tempfile.mkdtemp(prefix="hoopa-lifecycle-"))
    binary = snapshot_executable(candidate, args.sha256, base / "candidate")
    results = {}
    echo = None
    try:
        echo = Echo()
        for mode in ("normal", "active-shutdown", "crash-restart"):
            results[mode] = scenario(base, binary, args.sha256, mode, echo)
            if not results[mode]["passed"]:
                break  # Preserve the first failure; further scenarios need a decision.
    except Exception:
        pass  # The missing scenarios remain failed/unexecuted; no raw error escapes.
    finally:
        echo_clean = echo.close() if echo else True
        passed = len(results) == 3 and all(r["passed"] for r in results.values()) and echo_clean
        summary = {"passed": passed, "private_directory": str(base), "candidate_sha256": args.sha256, "scenarios_completed": len(results), "echo_cleanup_passed": echo_clean, "scenarios": results, "unverified": ["G01", "G02_full_matrix", "partial_write", "lazy_handshake", "production_recovery", "source_completeness"]}
        private_write(base / "summary.private.json", json.dumps(summary, sort_keys=True, indent=2) + "\n")
        print(json.dumps({"passed": passed, "private_directory": str(base), "candidate_sha256": args.sha256, "scenarios_completed": len(results), "cleanup_passed": echo_clean and all(r["cleanup_passed"] for r in results.values())}, sort_keys=True))
    return 0 if passed else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate", required=True)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--allow-loopback", action="store_true")
    args = parser.parse_args()
    try:
        return run(args)
    except Exception:
        print(json.dumps({"passed": False}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
