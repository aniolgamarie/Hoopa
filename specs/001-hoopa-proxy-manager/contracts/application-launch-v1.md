# Application launch v1 — T053 executable definition boundary

Root decision, 2026-10-29. T139 and T052 definitions are complete. This contract fixes the next actual CLI tests and the required T054/T056/T057 behavior; no product launch implementation is claimed yet. The independent private PTY spike has accepted limited normal-04/race-02 observations after correcting a foreground-restore-after-reap identity gap. Its final ordering retains the original child as an unreaped zombie during restoration; the original failed ordering and observations remain recorded. The spike does not implement persistent launch records or application admission.

## Command and result

`hoopa run --app ID [--mode managed|inherit|direct] [--meter] -- ARGV...` requires a nonempty argv array after the separator. It executes that array directly, without a shell expansion. Child stdin/stdout/stderr are the actual inherited file descriptors; application output remains application output, without a management JSON envelope or Hoopa progress text on stdout. Normal application stderr is likewise passed through. Controlled Hoopa failures use bounded constant stderr diagnostics; full argv, inherited environment, proxy credentials and untrusted errors are not printed or persisted.

When the root application has a normal terminal result and its bookkeeping completes, Hoopa returns its exit code, or128+signal for signal termination. Root normal exit and signal termination remain distinct in the retained result, so an application deliberately returning130 is distinguishable from SIGINT. A failure to establish the configured mode/entry is a launch failure before dispatch, never fallback. Invalid invocation/mode or unsupported metering returns2; unavailable profile/entry returns4; integrity/recovery or bookkeeping failure returns5. After an actual child exits, a terminal-record failure must report the observed root exit/signal in a bounded diagnostic while returning5, rather than pretending the history was durably completed. Application exec failure is distinguishable through the fixed launcher's bounded failure channel; conventional126/127 can represent unavailable/not-found execution, with the actual declared reason retained. No API management operation executes arbitrary argv.

The first tested refusal diagnostics are exact single stderr lines: `hoopa: INVALID_ARGUMENT\n` (2), `hoopa: METERING_MODE_INVALID\n` (2), `hoopa: METERING_UNSUPPORTED\n` (2), `hoopa: APP_NOT_FOUND\n` (4), `hoopa: PROFILE_UNAVAILABLE\n` (4), and `hoopa: MANAGED_ENTRY_UNAVAILABLE\n` (4). The latter covers unavailable core/service/qualified entry without copying their private errors. Successful app output is unchanged. An existing executable file of invalid format must reach the fixed launcher's exec attempt, fail without any shell fallback, return126 with `hoopa: EXEC_FORMAT\n`, and retain `DispatchState=exec_failure_reported`, `ExecFailureReason=exec_format` and the original launch-root terminal result. Format detection is not silently substituted by a different shell or application. Other finite failure reasons must be specified and tested when introduced; untrusted OS error text is never the public diagnostic.

The override changes only this invocation's mode. `--meter` may enable a requested metered launch but cannot disable configured metering; incompatible modes and P1's unsupported managed metering both refuse before dispatch. Environment construction follows [child-environment-v1](child-environment-v1.md).

## Profile and entry admission

Managed launch consumes the committed Intent/generation/manifest and the exact bundled app binding, independently checked against the route map and the named entry. It cannot use an unapplied candidate, another app's credentials or the shared default entry. The initially supported path requires a currently verified core from the management service and its configured local authenticated entry; no service/core is implicitly started. The explicit run may authenticate to its bound local listener without sending a business request. Unavailable evidence, absent app/entry, missing history or incompatible metering fails before a user application executes.

A captured profile revision describes this launch's inputs, not the actual route of every later connection. A separately confirmed Apply may interrupt/replace forwarding under the existing impact contract. A run never updates providers, selects a group, reloads the core, allocates a new listener or changes prior launches/leases. Multiple launches of an already committed unmetered profile may share its immutable named entry. Requesting unsupported metering for a new launch leaves the original launch and its held connection unchanged.

Direct/inherit do not require a service or core. They may use an explicitly initialized local draft or the committed profile; the launch override does not modify either. T053's initial fixture uses committed profiles and mode overrides, and T054/T058 separately establish the draft path and full initialization journey.

## Terminal and signal behavior

Use a waiting wrapper, original stdin/stdout/stderr file descriptors and a new child process group. When stdin is the wrapper's controlling terminal and its foreground group is the wrapper's current group, the child uses Go's `SysProcAttr.Foreground`/`Ctty` path. Normal terminal Ctrl-C reaches the foreground child group naturally. A non-controlling/background terminal conflict refuses before application dispatch; ordinary pipe/file input needs no terminal transition. The prototype and T053 must explicitly test normal terminal read/write, foreground identities, Ctrl-C and restoration, not merely isatty.

The wrapper retains the original raw clone-created pidfd and forwards INT/TERM/HUP/QUIT received by its own PID only to that direct child through pidfd_send_signal. It never broadcasts to an unverified numeric process group or discovers and kills guessed descendants. Require pidfd support; no silent numeric-PID signal fallback. Original child exit is first observed through the retained pidfd without reaping. For a TTY run, restore is attempted while this original child remains an unreaped zombie, preventing its PID/group-leader number from being reused before the check. Only then is the original command waited exactly once, and the retained raw pidfd closed. No earlier Wait goroutine may silently reap it. A signal race with terminal exit may produce ESRCH and must not target a replacement process.

On ordinary completion/failure, require the retained TTY to report the saved controlling-session ID and this original unreaped child's foreground group before restoring the saved group. An observed competing foreground/session owner is a conflict, not permission to overwrite it. The terminal UAPI has no cross-process compare-and-swap: this initial path covers ordinary cooperative session ownership and does not claim atomic protection against an external actor changing foreground between the check and ioctl. The restore ioctl blocks SIGTTOU on a locked OS thread and restores the original mask; no global signal.Ignore change. Application signal receipt and descendant completion are different facts. Full shell job control (Ctrl-Z/fg/bg), arbitrary escaped sessions and every possible terminal arrangement are not promised by this initial path.

File descriptors are passed as files, not replaced with Go io-copy goroutines that make Wait depend on all descendant pipe writers closing. Hoopa can report its direct root's terminal result while a background descendant still holds stdout/stderr. The caller may still wait for stream EOF; that does not mean the wrapper itself is running.

## Persistent history and dispatch authorization

Use the dedicated bounded private DB-free namespace and explicit partitioned write authority specified in [application-launch-store-v1](application-launch-store-v1.md), initialized by T054 under the existing protected state roots. It is separate from configuration operations: it cannot publish active, consume the manager's global writer lock as a competing process, or create a second Apply/recovery workflow. A short namespace allocation lock bounds capacity and exclusive fresh random launch-ID creation; each launch has one wrapper-owned record directory/lock. Neither read nor run recreates a missing namespace. Existing launch IDs and uncertain entry leases are never reused or silently pruned.

Before spawn, durably reserve the launch identity, profile/entry references and wrapper identity. A fixed internal app launcher receives sensitive argv/environment only through private inherited anonymous descriptors, and waits for a one-use authorization. The parent obtains the original child pidfd/PID/boot/start identity and durably records the original child plus dispatch intent before authorizing user execution. Parent EOF before authorization cannot execute the application. After authorization, missing terminal evidence means unknown, not proof that nothing ran. Do not use a PID file or port as subsequent adoption/signal authority.

After the original Wait, persist its exact terminal result and timestamps with the original launch identity. Direct root completion does not establish descendant/connection completion. In this first profile, descendants remain `unknown` with `not_enumerated` unless separately supported evidence is added; the test fixture's independent knowledge is not a product census. A managed lease after root exit remains `unknown`, retaining its original profile/entry identity. Direct/inherit have no managed entry lease. Read-only inspection does not repair, reopen a writer, collect hidden state or reclaim a lease. Interrupted stages remain explicitly incomplete/unknown. Root owns the exact codec, write ordering, limits and failure tests before T056 implementation.

For test-definition integration, the narrow metadata surface is:

```go
func (c *Coordinator) InitializeLaunchHistory(ctx context.Context, expected config.Revision) error
func ReadLaunches(ctx context.Context, paths config.Paths) ([]LaunchView, error)

type LaunchView struct {
    LaunchID config.ID
    AppID config.ID
    Mode string
    Metering bool
    ProfileRevision string
    ProfileGeneration *config.ID
    EntryBindingSHA256 *string
    DispatchState string // "not_authorized", "authorization_recorded", or "exec_failure_reported"
    ExecFailureReason *string // nil unless the fixed launcher reports a validated finite exec failure
    Root *LaunchRootView
    DescendantsState string // initial profile: "unknown"
    DescendantsReason string // "not_enumerated"
    EntryLeaseState string // direct/inherit: "none"; managed: "reserved", "ready", or "unknown"
}
type LaunchRootView struct {
    PID int
    BootID string
    StartTicks uint64
    ExitCode *int
    Signal *int
    WaitStatus *uint32
}
```

These are protected local metadata use cases, not a new versioned public JSON API. The root identity is the original application's launch root incarnation; it does not identify which descendant opened or wrote a later network connection. The dispatch field records authorization/failure evidence, not proof that an arbitrary program reached its main function. EOF on a close-on-exec channel can also accompany pre-exec signal death; it must not alone be reported as successful application execution. Missing root/terminal fields remain nil, never zero-filled. For normal terminal exit, ExitCode is present and Signal nil; for signal termination the reverse. WaitStatus is present only with a valid terminal record. `ReadLaunches` returns a bounded complete inventory or an error, not a partial empty success. Actual root liveness and descendant liveness must not be inferred from the absence of an exit record. The initial tests use explicit initialization primitives; T057/T064 will project this metadata for users without exposing private raw records.

## T053 actual test definitions

Use separately pinned actual `cmd/hoopa` and a separately built deterministic application fixture. Reuse T139 only for explicit prepared source/app inputs and genuine Plan/Apply setup; do not reuse its JSON invocation helper for run or substitute a parent's `h.fresh` request for application traffic.

1. Literal argv containing spaces, quotes, shell metacharacters and a synthetic secret arrives unchanged; no shell side effect occurs. Real pipe input and stdout/stderr remain exact, exit17 is retained/returned, and no product file stores full argv/environment. An executable invalid-format fixture separately exercises the real exec-failure channel, exact126/EXEC_FORMAT result and retained finite reason without executing its sentinel as a shell script. Parent environment and unrelated state remain unchanged.
2. Send TERM to the original wrapper handle after the real child reports readiness. Verify the direct child's signal result separately from numeric exit143, and prove a same-group sentinel is not killed by a group broadcast. Test normal exit130 separately from SIGINT.
3. An isolated actual PTY fixture owns its session/controlling terminal. Verify TTY stdin/out/err and foreground identity, exercise normal read/write/exit17 and actual terminal Ctrl-C, and prove the prior foreground group is restored. Fixture controls only its original child handles; no host terminal or unrelated session is touched.
4. The root forks a bounded-life background child retaining stdio and exits. Observe wrapper exit without requiring stream EOF, independently establish the background child is still alive, and inspect retained root result plus unknown descendants/managed lease. Its automatic TTL/verified test cleanup is not product completion evidence.
5. A real managed child uses the captured entry, while an earlier managed launch/held connection remains unchanged. A missing app/entry or unsupported metering launch cannot execute its sentinel. Core identity, active generation, immutable entry, existing launch records and old traffic stay consistent; no run-triggered reload or implicit migration is accepted.

T053's milestone is executable definitions with honest expected RED until T054–T057 exist. Actual Linux normal/race acceptance, complete first-use initialization and a real Agent workflow remain T058. The private terminal spike is supporting feasibility evidence only. No original traffic PID attribution, GOST/SQLite metering, all-descendant cleanup, Windows/macOS support or resource-budget acceptance follows from these definitions.
