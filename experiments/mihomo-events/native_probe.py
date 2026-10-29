#!/usr/bin/env python3
"""Explicit Linux loopback probe for a hash-pinned isolated Mihomo candidate.

Writes raw source events/truth/config only to a new private /tmp directory.
No system proxy, existing core, external target or inherited proxy is used.
The identity assertion is strict; failed/unknown samples stay in its denominator.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import stat
import struct
import subprocess
import sys
import tempfile
import threading
import time


def private_write(path, data):
    with open(path, "x", opener=lambda p, flags: os.open(p, flags, 0o600)) as out:
        out.write(data)
        out.flush()
        os.fsync(out.fileno())


def identity():
    stat = Path("/proc/self/stat").read_text()
    left, right = stat.index("("), stat.rindex(")")
    return {
        "pid": os.getpid(), "name": stat[left + 1:right],
        "start_ticks": stat[right + 1:].split()[19],
        "boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
        "pid_namespace": os.readlink("/proc/self/ns/pid"),
    }


def helper(args):
    Path("/proc/self/comm").write_text("hoopa-probe")
    expected = identity()
    for index in range(args.count):
        payload = ("hoopa:" + args.helper + ":" + str(index)).encode("ascii")
        with socket.create_connection(("127.0.0.1", args.port), timeout=10) as conn:
            local = conn.getsockname()
            truth = dict(expected, sample_id=f"{args.helper}-{index}",
                         source_addr=f"{local[0]}:{local[1]}", host=args.host,
                         upload_bytes=str(len(payload)), download_bytes=str(len(payload)))
            print(json.dumps(truth), flush=True)
            # Parent fsyncs preregistration before allowing the parsed proxy request.
            if sys.stdin.readline() != "go\n":
                raise RuntimeError("preregistration_not_acknowledged")
            request = f"CONNECT {args.host}:{args.target_port} HTTP/1.1\r\nHost: {args.host}:{args.target_port}\r\n\r\n"
            conn.sendall(request.encode("ascii"))
            reply = bytearray()
            while b"\r\n\r\n" not in reply:
                byte = conn.recv(1)
                if not byte or len(reply) > 8192:
                    raise RuntimeError("proxy_response_incomplete")
                reply.extend(byte)
            if b" 200 " not in reply.split(b"\r\n", 1)[0]:
                raise RuntimeError("proxy_connect_rejected")
            conn.sendall(payload)
            actual = bytearray()
            while len(actual) < len(payload):
                block = conn.recv(len(payload) - len(actual))
                if not block:
                    raise RuntimeError("echo_incomplete")
                actual.extend(block)
            if bytes(actual) != payload:
                raise RuntimeError("echo_mismatch")
            conn.shutdown(socket.SHUT_WR)


def read_source(trace):
    events = []
    for path in sorted(trace.glob("epoch-*.frames")):
        raw = path.read_bytes()
        pos, seq = 0, 0
        while pos < len(raw):
            if len(raw) - pos < 4:
                raise RuntimeError("source_incomplete_header")
            size = struct.unpack(">I", raw[pos:pos + 4])[0]
            pos += 4
            if not 0 < size <= 8192 or len(raw) - pos < size + 32:
                raise RuntimeError("source_incomplete_frame")
            body, digest = raw[pos:pos + size], raw[pos + size:pos + size + 32]
            pos += size + 32
            if hashlib.sha256(body).digest() != digest:
                raise RuntimeError("source_checksum_mismatch")
            event = json.loads(body)
            seq += 1
            if event["source_seq"] != str(seq):
                raise RuntimeError("source_sequence_discontinuity")
            events.append(event)
    return events


def free_port():
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 0))
        return reservation.getsockname()[1]


def checked_preflight_observer(args):
    path = getattr(args, "preflight_observer", None)
    digest = getattr(args, "preflight_observer_sha256", None)
    if not path and not digest:
        return None
    # The diagnostic mode holds exactly two sockets, never a formal 100-flow run.
    if not path or not digest or args.count != 2 or args.require_identity:
        raise RuntimeError("preflight_requires_two_diagnostic_samples")
    path = Path(path)
    if not path.is_absolute() or path.is_symlink() or not path.is_file():
        raise RuntimeError("preflight_observer_path_invalid")
    if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise RuntimeError("preflight_observer_hash_mismatch")
    return path


def snapshot_executable(path, digest, destination):
    # Execute only the verified bytes copied into this run's private directory.
    # An open O_NOFOLLOW handle and post-read digest also catch path replacement
    # or in-place changes during the read; the original path is never executed.
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), "rb") as source:
        if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):
            raise RuntimeError("executable_path_invalid")
        data = source.read(128 * 1024 * 1024 + 1)
    if len(data) > 128 * 1024 * 1024 or hashlib.sha256(data).hexdigest() != digest:
        raise RuntimeError("executable_hash_mismatch")
    with open(destination, "xb", opener=lambda p, flags: os.open(p, flags, 0o500)) as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())
    return destination


def validate_observer(value):
    if not isinstance(value, dict) or not isinstance(value.get("report"), dict):
        raise RuntimeError("preflight_report_invalid")
    report = value["report"]
    observer = report.get("observer")
    if not isinstance(observer, dict) or type(value.get("observer_parent_pid")) is not int:
        raise RuntimeError("preflight_report_invalid")
    if report.get("status") not in ("passed", "failed", "unverified") or not isinstance(report.get("reason"), str):
        raise RuntimeError("preflight_report_invalid")
    if not isinstance(report.get("counts"), dict) or not all(type(n) is int and n >= 0 for n in report["counts"].values()):
        raise RuntimeError("preflight_report_invalid")
    for key in ("pid", "uid"):
        if type(observer.get(key)) is not int or observer[key] < (1 if key == "pid" else 0):
            raise RuntimeError("preflight_report_invalid")
    for key in ("start_ticks", "netns", "pidns"):
        if not isinstance(observer.get(key), str) or not observer[key]:
            raise RuntimeError("preflight_report_invalid")


def release_after_preregistration(child, action, on_error):
    try:
        if action:
            action()
        child.stdin.write("go\n")
        child.stdin.flush()
        return True
    except Exception as exc:
        # Any barrier action exception, including malformed observer output,
        # releases the helper through EOF immediately. Exception values stay private.
        on_error(type(exc).__name__)
        try:
            child.stdin.close()
        except OSError:
            pass
        return False


def process_context(pid):
    proc = Path("/proc") / str(pid)
    before = (proc / "stat").read_text()
    status = (proc / "status").read_text().splitlines()
    uid_rows = [line.split()[1:] for line in status if line.startswith("Uid:")]
    if len(uid_rows) != 1 or len(uid_rows[0]) != 4:
        raise RuntimeError("preflight_uid_unavailable")
    fields = before[before.rindex(")") + 1:].split()
    result = {"pid": pid, "parent_pid": int(fields[1]), "start_ticks": fields[19],
              "uid": int(uid_rows[0][1]), "netns": os.readlink(proc / "ns/net"),
              "pidns": os.readlink(proc / "ns/pid")}
    after = (proc / "stat").read_text()
    if after[after.rindex(")") + 1:].split()[19] != result["start_ticks"]:
        raise RuntimeError("preflight_participant_changed")
    return result


def topology_matches(observer, participants, parent_pid):
    if not isinstance(observer, dict) or not isinstance(observer.get("report"), dict):
        return False
    report = observer.get("report", {})
    actual = report.get("observer", {})
    if not isinstance(actual, dict):
        return False
    if observer.get("observer_parent_pid") != parent_pid or not participants:
        return False
    return all(isinstance(p, dict) and p.get("parent_pid") == parent_pid and
               all(key in p and type(p[key]) is type(actual.get(key)) and p[key] == actual.get(key)
                   for key in ("uid", "netns", "pidns"))
               for p in participants)


def run(args):
    observer_path = checked_preflight_observer(args)
    candidate = Path(args.candidate)
    if not candidate.is_absolute() or candidate.is_symlink() or not candidate.is_file():
        raise RuntimeError("candidate_path_invalid")
    actual_sha = hashlib.sha256(candidate.read_bytes()).hexdigest()
    if actual_sha != args.sha256:
        raise RuntimeError("candidate_hash_mismatch")
    if args.count < 1 or args.count > 100:
        raise RuntimeError("count_out_of_probe_scope")
    base = Path(tempfile.mkdtemp(prefix="hoopa-native-probe-"))
    candidate = snapshot_executable(candidate, args.sha256, base / "candidate")
    if observer_path:
        observer_path = snapshot_executable(observer_path, args.preflight_observer_sha256, base / "preflight-observer")
    runtime, trace = base / "runtime", base / "trace"
    runtime.mkdir(mode=0o700)
    trace.mkdir(mode=0o700)
    listener_port = free_port()
    echo = socket.socket()
    echo.bind(("127.0.0.1", 0))
    echo.listen()
    echo.settimeout(0.2)
    target_port = echo.getsockname()[1]
    stopped = threading.Event()
    echo_threads = []

    def echo_connection(conn):
        with conn:
            conn.settimeout(10)
            while True:
                block = conn.recv(16384)
                if not block:
                    return
                conn.sendall(block)

    def echo_loop():
        while not stopped.is_set():
            try:
                conn, _ = echo.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            thread = threading.Thread(target=echo_connection, args=(conn,), daemon=True)
            echo_threads.append(thread)
            thread.start()

    server_thread = threading.Thread(target=echo_loop, daemon=True)
    server_thread.start()
    configuration = f"""mode: rule
log-level: silent
ipv6: false
find-process-mode: off
listeners:
  - name: hoopa-p0
    type: mixed
    listen: 127.0.0.1
    port: {listener_port}
    udp: false
hosts:
  alpha.test: 127.0.0.1
  beta.test: 127.0.0.1
rules:
  - MATCH,DIRECT
"""
    config = runtime / "mihomo.yaml"
    private_write(config, configuration)
    core_log = open(base / "core.log", "xb", opener=lambda p, flags: os.open(p, flags, 0o600))
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "TZ": "UTC",
           "HOOPA_TRACE_DIR": str(trace), "HOOPA_TRACE_INBOUND": "hoopa-p0",
           "HOOPA_TRACE_GENERATION": "probe-generation"}
    core = subprocess.Popen([str(candidate), "-d", str(runtime), "-f", str(config)],
                            env=env, stdin=subprocess.DEVNULL, stdout=core_log, stderr=subprocess.STDOUT)
    helpers = []
    truth = []
    errors = []
    truth_lock = threading.Lock()
    truth_file = open(base / "truth.jsonl", "x", opener=lambda p, flags: os.open(p, flags, 0o600))
    preflight = None

    def observe_held_sources():
        nonlocal preflight
        # Both same-name sources are blocked on stdin with their original sockets
        # established; this callback completes before either proxy request is sent.
        participants = [process_context(core.pid)] + [process_context(row["pid"]) for row in truth]
        result = subprocess.run([str(observer_path)], timeout=12, stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, env={"PATH": env["PATH"], "TZ": "UTC"})
        if result.returncode != 0 or len(result.stdout) > 65536:
            raise RuntimeError("preflight_observer_execution_failed")
        observed = json.loads(result.stdout)
        validate_observer(observed)
        matched = topology_matches(observed, participants, os.getpid())
        preflight = {"scope": "T130_two_held_source_diagnostic", "topology_matched": matched,
                     "participants": participants, "observer": observed,
                     "observer_binary_sha256": args.preflight_observer_sha256,
                     "observer_security_context_equivalence": "unverified_different_executable",
                     "does_not_pass_g01": True}
        private_write(base / "preflight.json", json.dumps(preflight, sort_keys=True, indent=2) + "\n")
        if not matched:
            raise RuntimeError("preflight_topology_mismatch")

    barrier = threading.Barrier(2, action=observe_held_sources, timeout=20) if observer_path else None
    try:
        deadline = time.monotonic() + 15
        while True:
            if core.poll() is not None:
                raise RuntimeError("candidate_start_failed")
            try:
                with socket.create_connection(("127.0.0.1", listener_port), timeout=0.1):
                    break
            except OSError:
                if time.monotonic() > deadline:
                    raise RuntimeError("candidate_listener_timeout")
                time.sleep(0.05)

        def receive_truth(child):
            try:
                for line in child.stdout:
                    row = json.loads(line)
                    with truth_lock:
                        if row.get("name") != "hoopa-probe":
                            errors.append("preregistered_name_mismatch")
                        truth_file.write(json.dumps(row, sort_keys=True) + "\n")
                        truth_file.flush()
                        os.fsync(truth_file.fileno())
                        truth.append(row)
                    if not release_after_preregistration(child, barrier.wait if barrier else None, errors.append):
                        break
            except Exception as exc:
                with truth_lock:
                    errors.append(type(exc).__name__)
                if child.stdin:
                    child.stdin.close()

        # Counts and two same-name process/domain expectations precede launching clients.
        expected_counts = [("a", "alpha.test", (args.count + 1) // 2),
                           ("b", "beta.test", args.count // 2)]
        private_write(base / "preregistered.json", json.dumps({"count": args.count, "clients": expected_counts,
                      "name": "hoopa-probe", "identity_required": args.require_identity}) + "\n")
        readers = []
        for label, host, count in expected_counts:
            if count == 0:
                continue
            error_file = open(base / f"helper-{label}.log", "xb", opener=lambda p, flags: os.open(p, flags, 0o600))
            child = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "--helper", label,
                                     "--port", str(listener_port), "--target-port", str(target_port),
                                     "--host", host, "--count", str(count)], stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, stderr=error_file, text=True)
            error_file.close()
            helpers.append(child)
            reader = threading.Thread(target=receive_truth, args=(child,), daemon=True)
            readers.append(reader)
            reader.start()
        for child in helpers:
            try:
                if child.wait(timeout=90) != 0:
                    errors.append("helper_failed")
            except subprocess.TimeoutExpired:
                errors.append("helper_timeout")
                child.kill()
                child.wait(timeout=5)
        for reader in readers:
            reader.join(timeout=5)
            if reader.is_alive():
                errors.append("truth_reader_timeout")
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            events = read_source(trace)
            if sum(e["kind"] == "closed" for e in events) >= len(truth):
                break
            time.sleep(0.05)
        events = read_source(trace)
        manifests = [json.loads(p.read_text()) for p in trace.glob("*.source.json")]
        source_binding = bool(manifests)
        for m in manifests:
            basis = f'{candidate}|{actual_sha}|{m["boot_id"]}|{core.pid}|{m["start_ticks"]}|{m["nonce"]}'
            source_binding &= (m["binary_sha256"] == actual_sha and m["pid"] == core.pid and
                               m["binary_path"] == str(candidate) and
                               m["source_epoch"] == "epoch:" + hashlib.sha256(basis.encode()).hexdigest())
        opened = [e for e in events if e["kind"] == "opened"]
        closed = {e["flow_id"]: e for e in events if e["kind"] == "closed"}
        truth_by_addr = {r["source_addr"]: r for r in truth}
        identity_correct, terminal_correct, target_correct = 0, 0, 0
        unknown_reasons = {}
        for flow in opened:
            expected = truth_by_addr.get(flow.get("source_addr"))
            if expected is None:
                errors.append("unregistered_flow")
                continue
            process = flow["process"]
            if process.get("status") == "identified":
                if all(process.get(k) == expected[k] for k in ("pid", "name", "start_ticks", "boot_id", "pid_namespace")):
                    identity_correct += 1
            else:
                reason = process.get("reason", "missing_reason")
                unknown_reasons[reason] = unknown_reasons.get(reason, 0) + 1
            target_correct += flow.get("target_kind") == "host" and flow.get("target") == expected["host"]
            final = closed.get(flow["flow_id"])
            terminal_correct += bool(final and final["upload_bytes"] == expected["upload_bytes"] and
                                     final["download_bytes"] == expected["download_bytes"])
        exact_flows = len(truth) == args.count and len(opened) == args.count and len(closed) == args.count
        gaps = sum(e["kind"] == "gap" for e in events)
        transport_passed = exact_flows and terminal_correct == args.count and target_correct == args.count and source_binding and not errors and gaps == 0
        passed = transport_passed and (not args.require_identity or identity_correct == args.count)
        summary = {"schema_version": 1, "scope": "isolated_native_candidate_loopback_probe",
                   "private_directory": str(base), "candidate_sha256": actual_sha,
                   "preregistered": args.count, "truth_records": len(truth), "opened": len(opened),
                   "closed": len(closed), "terminal_exact": terminal_correct, "target_exact": target_correct,
                   "identity_exact": identity_correct, "identity_unknown_reasons": unknown_reasons,
                   "source_identity_binding": source_binding, "transport_passed": transport_passed,
                   "identity_required": args.require_identity, "passed": passed,
                   "errors": sorted(set(errors)), "events": len(events), "gaps": gaps,
                   "source_processes": len({r["pid"] for r in truth}),
                   "source_names": sorted({r["name"] for r in truth}),
                   "target_domains": len({r["host"] for r in truth}),
                   "unverified": ["pid_reuse", "shared_or_transferred_fd", "descendant_matrix", "g02_fault_matrix", "multi_attempt_byte_attribution", "g03_protocol_ready_and_two_routes", "configuration_rejection_at_load"]}
        if preflight:
            summary["preflight"] = {"status": preflight["observer"]["report"]["status"],
                                    "reason": preflight["observer"]["report"]["reason"],
                                    "counts": preflight["observer"]["report"]["counts"],
                                    "topology_matched": preflight["topology_matched"],
                                    "does_not_pass_g01": True}
        private_write(base / "result.json", json.dumps(summary, sort_keys=True, indent=2) + "\n")
        print(json.dumps(summary, sort_keys=True))
        return 0 if passed else 1
    except Exception as exc:
        # Failure still has a durable result; unavailable observations remain null.
        # Exception values and core output stay private rather than entering JSON.
        summary = {"schema_version": 1, "scope": "isolated_native_candidate_loopback_probe",
                   "private_directory": str(base), "candidate_sha256": actual_sha,
                   "preregistered": args.count, "truth_records": len(truth),
                   "opened": None, "closed": None, "terminal_exact": None,
                   "identity_exact": None, "passed": False,
                   "identity_required": args.require_identity,
                   "errors": sorted(set(errors + ["probe_incomplete_" + type(exc).__name__])),
                   "unverified": ["probe_did_not_complete"]}
        private_write(base / "result.json", json.dumps(summary, sort_keys=True, indent=2) + "\n")
        print(json.dumps(summary, sort_keys=True))
        return 1
    finally:
        for child in helpers:
            if child.poll() is None:
                child.kill()
                child.wait(timeout=5)
            if child.stdin:
                child.stdin.close()
            if child.stdout:
                child.stdout.close()
        if core.poll() is None:
            core.terminate()
            try:
                core.wait(timeout=5)
            except subprocess.TimeoutExpired:
                core.kill()
                core.wait(timeout=5)
        truth_file.close()
        core_log.close()
        stopped.set()
        echo.close()
        server_thread.join(timeout=1)
        for thread in echo_threads:
            thread.join(timeout=1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate")
    parser.add_argument("--sha256")
    parser.add_argument("--count", type=int, default=1)
    parser.add_argument("--require-identity", action="store_true")
    parser.add_argument("--preflight-observer")
    parser.add_argument("--preflight-observer-sha256")
    parser.add_argument("--helper")
    parser.add_argument("--host")
    parser.add_argument("--port", type=int)
    parser.add_argument("--target-port", type=int)
    args = parser.parse_args()
    if args.helper:
        helper(args)
        return 0
    return run(args)


if __name__ == "__main__":
    sys.exit(main())
