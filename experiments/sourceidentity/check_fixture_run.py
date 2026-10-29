#!/usr/bin/env python3
"""Offline material consistency only; no runtime or scope certification."""
import argparse
import hashlib
import json
import math
import os
import re
import stat
import sys

from calibration import check_bundle, parse_json, require

METADATA_LIMIT = 4 * 1024 * 1024
LABELS = ("server", "client1", "client2")
ARTIFACTS = {"registration": "registration.json", "observer_input": "observer-input.json",
             "attached": "attached.json", "fixture": "fixture"}
EVENTS = ("preflight_complete", "server_started", "listening_observed",
          "client1_started", "client2_started", "children_completed")
CLAIMS = {"placement_before_socket", "attachment_continuity", "no_preexisting_sockets",
          "initial_pid_namespace", "preregistration_before_traffic"}


def number(value):
    require(isinstance(value, str) and re.fullmatch(r"[1-9][0-9]{0,19}", value),
            "noncanonical uint64 string")
    require(int(value) < 2**64, "uint64 overflow")
    return int(value)


def positive_pid(value):
    require(type(value) is int and value > 0, "invalid owned PID")
    return value


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def read_file(name, limit, directory=None):
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
    with os.fdopen(fd, "rb") as stream:
        require(stat.S_ISREG(os.fstat(stream.fileno()).st_mode), "regular input file required")
        raw = stream.read(limit + 1)
    require(len(raw) <= limit, "input exceeds size limit: " + name)
    return raw


def document(raw):
    return parse_json(raw.decode("utf-8"))


def bound_file(directory, metadata, expected, limit):
    require(isinstance(metadata, dict) and set(metadata) == {"name", "sha256", "bytes"},
            "invalid artifact metadata")
    require(metadata["name"] == expected, "unsafe or unexpected artifact name")
    require(type(metadata["bytes"]) is int and 0 <= metadata["bytes"] <= limit,
            "invalid artifact size")
    raw = read_file(expected, limit, directory)
    require(len(raw) == metadata["bytes"] and digest(raw) == metadata["sha256"],
            "artifact digest/size mismatch: " + expected)
    return raw


def _check(directory, fixture_path, run_raw, run, observer_raw, scope_raw):
    require(run["schema"] == "sourceidentity-fixture-run-v1"
            and run["status"] == "completed_unverified" and run["errors"] == [],
            "unsuccessful fixture run")
    for field in ("identity_capability", "association", "completeness", "scope_isolation"):
        require(run[field] == "unverified", "invalid receipt capability")
    generation = run["generation"]
    number(generation)
    controller = positive_pid(run["controller_pid"])
    require(isinstance(run["boot_id"], str) and bool(run["boot_id"]), "missing boot ID")
    number(run["pidns_inode"])
    number(run["timens_inode"])
    started, finished, deadline = (number(run[k]) for k in
        ("started_monotonic_ns", "finished_monotonic_ns", "deadline_monotonic_ns"))
    require(started <= finished < deadline, "run outside marker deadline")
    require(isinstance(run["artifacts"], dict) and set(run["artifacts"]) == set(ARTIFACTS),
            "artifact set mismatch")
    raws = {key: bound_file(directory, run["artifacts"][key], name,
             64 * 1024 * 1024 if key == "fixture" else METADATA_LIMIT)
            for key, name in ARTIFACTS.items()}
    registration, source, attached = (document(raws[k]) for k in
                                      ("registration", "observer_input", "attached"))
    require(registration["generation"] == generation, "registration generation mismatch")
    require(source["profile"] == "namespaced", "wrong observer profile")
    require(source["boot_id"] == attached["boot_id"] == run["boot_id"], "boot ID mismatch")
    require(source["observer_pidns_inode"] == run["pidns_inode"]
            and source["observer_timens_inode"] == run["timens_inode"], "clock/PID namespace mismatch")
    require(attached["event"] == "attached_observer" and attached["generation"] == generation,
            "invalid attached marker")
    require(attached["input_sha256"] == digest(raws["observer_input"]), "marker input hash mismatch")
    duration = attached["duration_seconds"]
    require(type(duration) in (int, float) and math.isfinite(duration) and 0 < duration <= 60,
            "invalid marker duration")
    source_duration = source["duration_seconds"]
    require(type(source_duration) in (int, float) and math.isfinite(source_duration)
            and source_duration == duration, "observer input/marker duration mismatch")
    require(source["cgroup"]["inode"] == registration["cgroup"],
            "observer input/registration cgroup inode mismatch")
    marker = number(attached["observed_monotonic_ns"])
    require(deadline == marker + int(duration * 1_000_000_000) and marker <= started
            and deadline - started >= 14_000_000_000,
            "marker deadline/start mismatch")
    tokens = registration["tokens"]
    require(isinstance(tokens, list) and len(tokens) == 2, "exactly two tokens required")
    require(isinstance(run["children"], dict) and set(run["children"]) == set(LABELS),
            "child set mismatch")
    events = run["events"]
    require(isinstance(events, list) and len(events) == len(EVENTS), "event set mismatch")
    event_times = []
    for item, name in zip(events, EVENTS):
        require(set(item) == {"event", "observed_monotonic_ns"} and item["event"] == name,
                "event sequence mismatch")
        event_times.append(number(item["observed_monotonic_ns"]))
    require(event_times == sorted(event_times)
            and started <= event_times[0] <= event_times[-1] <= finished, "event time order mismatch")
    fixtures, pids, exits = [], [], []
    for index, label in enumerate(LABELS):
        child = run["children"][label]
        require(child["label"] == label, "child label mismatch")
        pids.append(positive_pid(child["pid"]))
        require(type(child["exit_code"]) is int and child["exit_code"] == 0,
                "unsuccessful child exit")
        require(child["stdout_eof"] is True and child["stderr_eof"] is True
                and child["truncated"] is False, "incomplete child transport")
        child_start = number(child["started_monotonic_ns"])
        observed_exit = number(child["observed_exit_monotonic_ns"])
        require(started <= child_start <= observed_exit <= finished and observed_exit < deadline,
                "child outside run deadline")
        start_event = event_times[1 if index == 0 else index + 2]
        require(child_start <= start_event, "child start event precedes spawn")
        require(deadline - start_event >= 12_000_000_000,
                "insufficient execution/cleanup reserve after spawn")
        if index:
            require(child_start >= event_times[2], "client started before listening")
        if index == 0:
            require(deadline - child_start >= 14_000_000_000, "insufficient reserve at server spawn")
        if index == 2:
            require(child_start >= event_times[3], "second client started before first launch completed")
        exits.append(observed_exit)
        expected_argv = [fixture_path,
                         "--role", "serve" if index == 0 else "client", "--generation", generation,
                         "--port", str(registration["port"])]
        expected_argv += ["--count", "2"] if index == 0 else ["--token", tokens[index - 1]]
        expected_argv += ["--allow-loopback"]
        require(child["argv"] == expected_argv, "child argv mismatch")
        stdout = bound_file(directory, child["stdout"], label + ".stdout.jsonl", 65536)
        bound_file(directory, child["stderr"], label + ".stderr.log", 65536)
        lines = stdout.decode("utf-8").splitlines()
        require(len(lines) == (3 if index == 0 else 1) and all(lines), "fixture record count mismatch")
        records = [parse_json(line) for line in lines]
        if index == 0:
            require(records[0]["event"] == "listening" and all(
                item["event"] == "connection" and item["role"] == "serve"
                for item in records[1:]), "invalid server stream")
        else:
            item = records[0]
            require(item["event"] == "connection" and item["role"] == "client"
                    and item["token"] == tokens[index - 1] and type(item["pid"]) is int
                    and item["pid"] == child["pid"], "client owned PID/token mismatch")
        fixtures.extend(records)
    require(len(set(pids + [controller])) == 4, "reused child/controller PID")
    require(max(exits) <= event_times[-1], "completion precedes observed exits")
    scope = document(scope_raw)
    require(scope["schema"] == "sourceidentity-lab-scope-v1"
            and scope["evidence_kind"] == "operator_attestation", "invalid scope evidence")
    require(scope["generation"] == generation and scope["boot_id"] == run["boot_id"],
            "scope generation/boot mismatch")
    for field, raw in (("registration_sha256", raws["registration"]),
                       ("observer_input_sha256", raws["observer_input"]),
                       ("attached_sha256", raws["attached"]), ("fixture_run_sha256", run_raw),
                       ("observer_result_sha256", observer_raw)):
        require(scope[field] == digest(raw), "scope hash mismatch: " + field)
    require(type(scope["observer_exit_code"]) is int and scope["observer_exit_code"] == 0,
            "unsuccessful observer exit")
    require(isinstance(scope["claims"], dict) and set(scope["claims"]) == CLAIMS,
            "scope claim set mismatch")
    for claim in scope["claims"].values():
        require(isinstance(claim, dict) and set(claim) == {"status", "detail"}
                and claim["status"] == "operator_reported" and isinstance(claim["detail"], str)
                and 0 < len(claim["detail"].strip()) <= 4096 and len(claim["detail"]) <= 4096,
                "invalid operator claim")
    result = check_bundle(registration, source, document(observer_raw), fixtures)
    require(result["status"] == "consistent_unverified", "; ".join(result["errors"]))
    return result["pairs"]


def check_run(run_directory, observer_result, scope_evidence, *, original_run_directory=None):
    result = dict(schema="sourceidentity-fixture-run-check-v1", status="rejected", errors=[],
                  pairs=[], inputs=[], external_claims="operator_reported_unverified")
    for field in ("identity_capability", "runtime", "association", "completeness",
                  "scope_isolation", "startup_identity", "preregistration"):
        result[field] = "unverified"
    directory = None
    try:
        if original_run_directory is not None:
            require(isinstance(original_run_directory, str)
                    and original_run_directory.startswith("/")
                    and "\x00" not in original_run_directory
                    and all(part and part not in (".", "..")
                            for part in original_run_directory[1:].split("/")),
                    "canonical absolute POSIX original run directory required")
            result["original_run_directory"] = original_run_directory
            result["original_run_directory_claim"] = "operator_declared_unverified"
        require(os.path.isabs(run_directory), "absolute run directory required")
        directory = os.open(run_directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        names = {"run.json", *ARTIFACTS.values()}
        names.update(label + suffix for label in LABELS for suffix in (".stdout.jsonl", ".stderr.log"))
        require(set(os.listdir(directory)) == names, "run directory file set mismatch")
        raw_run = read_file("run.json", METADATA_LIMIT, directory)
        result["inputs"].append(dict(path=os.path.join(run_directory, "run.json"), sha256=digest(raw_run)))
        external = []
        for path in (observer_result, scope_evidence):
            raw = read_file(os.fspath(path), METADATA_LIMIT)
            result["inputs"].append(dict(path=os.path.abspath(path), sha256=digest(raw)))
            external.append(raw)
        fixture_path = (original_run_directory + "/fixture" if original_run_directory is not None
                        else os.path.join(os.path.abspath(run_directory), "fixture"))
        result["pairs"] = _check(directory, fixture_path, raw_run, document(raw_run), *external)
        result["status"] = "consistent_unverified"
    except (OSError, ValueError, KeyError, TypeError, AttributeError, OverflowError, RecursionError) as exc:
        result["errors"].append(str(exc))
    finally:
        if directory is not None:
            os.close(directory)
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("run-directory", "observer-result", "scope-evidence"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--original-run-directory",
                        help="declared original POSIX run directory for argv comparison only; "
                             "does not authenticate origin")
    args = parser.parse_args(argv)
    result = check_run(args.run_directory, args.observer_result, args.scope_evidence,
                       original_run_directory=args.original_run_directory)
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0 if result["status"] == "consistent_unverified" else 1


if __name__ == "__main__":
    sys.exit(main())
