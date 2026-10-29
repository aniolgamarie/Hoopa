"""One finite observation session; no identity association or stable snapshot claim."""
import time

from observations import decode_config, decode_entry, expected_maps


def error_record(stage, exc):
    result = {"stage": stage, "type": type(exc).__name__, "message": str(exc)}
    for field in ("ret", "errno"):
        value = getattr(exc, field, None)
        if value is not None:
            result[field] = value
    return result


def diagnostic_snapshot(raw, config, profile="lifecycle"):
    """Retain raw entries even when decoding fails. Never infer absent events."""
    expected = set(expected_maps(profile)) - {"capture_config", "capture_gate"}
    if set(raw) != expected:
        raise ValueError("snapshot map set mismatch")
    result, errors = {}, []
    for name, entries in raw.items():
        result[name] = []
        for key, value in entries:
            record = {"key_hex": key.hex(), "value_hex": value.hex()}
            try:
                record["observation"] = decode_entry(name, key, value, config, profile=profile)
            except ValueError as exc:
                record["decode_error"] = str(exc)
                errors.append(error_record("decode:" + name, exc))
            result[name].append(record)
    faults = raw["capture_faults"]
    if len(faults) != 1 or faults[0][0] != bytes(4) or len(faults[0][1]) != 8:
        errors.append(error_record("faults", ValueError("missing or invalid fault counter")))
    elif int.from_bytes(faults[0][1], "little") != 0:
        errors.append(error_record("faults", ValueError("capture faults invalidate this run")))
    return result, errors


def observe(backend, config_raw, cgroup_fd, duration, emit, wait=time.sleep,
            check_target=lambda: None, cancelled=lambda: False, gated=False, profile=None):
    """Backend methods are the only kernel boundary; tests inject a fake backend.

    The caller owns the cgroup FD. This session owns the object and every link it
    creates. A link is registered before verification, then closed once in reverse
    order. A diagnostic scan follows successful link close, without claiming an
    RCU grace period or callback drain. Failures skip the scan and retain errors.
    """
    if profile is None:
        profile = "gated" if gated else "lifecycle"
    if profile not in ("lifecycle", "gated", "namespaced"):
        raise ValueError("unsupported session profile")
    if getattr(backend, "profile", profile) != profile:
        raise ValueError("backend/session profile mismatch")
    if gated and profile == "lifecycle":
        raise ValueError("gated session requires a gated profile")
    gated = profile in ("gated", "namespaced")
    config = decode_config(config_raw)
    if isinstance(duration, bool) or not isinstance(duration, (int, float)) or not 0 < duration <= 60:
        raise ValueError("observation duration must be greater than 0 and at most 60 seconds")
    result = {
        "schema": "sourceidentity-loader-session-v1", "status": "failed",
        "attachment_observed": False, "kernel_objects": None, "links": [],
        "link_close_attempts": [], "errors": [], "diagnostic_snapshot": None,
        "quiescence": "unverified", "completeness": "unverified",
        "association": "unverified", "identity_capability": "unverified",
        "writer_stop": None, "profile": profile,
    }
    # Reserve the two ownership slots before acquiring any native resource.
    links = [["hoopa_connect", None], ["hoopa_lifecycle", None]]
    finished, interrupted = False, False
    stage = "open_object"
    def check_cancel():
        if cancelled():
            raise InterruptedError("stop requested")

    try:
        check_cancel()
        backend.open_object()
        stage = "load_object"
        check_cancel()
        backend.load_object()
        stage = "verify_loaded"
        check_cancel()
        result["kernel_objects"] = backend.verify_loaded()
        stage = "initialize_config"
        check_cancel()
        backend.initialize_config(config_raw)
        stage = "freeze_config"
        check_cancel()
        backend.freeze_config(config_raw)
        # The target must remain empty throughout preparation. This is a
        # precondition check, not a lock preventing external cgroup mutation.
        stage = "check_target"
        check_target()
        for entry in links:
            program = entry[0]
            stage = "create_link:" + program
            check_cancel()
            fd = backend.create_link(program, cgroup_fd)
            entry[1] = fd
            stage = "verify_link:" + program
            check_cancel()
            info = backend.verify_link(fd, program, config.cgroup)
            result["links"].append({"program": program, "info": info})
        stage = "check_target"
        check_target()
        check_cancel()
        result["attachment_observed"] = True
        stage = "emit_attached"
        emit({"event": "attached_observer", "generation": str(config.generation),
              "duration_seconds": duration, "identity_capability": "unverified"})
        stage = "observation_wait"
        wait(duration)
        check_cancel()
        finished = True
    except (Exception, KeyboardInterrupt) as exc:
        interrupted = isinstance(exc, (KeyboardInterrupt, InterruptedError))
        result["errors"].append(error_record(stage, exc))
    finally:
        if gated and any(fd is not None for _, fd in links):
            try:
                result["writer_stop"] = backend.stop_writers()
            except (Exception, KeyboardInterrupt) as exc:
                result["errors"].append(error_record("stop_writers", exc))
        for program, fd in reversed(links):
            if fd is None:
                continue
            entry = {"program": program, "fd": fd, "closed": False}
            result["link_close_attempts"].append(entry)
            try:
                backend.close_link(fd)
                entry["closed"] = True
            except (Exception, KeyboardInterrupt) as exc:
                result["errors"].append(error_record("close_link:" + program, exc))
        try:
            if finished and not result["errors"]:
                raw = backend.snapshot()
                result["diagnostic_snapshot"], errors = diagnostic_snapshot(raw, config, profile=profile)
                result["errors"].extend(errors)
        except (Exception, KeyboardInterrupt) as exc:
            result["errors"].append(error_record("diagnostic_snapshot", exc))
        finally:
            try:
                backend.close_object()
            except (Exception, KeyboardInterrupt) as exc:
                result["errors"].append(error_record("close_object", exc))
    result["status"] = "interrupted" if interrupted else (
        "failed" if result["errors"] else "observed_unverified")
    return result
