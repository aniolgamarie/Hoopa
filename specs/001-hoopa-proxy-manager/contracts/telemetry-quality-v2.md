# T134 experimental quality profile v2

Frozen implementation input, 2026-10-23. This profile adds connection quality and
route segments; it does not upgrade process identity or change the stream byte
boundary. Runtime acceptance is recorded separately. Old v1 bytes and decoding
remain supported unchanged. No recovery events or cross-epoch reference graph
are introduced.

## Version and common fields

Every event in a new epoch has literal numeric `schema_version: 2`. All existing
common v1 fields remain required with their existing exact types and limits.
An epoch cannot mix versions. Unchanged event kinds use their v1 extra fields.
Unknown, missing and duplicate fields, including nested fields, are rejected.
Raw bytes are retained for exact replay. The source manifest remains version 1:
its version describes the manifest shape, not event shape. Event version is
fixed by the first frame and validated across the whole epoch.

`opened` additionally requires `flow_start_ns`, a canonical uint64 decimal
string. Nanosecond coordinates are elapsed monotonic time since an origin
captured once in the source process before target parsing. They are comparable
only inside the same source process/epoch; UTC is not used for duration.

## Actual connection and tracker binding

`route_decided` retains the old rule-decision shape. Two phase shapes exist:

- `phase: actual_connection`, `actual_connection_ref`, `ordered_chain`.
- `phase: connection_binding`, `connection_id`, `actual_connection_ref`.

An actual connection ref is a fresh ID on the concrete outer returned Conn,
bound to exactly one flow/attempt before any counted I/O. At most ten connection
segments per flow (the fixed core's retry bound). Tracker binding may only refer
to that flow/attempt's already registered connection and is unique. Failed dial
with no Conn emits no actual-connection event. Selection intent is not a route.

The ordered chain contains 1–8 objects in the core's actual inner-to-outer order:
`internal_name`, `adapter_type`, `provider_name`, `node_id`, `provider_id`,
`group_id`. The three ID fields are ID strings or null. Names/types are nonempty;
provider_name may be empty, in which case provider_id must be null. A mapped
node uses its persistent node ID as internal_name; a mapped group uses its
persistent group ID. Node and group IDs are mutually exclusive. Provider ID,
when present, equals the actual provider_name. Unmapped actual hops retain raw
name/type/provider with null IDs. IDs are resolved and frozen at Conn creation
and actual AppendToChains, never from current group selection at query time.

The opt-in generated bundle mapping is immutable per source generation and
checks internal name, adapter type and actual provider. Display aliases are
separate mapping metadata and do not appear in event identity. Built-in DIRECT
may remain unmapped; generated named direct nodes can carry persistent IDs.
Mapping mismatch is an error, not permission to invent or substitute IDs.
Configured DialerProxy chains retain the actually returned inner hops, including
selected groups, under the source generation. Chains over eight hops are rejected
rather than truncated; unmapped hops prohibit a complete stable-route claim.

## setup_finished

Exact extra fields:

`definition`, `protocol`, `ready_kind`, `outcome`, `reason`, `failure_stage`,
`flow_start_ns`, `attempt_start_ns`, `ready_ns`, `finish_ns`, `duration_ms`,
`flow_duration_ms`, `observed_wait_ms`, `flow_observed_wait_ms`,
`actual_connection_ref`.

- definition is `forward_setup_v2`.
- protocol is `direct_tcp`, `http_connect`, `socks5` or `unknown`.
- ready_kind is `tcp_connected`, `http_connect_accepted`,
  `socks5_connect_accepted` or `ready_unproven`, matching its protocol.
- outcome is `success`, `failure`, `timeout`, `cancelled` or `unknown`.
- All ns/ms values are canonical uint64 strings; ready_ns and the two successful
  duration fields may be null. actual_connection_ref is an ID or null.
- `flow_start_ns <= attempt_start_ns <= finish_ns`. Wait fields equal floor of
  their respective finish-minus-start nanoseconds divided by 1,000,000.
- A proven ready point requires a real connection ref and
  `attempt_start_ns <= ready_ns <= finish_ns`. Successful duration fields equal
  floor of ready-minus-start for attempt and flow. `success` requires proof.
- `ready_unproven` has ready_ns=null and reason=`ready_unknown`; otherwise
  reason is empty. Known failure/timeout/cancelled is retained even when ready
  is unknown. Non-success has null successful durations. `unknown` is only a
  returned connection with no proved ready and no observed error.
- success/unknown use empty failure_stage. Observed errors use a nonempty
  stage (`pre_route`, `dial_or_handshake` or `initial_write`). Raw error text is not emitted.
  Reserved attempt zero represents an aborted pre-dial setup interval: it starts
  at flow_start_ns, uses pre_route, has no actual Conn and precedes any other
  attempts. It prohibits later connection registration or setup for that flow;
  a final rule rejection decision and cumulative terminal snapshot remain valid.
  Emit it only if metadata/routing aborts; a recoverable DNS lookup
  error followed by successful routing is not an aborted setup.
- finish is captured before locking or writing the event. It cannot shift the
  earlier protocol completion point. Flow start equals the opened coordinate;
  attempt starts do not go backwards. Only one setup event per attempt.

Proof is produced by DIRECT after actual target TCP connect, HTTP after complete
accepted CONNECT response (this fixed implementation accepts 200), and SOCKS5
after validated VER/REP/RSV and complete BND response. TFO, uncalibrated/lazy
protocols and unknown outer wrappers cannot claim readiness from an inner TCP
connection. Groups may carry an existing proof; they cannot create one. Only explicitly
transparent callback/tracker wrappers forward evidence. No arbitrary Upstream
search is permitted. A positive lazy first Write plus NeedHandshake changing to
false does not constitute readiness; it only establishes its actual byte count.
HTTP buffered payload survives the response parser and is counted once.
Business TTFB remains unavailable.

## Cumulative connection segments

`bytes_checkpoint` and `closed` retain upload_bytes/download_bytes and require
`segments`, `unallocated_upload_bytes`, `unallocated_download_bytes`.
Each segment has exactly `actual_connection_ref`, `attempt_id`, `upload_bytes`,
`download_bytes`. Counts are canonical uint64 strings; segments are unique,
bounded to ten, and reference earlier actual-connection events from this flow.
Every registered segment appears, including zero-byte segments.

Under one flow lock, actual n from first Write and relay updates both the flow
total and its particular segment (or the explicit unallocated bucket). A
snapshot under that lock must satisfy, separately in both directions:

`flow cumulative = sum(segment cumulative) + unallocated cumulative`.

Every term is monotonic; addition must not overflow. Checkpoint and final are
cumulative snapshots, never additive events. Retried partial writes remain on
their original connection segment. Binding the successful tracker cannot move
earlier bytes. Segment evidence does not count a second ByteFact. Recovery that
only knows v1 flow totals cannot fabricate these dimensions.

## Required checks

Before capability claims: strict positive/negative wire and epoch fixtures;
three protocol readiness boundaries and delayed replies; malformed/truncated
SOCKS5 and rejected HTTP; cancellation, timeout and unknown lazy handshake;
flow DNS delay versus independent attempt duration; actual two-node forwarding,
live group switch with old connection retained, display rename, actual nested
chain; initial partial/error writes and concurrent checkpoint conservation;
receiver and reader replay, new source reopen and old-v1 compatibility.

These local checks do not replace T132 identity, T135 capability rejection,
T137 candidate freeze or T129 formal G03/G05 acceptance.
