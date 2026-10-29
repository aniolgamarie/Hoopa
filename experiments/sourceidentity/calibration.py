#!/usr/bin/env python3
"""Offline, bounded comparison of namespaced observations and fixture traffic.

This is not an online resolver or a capability gate. Pairing uses only raw BPF
observations; independently transported fixture tokens are checked afterwards.
Even a consistent bundle cannot prove provenance, coverage or unique identity.
"""
import argparse
import hashlib
import json
import math
import os
import re
import stat
import sys

from observations import decode_config, decode_entry, expected_maps

LIMIT = 4 * 1024 * 1024
TOKEN = re.compile(r"[0-9a-f]{32}\Z")
DECIMAL = re.compile(r"[1-9][0-9]{0,19}\Z")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def u64(value):
    require(isinstance(value, str) and DECIMAL.fullmatch(value) is not None,
            "noncanonical positive uint64 string")
    result = int(value)
    require(result < 2**64, "uint64 overflow")
    return result


def exact_hex(value, length):
    require(isinstance(value, str) and len(value) == length * 2 and
            re.fullmatch(r"[0-9a-f]+", value) is not None, "invalid raw hex")
    return bytes.fromhex(value)


def endpoint(value):
    require(isinstance(value, str) and re.fullmatch(r"127\.0\.0\.1:[1-9][0-9]{0,4}", value),
            "invalid loopback endpoint")
    port = int(value.split(":")[1])
    require(port <= 65535, "invalid endpoint port")
    return port


def tuple_key(record):
    active = record["callback_op"] == 4
    return (record["generation"], record["socket_netns_cookie"],
            record["local_port"] if active else record["remote_port"],
            record["remote_port"] if active else record["local_port"])


def candidates(config, raw, count):
    """Candidate computation has no token, fixture, or expected-cookie input."""
    declarations = expected_maps("namespaced")
    names = set(declarations) - {"capture_config", "capture_gate"}
    require(isinstance(raw, dict) and set(raw) == names, "snapshot map set mismatch")
    bounds = dict(connect_records=count, established_records=count * 2,
                  capture_faults=1, close_records=count * 2)
    decoded = {}
    for name in names:
        rows, seen = raw[name], set()
        require(isinstance(rows, list) and len(rows) <= bounds[name], "unexpected map count")
        decoded[name] = []
        for row in rows:
            key = exact_hex(row["key_hex"], declarations[name]["key_size"])
            value = exact_hex(row["value_hex"], declarations[name]["value_size"])
            require(key not in seen, "duplicate map key")
            seen.add(key)
            # Embedded decoder caches are deliberately not used as evidence.
            decoded[name].append(decode_entry(name, key, value, config, profile="namespaced"))
    faults = decoded["capture_faults"]
    require(len(faults) == 1 and faults[0]["fault_count"] == "0", "missing or nonzero fault counter")
    connects = {r["client_cookie"]: r for r in decoded["connect_records"]}
    active, passive, established = {}, {}, {}
    for row in decoded["established_records"]:
        key = tuple_key(row)
        index = active if row["callback_op"] == 4 else passive
        require(key not in index, "duplicate tuple; reuse cannot be disambiguated")
        index[key] = row
        established[row["socket_cookie"]] = row
    require(len(connects) == len(active) == len(passive) == count and set(active) == set(passive),
            "incomplete or extra connect/active/passive observations")
    require({r["socket_cookie"] for r in active.values()} == set(connects), "active/connect cookie mismatch")
    result = {}
    for key, server in passive.items():
        client = active[key]
        connect = connects[client["socket_cookie"]]
        require(int(connect["observed_boot_ns"]) <= int(client["observed_boot_ns"]) <=
                int(server["observed_boot_ns"]), "callback order contradicts ordinary handshake")
        require(server["socket_cookie"] not in connects, "client/server cookie collision")
        result[server["socket_cookie"]] = (server, client, connect)
    for close in decoded["close_records"]:
        row = established.get(close["socket_cookie"])
        require(row is not None and int(close["observed_boot_ns"]) >= int(row["observed_boot_ns"]),
                "unmatched or premature close observation")
    return result


def _check(registration, source, session, fixtures):
    require(registration["schema"] == "sourceidentity-calibration-registration-v1", "registration schema mismatch")
    generation, netns, cgroup = (u64(registration[k]) for k in ("generation", "netns_cookie", "cgroup"))
    port, tokens = registration["port"], registration["tokens"]
    require(type(port) is int and 1 <= port <= 65535, "invalid registered port")
    require(isinstance(tokens, list) and 1 <= len(tokens) <= 16 and
            all(isinstance(t, str) and TOKEN.fullmatch(t) for t in tokens) and
            len(set(tokens)) == len(tokens), "invalid registered token set")
    require(source["schema"] == "sourceidentity-observer-input-v1" and source["profile"] == "namespaced",
            "observer input must use namespaced profile")
    config = decode_config(exact_hex(source["config_hex"], 32))
    require((config.generation, config.netns, config.cgroup, config.destination_port) ==
            (generation, netns, cgroup, port), "registration/config mismatch")
    require(session["schema"] == "sourceidentity-loader-session-v1" and
            session["profile"] == "namespaced" and session["status"] == "observed_unverified" and
            session["errors"] == [] and session["attachment_observed"] is True,
            "unsuccessful or wrong observer session")
    stop = session["writer_stop"]
    require(stop["status"] == "closed_and_drained_observed", "missing writer stop observation")
    gate = stop["gate"]
    require(gate["closed"] is True and gate["active"] == "0" and
            type(gate["poison"]) is int and gate["poison"] == 0 and
            u64(gate["admitted"]) == u64(gate["completed"]), "inconsistent writer stop observation")
    pairs = candidates(config, session["diagnostic_snapshot"], len(tokens))
    require(isinstance(fixtures, list) and len(fixtures) <= 33, "too many fixture records")
    clients, servers, ready = {}, {}, False
    for item in fixtures:
        require(item["schema"] == "sourceidentity-fixture-v1" and
                item["generation"] == str(generation), "fixture schema/generation mismatch")
        if item["event"] == "listening":
            require(not ready and type(item["port"]) is int and item["port"] == port, "invalid listening event")
            ready = True
            continue
        require(item["event"] == "connection" and item["identity_capability"] == "unverified",
                "unexpected fixture event/capability")
        token, role = item["token"], item["role"]
        require(token in tokens and role in ("client", "serve"), "unregistered fixture token/role")
        require(role != "serve" or ready, "server record precedes listening event")
        index = clients if role == "client" else servers
        require(token not in index, "duplicate fixture token/role")
        index[token] = item
    require(ready and set(clients) == set(servers) == set(tokens), "incomplete listening/fixture token sets")
    seen_clients, seen_servers, output = set(), set(), []
    for token in tokens:
        client, witness = clients[token], servers[token]["witness"]
        require(witness["schema"] == "accepted-socket-witness-v1" and
                witness["association"] == witness["identity_capability"] == "unverified" and
                witness["generation"] == str(generation), "invalid original socket witness")
        client_cookie, server_cookie = str(u64(client["socket_cookie"])), str(u64(witness["socket_cookie"]))
        require(client_cookie not in seen_clients and server_cookie not in seen_servers, "duplicate fixture cookie")
        seen_clients.add(client_cookie); seen_servers.add(server_cookie)
        require(u64(client["netns_cookie"]) == u64(witness["netns_cookie"]) == netns,
                "fixture socket namespace mismatch")
        require(server_cookie in pairs, "original witness missing from passive observations")
        server, active, connect = pairs[server_cookie]
        require(active["socket_cookie"] == client_cookie, "transport token disagrees with candidate client")
        require(endpoint(client["local_endpoint"]) == active["local_port"] and
                endpoint(client["remote_endpoint"]) == active["remote_port"] and
                endpoint(witness["local_endpoint"]) == server["local_port"] and
                endpoint(witness["remote_endpoint"]) == server["remote_port"], "fixture endpoint mismatch")
        require(type(client["pid"]) is int and 0 < client["pid"] <= 0xffffffff and
                client["pid"] == connect["tgid_initial"] and
                u64(client["pidns_inode"]) == connect["active_pidns_inum"], "fixture PID/namespace mismatch")
        u64(client["start_ticks"])
        output.append(dict(token=token, client_cookie=client_cookie, accepted_cookie=server_cookie,
                           pid=client["pid"], pidns_inode=client["pidns_inode"],
                           proc_start_ticks=client["start_ticks"],
                           observed_start_boottime_ns=connect["leader_start_boottime_ns"]))
    require(seen_servers == set(pairs), "unconsumed candidate pairs")
    return output


def check_bundle(registration, source, session, fixtures):
    result = dict(schema="sourceidentity-calibration-v1", status="rejected", errors=[], pairs=[],
                  runtime="unverified", association="unverified", completeness="unverified",
                  identity_capability="unverified", scope_isolation="unverified",
                  startup_identity="unverified", preregistration="unverified")
    try:
        result["pairs"] = _check(registration, source, session, fixtures)
        result["status"] = "consistent_unverified"
    except (ValueError, KeyError, TypeError, AttributeError, OverflowError) as exc:
        result["errors"].append(str(exc))
    return result


def parse_json(raw):
    def unique(pairs):
        value = {}
        for key, item in pairs:
            require(key not in value, "duplicate JSON member: " + key)
            value[key] = item
        return value
    def nonfinite(value):
        raise ValueError("nonfinite JSON value: " + value)
    def finite_float(value):
        parsed = float(value)
        require(math.isfinite(parsed), "nonfinite JSON number")
        return parsed
    return json.loads(raw, object_pairs_hook=unique, parse_constant=nonfinite, parse_float=finite_float)


def read_input(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        require(stat.S_ISREG(os.fstat(stream.fileno()).st_mode), "regular input file required")
        data = stream.read(LIMIT + 1)
    require(len(data) <= LIMIT, "input exceeds 4 MiB")
    return data.decode("utf-8"), dict(path=os.path.abspath(path), sha256=hashlib.sha256(data).hexdigest())


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("registration", "observer-input", "observer-result"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--fixtures", nargs="+", required=True)
    args = parser.parse_args(argv)
    hashes = []
    try:
        require(len(args.fixtures) <= 17, "at most 17 fixture files")
        documents = []
        for path in (args.registration, args.observer_input, args.observer_result):
            raw, digest = read_input(path); hashes.append(digest)
            documents.append(parse_json(raw))
        fixtures = []
        for path in args.fixtures:
            raw, digest = read_input(path); hashes.append(digest)
            lines = raw.splitlines()
            require(1 <= len(lines) <= 33 and all(lines), "invalid fixture JSONL")
            fixtures.extend(parse_json(line) for line in lines)
        result = check_bundle(*documents, fixtures)
    except (OSError, ValueError, RecursionError) as exc:
        result = check_bundle({}, {}, {}, [])
        result["errors"] = [str(exc)]
    result["inputs"] = hashes
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0 if result["status"] == "consistent_unverified" else 1


if __name__ == "__main__":
    sys.exit(main())
