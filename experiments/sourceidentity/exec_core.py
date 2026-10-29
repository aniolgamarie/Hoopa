#!/usr/bin/env python3
"""Explicit isolated-guest BPF bootstrap; replace this process with a pinned core.

There is no daemon, runtime RPC or host installation. The core inherits the
actual link/map/program FDs and must validate them before opening its entry.
"""
import argparse
import fcntl
import hashlib
import json
import os
import platform
from pathlib import Path
import re
import stat
import struct
import sys

from native_loader import NativeBackend
from online_admission import AdmissionOwner, pinned, require

MAPS = ("capture_config", "connect_records", "capture_faults", "capture_gate",
        "online_tuples", "online_bindings")
PROGRAMS = ("hoopa_connect", "hoopa_lifecycle", "hoopa_stop")
LINKS = ("hoopa_connect", "hoopa_lifecycle")
EXECUTABLE_MAPS = MAPS + ("calls", "exe_records", "exe_state")
EXECUTABLE_PROGRAMS = PROGRAMS + ("call_enter", "call_exit", "capture_exe")
SCOPED_MAPS = EXECUTABLE_MAPS + ("identity_scope",)
EXECUTABLE_PROFILES = ("executable-online", "scoped-executable-online")

EXECUTABLE_LINKS = ("call_exit", "call_enter", "capture_exe", "hoopa_connect", "hoopa_lifecycle")


def resource_names(profile):
    require(profile in ("online", "executable-online", "scoped-executable-online"), "unsupported bootstrap profile")
    if profile == "scoped-executable-online":
        return SCOPED_MAPS, EXECUTABLE_PROGRAMS, EXECUTABLE_LINKS
    if profile == "executable-online":
        return EXECUTABLE_MAPS, EXECUTABLE_PROGRAMS, EXECUTABLE_LINKS
    return MAPS, PROGRAMS, LINKS


IDENTITY_PROFILE = "linux_amd64_loopback_connect_v1"

def handoff_schema(profile, identity_profile=None):
    if identity_profile is not None:
        require(profile=="scoped-executable-online" and identity_profile==IDENTITY_PROFILE,"identity profile requires exact scoped profile")
        return "hoopa-core-identity-handoff-v4"
    resource_names(profile)
    return {"online":"hoopa-core-identity-handoff-v1","executable-online":"hoopa-core-identity-handoff-v2","scoped-executable-online":"hoopa-core-identity-handoff-v3"}[profile]



class BootstrapCleanupError(RuntimeError):
    """Retain the initiating failure and every cleanup failure on Python 3.8."""
    def __init__(self, primary, errors):
        self.primary = primary
        self.cleanup_errors = tuple(errors)
        super().__init__("{}; cleanup: {}".format(primary, "; ".join(errors)))


def close_once(fds, close):
    """Close only already-owned descriptors, once, and retain every error."""
    errors = []
    for fd in reversed(fds):
        try:
            close(fd)
        except OSError as exc:
            errors.append("close fd {}: {}".format(fd, exc))
    return errors


def duplicate_resources(backend, owner, duplicate=None, inheritable=None, close=None,
                        get_inheritable=None, profile="online"):
    """Return sole explicit exec exports; register each FD before another call."""
    duplicate = duplicate or (lambda fd: fcntl.fcntl(fd, fcntl.F_DUPFD_CLOEXEC, 200))
    inheritable = inheritable or os.set_inheritable
    get_inheritable = get_inheritable or os.get_inheritable
    close = close or os.close
    maps, programs, links = resource_names(profile)
    require(set(backend.map_fds) == set(maps) and
            set(backend.program_fds) == set(programs), "exact source resources required")
    require(len(owner.links) == len(links) and {entry[0] for entry in owner.links} == set(links),
            "exact source links required")
    sources = [("maps", name, backend.map_fds[name]) for name in maps]
    sources += [("programs", name, backend.program_fds[name]) for name in programs]
    original_links = dict(owner.links)
    sources += [("links", name, original_links[name]) for name in links]
    source_fds = [entry[2] for entry in sources]
    require(all(type(fd) is int and fd >= 0 for fd in source_fds) and
            len(set(source_fds)) == len(source_fds), "invalid source resource FDs")
    slots = [None] * len(sources)
    result = {"maps": {}, "programs": {}, "links": {}}
    try:
        for index, (kind, name, fd) in enumerate(sources):
            slots[index] = duplicate(fd)
            require(type(slots[index]) is int and 200 <= slots[index] <= 1023,
                    "export FD outside dedicated range")
            require(slots[index] not in source_fds and slots[index] not in slots[:index],
                    "export FD aliases existing resource")
            require(not get_inheritable(slots[index]), "new export FD not CLOEXEC")
            result[kind][name] = slots[index]
        # Every copy is complete and CLOEXEC before any inheritance is enabled.
        for fd in source_fds:
            require(not get_inheritable(fd), "original BPF FD not CLOEXEC")
        for fd in slots:
            inheritable(fd, True)
        return result, slots
    except BaseException as exc:
        # F_DUPFD_CLOEXEC never returns an original or a repeated FD. Defend
        # injected/malformed results without double-closing or closing sources.
        owned = []
        for fd in slots:
            if type(fd) is int and fd >= 200 and fd not in source_fds and fd not in owned:
                owned.append(fd)
        errors = close_once(owned, close)
        if errors:
            raise BootstrapCleanupError(exc, errors) from exc
        raise


def encode_handoff(handoff, profile="online", identity_profile=None):
    """Validate the exact generated record before exposing it through exec."""
    maps, programs, links = resource_names(profile)
    names = {"schema", "core_generation", "generation", "source_cgroup_id",
             "root_cgroup_id", "netns_cookie", "pidns_inode", "boot_id",
             "source_path", "port", "maps", "programs", "links"}
    if profile == "scoped-executable-online": names.update(("userns_inode", "mountns_inode"))
    if identity_profile is not None: names.add("identity_profile")
    require(type(handoff) is dict and set(handoff) == names, "exact handoff fields required")
    require(handoff["schema"] == handoff_schema(profile,identity_profile) and
            isinstance(handoff["core_generation"], str) and handoff["core_generation"],
            "invalid handoff schema/core generation")
    if identity_profile is not None: require(handoff["identity_profile"]==IDENTITY_PROFILE,"exact identity profile declaration")
    for name in ("generation", "source_cgroup_id", "root_cgroup_id", "netns_cookie"):
        value = handoff[name]
        require(type(value) is str and re.fullmatch(r"[1-9][0-9]{0,19}", value) and
                int(value) < 2**64, "invalid handoff uint64: " + name)
    require(handoff["source_cgroup_id"] != handoff["root_cgroup_id"], "source must differ from root")
    require(type(handoff["pidns_inode"]) is int and 0 < handoff["pidns_inode"] < 2**32,
            "invalid handoff pid namespace")
    if profile == "scoped-executable-online":
        for name in ("userns_inode", "mountns_inode"):
            require(type(handoff[name]) is int and 0<handoff[name]<2**32,"invalid handoff namespace "+name)
    require(type(handoff["port"]) is int and 0 < handoff["port"] <= 65535, "invalid handoff port")
    require(type(handoff["boot_id"]) is str and re.fullmatch(
        r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", handoff["boot_id"]),
        "invalid handoff boot ID")
    require(type(handoff["source_path"]) is str and re.fullmatch(
        r"/sys/fs/cgroup/hoopa-online-[A-Za-z0-9_-]+", handoff["source_path"]),
        "invalid handoff source path")
    seen = set()
    for kind, expected in (("maps", maps), ("programs", programs), ("links", links)):
        group = handoff[kind]
        require(type(group) is dict and set(group) == set(expected), "exact handoff " + kind + " required")
        for fd in group.values():
            require(type(fd) is int and 200 <= fd <= 1023 and fd not in seen,
                    "invalid/duplicate handoff FD")
            seen.add(fd)
    encoded = json.dumps(handoff, separators=(",", ":"), sort_keys=True)
    require(len(encoded.encode()) <= 8192, "handoff exceeds bound")
    return encoded


def open_core(path, expected):
    require(Path(path).is_absolute() and re.fullmatch(r"[0-9a-f]{64}", expected),
            "absolute core path and SHA256 required")
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
    try:
        info = os.fstat(fd)
        require(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= 128 * 1024 * 1024,
                "bounded regular core required")
        require(info.st_mode & 0o111, "core is not executable")
        digest = hashlib.sha256()
        remaining = info.st_size
        while remaining:
            data = os.read(fd, min(1024 * 1024, remaining))
            require(data, "short core read")
            digest.update(data)
            remaining -= len(data)
        require(not os.read(fd, 1) and digest.hexdigest() == expected, "core hash mismatch")
        return fd
    except BaseException as exc:
        errors = close_once([fd], os.close)
        if errors:
            raise BootstrapCleanupError(exc, errors) from exc
        raise


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--allow-bpf-load", action="store_true")
    parser.add_argument("--identity-profile",choices=(IDENTITY_PROFILE,))
    parser.add_argument("--profile", choices=("online", "executable-online", "scoped-executable-online"), default="online")
    for name in ("object", "object-sha256", "libbpf", "libbpf-sha256", "core",
                 "core-sha256", "core-config", "core-home", "core-generation",
                 "scope", "output"):
        parser.add_argument("--"+name, required=True)
    parser.add_argument("--generation", type=int, required=True)
    parser.add_argument("--netns-cookie", type=int, required=True)
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args(argv)
    if not args.allow_bpf_load:
        parser.error("--allow-bpf-load required before reading inputs or loading BPF")
    require(os.geteuid() == 0 and "hoopa_lab=t132" in Path("/proc/cmdline").read_text().split(),
            "dedicated privileged guest only")
    require(os.execve in os.supports_fd, "execve by verified FD required")
    require(0 < args.generation < 2**64 and 0 < args.netns_cookie < 2**64 and
            0 < args.port <= 65535, "invalid BPF registration")
    require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}", args.core_generation),
            "invalid core generation")
    require(os.environ.get("HOOPA_GUARD_GENERATION") == args.core_generation and
            re.fullmatch(r"[0-9a-f]{64}", os.environ.get("HOOPA_GUARD_CONFIG_SHA256", "")),
            "protected core configuration required")
    root = Path("/sys/fs/cgroup")
    scope = Path(args.scope)
    require(scope.is_absolute() and scope.parent == root and
            scope.name.startswith("hoopa-online-") and not scope.is_symlink(),
            "dedicated direct source leaf required")
    require((scope/"cgroup.type").read_text().strip() == "domain" and
            not (scope/"cgroup.procs").read_text().strip(), "empty domain leaf required")
    require(Path("/proc/self/cgroup").read_text().strip() == "0::/", "start at guest cgroup root")
    mounts = [line for line in Path("/proc/self/mountinfo").read_text().splitlines()
              if line.split()[4] == str(root)]
    require(len(mounts) == 1 and mounts[0].split(" - ")[1].split()[0] == "cgroup2",
            "cgroup2 root required")
    for namespace in ("pid", "net", "user", "time", "time_for_children") + (("mnt",) if args.profile == "scoped-executable-online" else ()):
        require(os.stat("/proc/self/ns/"+namespace).st_ino == os.stat("/proc/1/ns/"+namespace).st_ino,
                "initial guest namespace required")
    for path in (args.core_config, args.core_home, args.output):
        require(Path(path).is_absolute(), "absolute core/output paths required")
    if args.identity_profile is not None:
        require(os.environ.get("HOOPA_TRACE_PROFILE")=="5","explicit field profile requires source5 environment")
        require(args.profile=="scoped-executable-online","identity fields require scoped executable profile")
        require(args.object_sha256=="df7a561fd4b97cbb03b1f7083e3ffcc54a465233ae06ee47c4be67989eb8fc49" and args.libbpf_sha256=="1c928f61158f25d39cd3fbc7ddafeebec2448886225e2c1824455fcfcd15d04c","fixed field profile assets")
        require(platform.release()=="6.18.54.1-microsoft-standard-WSL2" and platform.machine()=="x86_64","fixed field profile kernel")
        pinned("/sys/kernel/btf/vmlinux","1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9",8*1024*1024)
    obj = pinned(args.object, args.object_sha256, 64*1024*1024)
    pinned(args.libbpf, args.libbpf_sha256, 32*1024*1024)
    output = Path(args.output)
    output.mkdir(mode=0o700)
    corefd = rootfd = None
    owner = None
    exported = []
    try:
        corefd = open_core(args.core, args.core_sha256)
        backend = NativeBackend(args.libbpf, obj, profile=args.profile)
        config = struct.pack("<QQQII", args.generation, scope.stat().st_ino,
                             args.netns_cookie, args.port, 0)
        scope_ids = None
        if args.profile == "scoped-executable-online":
            from observations import encode_identity_scope
            scope_ids={name:os.stat("/proc/self/ns/"+namespace).st_ino for name,namespace in (("userns_inode","user"),("mountns_inode","mnt"))}
            require(all(value == os.stat("/proc/1/ns/"+namespace).st_ino for name,namespace in (("userns_inode","user"),("mountns_inode","mnt")) for value in (scope_ids[name],)),"trusted scope namespace changed")
            backend.set_identity_scope(encode_identity_scope(args.generation,scope_ids["userns_inode"],scope_ids["mountns_inode"]))
        owner = AdmissionOwner(backend, config, root.stat().st_ino, lambda value: None)
        if args.profile in EXECUTABLE_PROFILES:
            owner.links = [[name, None] for name in EXECUTABLE_LINKS]
        rootfd = os.open(str(root), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        objects = owner.start(rootfd)
        require(not (scope/"cgroup.procs").read_text().strip(), "scope changed before placement")
        (scope/"cgroup.procs").write_text(str(os.getpid()))
        require(Path("/proc/self/cgroup").read_text().strip() == "0::/"+scope.name,
                "core placement mismatch")
        fdset, exported = duplicate_resources(backend, owner, profile=args.profile)
        handoff = dict(schema=handoff_schema(args.profile,args.identity_profile),
                       core_generation=args.core_generation, generation=str(args.generation),
                       source_cgroup_id=str(scope.stat().st_ino), root_cgroup_id=str(root.stat().st_ino),
                       netns_cookie=str(args.netns_cookie), pidns_inode=os.stat("/proc/self/ns/pid").st_ino,
                       boot_id=Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
                       source_path=str(scope), port=args.port, **fdset)
        if scope_ids is not None: handoff.update(scope_ids)
        if args.identity_profile is not None: handoff["identity_profile"]=args.identity_profile
        encoded = encode_handoff(handoff, profile=args.profile,identity_profile=args.identity_profile)
        intent = dict(schema="hoopa-core-exec-intent-v1", pid=os.getpid(),
                      core_sha256=args.core_sha256, object_sha256=args.object_sha256,
                      libbpf_sha256=args.libbpf_sha256, handoff=handoff, kernel_objects=objects)
        if args.profile == "scoped-executable-online":
            intent["identity_scope_snapshot"] = dict(
                schema="hoopa-core-identity-scope-readback-v1",
                reading_point="after_links_before_exec_readonly_map_lookup",
                raw_hex=backend.read_frozen_identity_scope().hex())
        fd = os.open(str(output/"exec-intent.json"), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(intent, stream, sort_keys=True, indent=2)
            stream.write("\n")
        env = dict(os.environ, HOOPA_IDENTITY_HANDOFF=encoded)
        os.execve(corefd, [args.core, "-d", args.core_home, "-f", args.core_config], env)
        raise RuntimeError("execve unexpectedly returned")
    finally:
        # This executes only if exec did not succeed. No child has been made.
        primary = sys.exc_info()[1]
        errors = close_once(exported, os.close)
        if owner is not None:
            try:
                errors.extend(owner.close()["errors"])
            except BaseException as exc:
                errors.append("owner close: " + str(exc))
        errors.extend(close_once([fd for fd in (corefd, rootfd) if fd is not None], os.close))
        if errors:
            raise BootstrapCleanupError(primary or "exec did not replace process", errors) from primary



if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print("exec-core: "+str(exc), file=sys.stderr)
        sys.exit(1)
