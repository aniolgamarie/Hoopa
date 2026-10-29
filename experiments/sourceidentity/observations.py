"""Pure byte decoding for the finite BPFEL observation map ABI.

Decoded records are diagnostic observations, never verified identity evidence.
No map access, collection association, or lifecycle inference happens here.
"""

import ipaddress
import struct
from dataclasses import dataclass


class ObservationError(ValueError):
    """A byte record does not satisfy the finite observation ABI."""


@dataclass(frozen=True)
class Config:
    generation: int
    cgroup: int
    netns: int
    destination_port: int


def _require(condition, message):
    if not condition:
        raise ObservationError(message)


def _bytes(raw, size, name):
    _require(isinstance(raw, bytes) and len(raw) == size,
             "{} must be exactly {} bytes".format(name, size))


def _config(config):
    _require(isinstance(config, Config), "config must be Config")
    for name in ("generation", "cgroup", "netns"):
        value = getattr(config, name)
        _require(type(value) is int and 0 < value <= 0xffffffffffffffff,
                 "invalid config " + name)
    _require(type(config.destination_port) is int and
             1 <= config.destination_port <= 65535, "invalid config destination_port")


def decode_config(raw: bytes) -> Config:
    _bytes(raw, 32, "capture_config value")
    generation, cgroup, netns, port, reserved = struct.unpack("<QQQII", raw)
    _require(reserved == 0, "capture_config reserved must be zero")
    config = Config(generation, cgroup, netns, port)
    _config(config)
    return config


def expected_maps(profile):
    _require(profile in ("capture", "lifecycle", "gated", "namespaced", "online", "executable-online", "scoped-executable-online"), "unsupported profile")
    maps = {
        "capture_config": dict(type=2, key_size=4, value_size=32,
                               max_entries=1, map_flags=128),
        "connect_records": dict(type=1, key_size=8, value_size=112,
                                max_entries=1024, map_flags=8),
        "capture_faults": dict(type=2, key_size=4, value_size=8,
                               max_entries=1, map_flags=8),
    }
    if profile in ("lifecycle", "gated", "namespaced"):
        for name, size in (("established_records", 56 if profile == "namespaced" else 48), ("close_records", 40)):
            maps[name] = dict(type=1, key_size=8, value_size=size,
                              max_entries=2048, map_flags=8)
    if profile in ("gated", "namespaced", "online", "executable-online", "scoped-executable-online"):
        maps["capture_gate"] = dict(type=2, key_size=4, value_size=40,
                                    max_entries=1, map_flags=8)
    if profile in ("online", "executable-online", "scoped-executable-online"):
        maps["online_tuples"] = dict(type=1, key_size=32, value_size=8,
                                    max_entries=1024, map_flags=8)
        maps["online_bindings"] = dict(type=1, key_size=8, value_size=56,
                                      max_entries=1024, map_flags=8)
    if profile in ("executable-online", "scoped-executable-online"):
        maps["calls"] = dict(type=29, key_size=4, value_size=8, max_entries=0, map_flags=1)
        maps["exe_records"] = dict(type=1, key_size=8, value_size=296, max_entries=1024, map_flags=8)
        maps["exe_state"] = dict(type=2, key_size=4, value_size=64, max_entries=1, map_flags=8)
    if profile == "scoped-executable-online":
        maps["identity_scope"] = dict(type=2, key_size=4, value_size=24, max_entries=1, map_flags=128)
    return maps


def decode_gate(raw):
    """Decode one BPF_F_LOCK read. Consistency depends on the caller's flags."""
    _bytes(raw, 40, "capture_gate value")
    lock, closed, active, admitted, completed, poison, reserved = struct.unpack("<IIQQQII", raw)
    _require(lock == 0 and closed in (0, 1) and reserved == 0, "invalid gate ABI")
    _require(poison == 0, "gate is poisoned")
    _require(completed <= admitted and active == admitted - completed, "gate counts inconsistent")
    return dict(closed=bool(closed), active=str(active), admitted=str(admitted),
                completed=str(completed), poison=poison)


def _comm(raw, status):
    _require(status in (0, 1), "invalid comm status")
    _require(raw[-1] == 0, "comm must end in NUL")
    try:
        text = raw.split(b"\0", 1)[0].decode("utf-8")
    except UnicodeDecodeError:
        text = None
    return dict(status="unverified" if status else "unavailable",
                raw_hex=raw.hex(), text=text)


def _online_endpoints(generation, netns, client_ip, server_ip, client_port, server_port, config):
    _require(generation == config.generation and netns == config.netns,
             "online generation or namespace mismatch")
    _require(struct.pack("<I", client_ip) == struct.pack("<I", server_ip) == b"\x7f\0\0\1",
             "non-loopback online endpoint")
    _require(1 <= client_port <= 65535 and server_port == config.destination_port,
             "invalid online endpoint port")
    return dict(generation=str(generation), socket_netns_cookie=str(netns),
                client_ip4="127.0.0.1", server_ip4="127.0.0.1",
                client_port=client_port, server_port=server_port,
                evidence_status="unverified", association="unverified",
                identity_capability="unverified")


def _decode_online(map_name, key, value, config):
    if map_name == "online_tuples":
        _bytes(key, 32, "online tuple key")
        _bytes(value, 8, "online tuple value")
        result = _online_endpoints(*struct.unpack("<QQIIII", key), config)
        cookie, = struct.unpack("<Q", value)
        _require(cookie != 0, "zero online client cookie")
        result["client_cookie"] = str(cookie)
        return result
    _bytes(key, 8, "online binding key")
    _bytes(value, 56, "online binding value")
    generation, netns, accepted, client, client_ip, server_ip, client_port, server_port, schema, reserved = \
        struct.unpack("<QQQQIIIIII", value)
    cookie, = struct.unpack("<Q", key)
    _require(cookie != 0 and accepted == cookie and client != 0,
             "online binding cookie mismatch or zero")
    _require(schema == 1 and reserved == 0, "invalid online binding schema or reserved")
    result = _online_endpoints(generation, netns, client_ip, server_ip, client_port, server_port, config)
    result.update(accepted_cookie=str(accepted), client_cookie=str(client), schema_version=schema)
    return result


def decode_entry(map_name, key: bytes, value: bytes, config: Config, profile="lifecycle"):
    """Decode one snapshot entry without proving collection completeness."""
    _config(config)
    _require(map_name in expected_maps(profile) and
             map_name not in ("capture_config", "capture_gate"), "unsupported map")
    if profile == "online" and map_name in ("online_tuples", "online_bindings"):
        return _decode_online(map_name, key, value, config)
    result = dict(evidence_status="unverified")
    if map_name == "capture_faults":
        _bytes(key, 4, "capture_faults key")
        _bytes(value, 8, "capture_faults value")
        _require(key == b"\0" * 4, "capture_faults key must be zero")
        result["fault_count"] = str(struct.unpack("<Q", value)[0])
        return result

    size = {"connect_records": 112,
            "established_records": 56 if profile == "namespaced" else 48,
            "close_records": 40}[map_name]
    _bytes(key, 8, "cookie key")
    _bytes(value, size, map_name + " value")
    cookie = struct.unpack("<Q", key)[0]
    generation, observed, record_cookie = struct.unpack_from("<QQQ", value)
    _require(cookie != 0 and cookie == record_cookie, "cookie key mismatch or zero")
    _require(generation == config.generation, "generation mismatch")
    result.update(generation=str(generation), observed_boot_ns=str(observed))

    if map_name == "connect_records":
        names64 = ("client_cookie", "socket_netns_cookie", "cgroup",
                   "leader_start_boottime_ns")
        result.update(zip(names64, map(str, struct.unpack_from("<QQQQ", value, 16))))
        fields = ("schema_version", "tgid_initial", "caller_tid_initial",
                  "active_pidns_inum", "leader_pid_initial",
                  "caller_comm_status", "leader_comm_status", "reserved")
        numbers = dict(zip(fields, struct.unpack_from("<8I", value, 48)))
        _require(numbers["schema_version"] == 1, "unsupported schema")
        _require(numbers["reserved"] == 0, "reserved must be zero")
        _require(int(result["socket_netns_cookie"]) == config.netns and
                 int(result["cgroup"]) == config.cgroup, "namespace or cgroup mismatch")
        _require(numbers["tgid_initial"] > 0 and
                 numbers["leader_pid_initial"] == numbers["tgid_initial"] and
                 numbers["caller_tid_initial"] > 0 and numbers["active_pidns_inum"] > 0 and
                 int(result["leader_start_boottime_ns"]) > 0, "invalid process coordinates")
        result.update(numbers)
        result["caller_comm"] = _comm(value[80:96], numbers["caller_comm_status"])
        result["leader_comm"] = _comm(value[96:112], numbers["leader_comm_status"])
        return result

    result.update(socket_cookie=str(cookie), socket_netns_cookie="unknown",
                  process_identity="unknown", association="unverified")
    schema, = struct.unpack_from("<I", value, 24)
    expected_schema = 2 if profile == "namespaced" and map_name == "established_records" else 1
    _require(schema == expected_schema, "unsupported schema")
    result["schema_version"] = schema
    if map_name == "established_records":
        if profile == "namespaced":
            netns, = struct.unpack_from("<Q", value, 48)
            _require(netns != 0 and netns == config.netns, "namespace mismatch or zero")
            result["socket_netns_cookie"] = str(netns)
        op, = struct.unpack_from("<I", value, 28)
        _require(op in (4, 5), "invalid established callback op")
        local_ip, remote_ip = value[32:36], value[36:40]
        _require(local_ip == remote_ip == b"\x7f\0\0\1", "non-loopback endpoint")
        local_port, remote_port = struct.unpack_from("<II", value, 40)
        _require(1 <= local_port <= 65535 and 1 <= remote_port <= 65535,
                 "invalid endpoint port")
        _require((remote_port if op == 4 else local_port) == config.destination_port,
                 "entry port mismatch")
        result.update(callback_op=op, local_ip4=str(ipaddress.IPv4Address(local_ip)),
                      remote_ip4=str(ipaddress.IPv4Address(remote_ip)),
                      local_port=local_port, remote_port=remote_port)
    else:
        old_state, new_state, reserved = struct.unpack_from("<III", value, 28)
        _require(1 <= old_state <= 12 and new_state == 7, "invalid TCP state")
        _require(reserved == 0, "reserved must be zero")
        result.update(old_state=old_state, new_state=new_state, reserved=reserved)
    return result


def decode_identity_scope(raw):
    """Pure fixed v1 scope map value. No implicit reserved-field versions."""
    _bytes(raw,24,"identity_scope value")
    generation,user,mount,version,reserved=struct.unpack("<QIIII",raw)
    _require(generation>0 and user>0 and mount>0 and version==1 and reserved==0,"invalid identity scope")
    return dict(generation=generation,userns_inode=user,mountns_inode=mount,schema_version=version,reserved=reserved)

def encode_identity_scope(generation,user,mount):
    _require(type(generation) is int and 0<generation<2**64 and all(type(x) is int and 0<x<2**32 for x in (user,mount)),"invalid scope coordinates")
    return struct.pack("<QIIII",generation,user,mount,1,0)
