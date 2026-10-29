# Application launch store v1 — planned scoped authority

Root decision for T054/T056, 2026-10-29. This is a required implementation contract, not existing storage behavior or acceptance. It expressly extends the configuration Store's single-writer scope for one independent application-lifecycle namespace. No launch writer gets configuration Apply, core ownership, cleanup or general Store write authority.

## Authority and paths

The existing `state/.writer.lock` remains exclusive for active, bundles, operations, core runs, credentials, source assets, draft input and configuration/service state. The sole additional write scope is `state/app-launches/`, with initialization identity in `state/credentials/app-launch-history-v1.json`. A wrapper can append only to a newly allocated launch directory it owns. It cannot write the marker, another launch, any configuration authority, or an existing stage. No generic writable Store is exposed to the wrapper.

| Actor | Lock and permitted writes |
|---|---|
| Explicit init | Existing Store writer; create the namespace identity/lock and publish its independent marker, or verify/resync identical already-installed metadata. No launch-stage updates or reclamation. |
| New launch allocator | Existing namespace `.allocation.lock` exclusive; validate marker/identity, check bounded inventory/capacity, prepare and publish one fresh launch directory. It never acquires the Store writer. |
| One launch wrapper | Its fresh directory's `.owner.lock`, held from allocation until completion/exit; append immutable stage originals only in that directory. It never reopens/reuses a historical ID as a new execution. |
| Read-only inventory | Open existing protected files; shared allocation lock only for a complete directory-list snapshot. No creation, chmod, synchronization, stage writing, owner-lock acquisition, process action or cleanup. |
| Management service | Existing configuration authority remains unchanged. A normal Store open does not install/repair the optional launch namespace, acquire launch-owner locks or mutate launch history. |

Configuration, cache and runtime paths remain separated. `Paths.Validate` must additionally reserve `state/app-launches` and the selected persistent draft record name against Runtime overlap before the namespace can be used. No new `Paths` field or arbitrary caller-selected history directory is introduced. Namespace/entry opens reuse full ancestor, owner, mode, no-follow, regular-file and single-link checks; being beneath State is not a substitute for verifying the new children. The current-user/private-directory isolation boundary remains unchanged.

## Namespace installation

The identity is exact canonical private JSON with `schema_version:1` and `history_id:"apphist-<48 lowercase hex>"`; generate the24-byte identity once with the existing entropy source. The independent credentials marker and `app-launches/.identity.json` contain identical bytes. The precreated `.allocation.lock` is a private regular0600 file; the namespace is0700. Only explicit init can create these names. Cold human control-only startup still prepares only its already-documented empty stopped Store; it does not install launch history.

Publish/sync namespace identity and allocation lock, then sync the namespace and State parent, before publishing/syncing the independent credentials marker. Run/query requires both identities to match and the lock to exist safely. Do not add missing namespace creation to ordinary `OpenStore`, service start, run or read.

- Marker and namespace both absent: explicit init may create them.
- Marker present, directory/identity/lock missing, unsafe or mismatched: recovery/integrity error; do not recreate or generate a new identity.
- Marker absent but namespace present: an idempotent explicit init may finish only when the directory contains exactly a valid immutable identity and optional valid allocation lock, with no final entries, staging entries or unknown files. Reuse the same identity, create the still-missing lock if needed, and establish fresh sync barriers before marker publication. Other partial/ambiguous states require recovery.
- Matching complete installation: identical init establishes fresh metadata durability barriers; no launch content or owner lock is touched.

A file/directory synchronization failure stops further writes through that initializer and is not reported as rollback/success. Persisted immutable inputs are retained. Independent wholesale history erasure by a malicious same-UID actor remains outside the existing file-store isolation claim; ordinary missing evidence never grants permission to reset.

## Allocation, publication and locks

Initially allow at most4096 retained final-plus-staging launch directories, and at most8192 total namespace entries visited. Unknown names/types or an unbounded/unreadable inventory fail closed. No implicit pruning enlarges capacity. Final names are `launch-<48 lowercase hex>`; staging names use `.stage-<48 lowercase hex>`. A complete public inventory enumerates only accepted final names, while capacity counts all retained staging. A staging directory is not a launched-app witness: user dispatch cannot occur until final publication and its parent sync succeed.

Under the existing allocation lock, allocate a fresh exclusive staging directory, create and hold its owner lock, and write/sync the complete reservation and directory. Publish the final ID with no-replace semantics and sync the namespace before returning a launch writer or starting a fixed launcher. An uncertain publication retains its final/staged evidence and cannot dispatch an application. Existing names are never overwritten. A fresh ID collision retries only before any spawn; it never reopens someone else's directory for mutation.

The only overlapping lock order is allocation exclusive → newly created owner lock during allocation. Release allocation before child start or later stage append. A wrapper holding its owner lock must never reacquire allocation or Store writer. Readers do not acquire owner locks. Init holds only Store writer while creating unpublished metadata, and does not take allocation/owner locks after installation. These rules avoid cycles with the persistent management writer. Allocation/read lock waits are bounded and context-cancellable and must return busy/unavailable rather than silently producing a partial inventory.

The owner lock remains in the directory after close. Its absence/unlocked state is not proof that an application or its descendants exited. No reader or new wrapper adopts an old record writer or dispatches from historical evidence. The original owner's lock FD is not inherited by the user application or background descendants.

## Append and read semantics

Reservation, original child birth, dispatch authorization and terminal result are immutable stage originals, bounded at64KiB each. Their exact codecs and reference commitments are finalized with the T056 runner; they must bind history ID, launch ID, profile/entry inputs and original process identities without storing full argv/environment or credentials. A single owner appends by exclusive temporary file, file sync, no-replace publication and directory sync. An error after possible publication is uncertain, not a claim that nothing happened. No existing stage is edited to erase a failure or make a gap look complete.

Only reservation/final-directory durability permits spawn of the fixed internal launcher. Only durable original birth and authorization intent permit a one-use authorization to user exec. A failed/cancelled gate leaves no automatic retry or replay-as-spawn. Parent EOF before authorization cannot execute the app. Full sensitive execution input travels through inherited anonymous descriptors only.

Readers take a bounded snapshot of accepted directory names under shared allocation lock, then read immutable records without taking wrapper-owner locks. They read the highest possible stage before its prerequisites and verify all referenced originals; a concurrent later append may yield an earlier valid prefix, never fabricated completion. Missing terminal evidence leaves root result nil and descendants/managed lease unknown as defined in application-launch-v1. Impossible references, malformed bytes and unreadable authority return explicit errors. No per-launch lookup or full inventory may return an empty/partial success on corruption. Reads never sync, repair or reconcile filesystem state.

This namespace is not the traffic ledger, a census of all descendants, an arbitrary-process control API or a second configuration-operation journal. Existing configuration journal CAS/replay/recovery rules remain untouched. P2 traffic/process attribution must use its own accepted source evidence and cannot substitute these launch labels for a connector identity.
