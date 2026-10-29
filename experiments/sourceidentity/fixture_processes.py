"""Bounded, single-owner transport for at most three fixture children.

Only locally retained Popen objects are polled or killed. Calls are single-threaded;
signal handlers must record cancellation rather than interrupt these operations.
"""

import math
import os
import selectors
import subprocess
import time


class ProcessError(RuntimeError):
    """A child could not be managed or exceeded the output bound."""


class ChildSet:
    OUTPUT_LIMIT = 65536

    def __init__(self, clock=time.monotonic_ns, popen=subprocess.Popen,
                 selector_factory=selectors.DefaultSelector):
        self._clock = clock
        self._popen = popen
        self._selector = selector_factory()
        self._children = {}
        self._closed = False
        self._close_errors = []

    def spawn(self, label, argv):
        if self._closed:
            raise ProcessError("child set is closed")
        if not isinstance(label, str) or not label or label in self._children:
            raise ProcessError("child label must be nonempty and unique")
        if len(self._children) >= 3:
            raise ProcessError("at most three children are allowed")
        if (not isinstance(argv, list) or not argv
                or any(not isinstance(arg, str) for arg in argv)):
            raise ProcessError("argv must be a nonempty list of strings")
        started = str(self._clock())
        try:
            proc = self._popen(list(argv), stdin=subprocess.DEVNULL,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               shell=False, bufsize=0)
        except Exception as exc:
            raise ProcessError(f"{label}: spawn failed: {exc}") from exc
        # Retain ownership before any stream setup that can fail.
        child = {"proc": proc, "streams": {}, "view": {
            "label": label, "argv": list(argv), "pid": proc.pid,
            "started_monotonic_ns": started,
            "observed_exit_monotonic_ns": None, "exit_code": None,
            "stdout": b"", "stderr": b"", "stdout_eof": False,
            "stderr_eof": False, "truncated": False}}
        self._children[label] = child
        try:
            for name in ("stdout", "stderr"):
                stream = getattr(proc, name)
                if stream is None:
                    raise ValueError(f"missing {name} pipe")
                child["streams"][name] = stream
                os.set_blocking(stream.fileno(), False)
                self._selector.register(stream, selectors.EVENT_READ, (label, name))
        except Exception as exc:
            raise ProcessError(f"{label}: pipe setup failed: {exc}") from exc

    def _observe(self, label, child):
        if child["view"]["exit_code"] is not None:
            return
        try:
            code = child["proc"].poll()
        except Exception as exc:
            raise ProcessError(f"{label}: poll failed: {exc}") from exc
        if code is not None:
            child["view"]["exit_code"] = code
            child["view"]["observed_exit_monotonic_ns"] = str(self._clock())

    def pump(self, timeout):
        if self._closed:
            raise ProcessError("child set is closed")
        if (not isinstance(timeout, (int, float)) or not math.isfinite(timeout)
                or not 0 <= timeout <= 0.1):
            raise ProcessError("pump timeout must be between zero and 0.1 seconds")
        try:
            for label, child in self._children.items():
                self._observe(label, child)
            events = self._selector.select(timeout)
            # One read per ready stream; never more than sixteen reads per call.
            for key, _ in events[:16]:
                label, name = key.data
                child = self._children[label]
                view = child["view"]
                try:
                    data = os.read(key.fileobj.fileno(), 4096)
                except BlockingIOError:
                    continue
                if not data:
                    view[name + "_eof"] = True
                    self._selector.unregister(key.fileobj)
                    continue
                remaining = self.OUTPUT_LIMIT - len(view[name])
                view[name] += data[:remaining]
                if len(data) > remaining:
                    view["truncated"] = True
                    raise ProcessError(f"{label}: {name} exceeded {self.OUTPUT_LIMIT} bytes")
            for label, child in self._children.items():
                self._observe(label, child)
        except ProcessError:
            raise
        except Exception as exc:
            raise ProcessError(f"child transport failed: {exc}") from exc

    def views(self):
        return {label: dict(child["view"], argv=list(child["view"]["argv"]))
                for label, child in self._children.items()}

    def close(self):
        if self._closed:
            return list(self._close_errors)
        errors = self._close_errors

        def record(message):
            if message not in errors:
                errors.append(message)

        # These are synchronous syscalls: the deadline bounds waiting/draining,
        # not an OS-level stall inside kill, poll, read, or close itself.
        deadline = time.monotonic() + 2.0
        try:
            for label, child in self._children.items():
                try:
                    self._observe(label, child)
                    if child["view"]["exit_code"] is None:
                        child["proc"].kill()
                except Exception as exc:
                    record(f"{label}: cleanup kill/poll failed: {exc}")
            for _ in range(40):
                if all(c["view"]["exit_code"] is not None
                       and c["view"]["stdout_eof"] and c["view"]["stderr_eof"]
                       for c in self._children.values()):
                    break
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                try:
                    self.pump(min(0.05, remaining))
                except Exception as exc:
                    record(str(exc))
            for label, child in self._children.items():
                try:
                    self._observe(label, child)
                except Exception as exc:
                    record(str(exc))
                if child["view"]["exit_code"] is None:
                    record(f"{label}: child exit could not be confirmed")
                for name in ("stdout", "stderr"):
                    if not child["view"][name + "_eof"]:
                        record(f"{label}: {name} EOF could not be confirmed")
        finally:
            self._closed = True
            for label, child in self._children.items():
                # Both handles belong to the Popen, even if setup failed early.
                seen = set()
                for name in ("stdout", "stderr"):
                    stream = getattr(child["proc"], name, None)
                    if stream is not None and id(stream) not in seen:
                        seen.add(id(stream))
                        try:
                            stream.close()
                        except Exception as exc:
                            record(f"{label}: {name} close failed: {exc}")
            try:
                self._selector.close()
            except Exception as exc:
                record(f"selector close failed: {exc}")
        return list(errors)
