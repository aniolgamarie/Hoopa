# Plan impact v2

Root decision for T050, 2026-10-29. New source/application Plans use schema_version=2. The exact v1 Plan parser and digest vectors remain for historical reads/replay; new source Apply requires v2 after the existing exact-request replay lookup. API transport stays v1. A new plan-v2.schema.json describes the complete v2 shape; the API accepts either Plan shape structurally for Apply/replay and emits v2 for new planning.

The original six impact fields remain. Version 2 additionally requires `details`, containing exactly:

- `scope`: `generation_union` or `known_generation_union`. Full replacement affects all referenced identities in the previous and target generations, not just textually edited declarations. The latter value explicitly means previous generation metadata is unavailable; the listed identities are a known subset and must not be represented as complete.
- `affected_nodes`: sorted unique `{subscription_id,node_id}` pairs, ordered by subscription then node. These are stable provider identities, not display names or a claimed current auto-group selection.
- `affected_groups` and `affected_rules`: sorted unique logical IDs. Top-level `affected_apps` is the sorted union of old/new managed app entry IDs, including removed applications.
- `existing_connections`: `no_previous_generation`, `interrupt`, or `unknown`. The first describes committed-generation state, not a claim that no untracked process or system connection exists.
- `new_connections`: `target_after_activation` or `unknown`, never a migration claim.
- `rollback_target`: `{kind,generation,manifest_sha256}`. `stop_candidate` has both identity fields null for first start. `restore_generation` binds the exact prior active generation and manifest hash. This is the intended recovery target, not evidence that rollback already succeeded.

Unknown connection effects require requires_disruption=true. P1 still produces only start/replace and a full-interruption confirmation; select/none remain unsupported for new admission. Invalid/missing target inputs fail before a usable Plan. Existing blockers remain part of the Plan envelope and admission rejects nonempty blockers.

Previous metadata comes only from the exact integrity-checked active bundle captured under the coordinator lock, with generation and manifest SHA equal to the captured active pointer. It must never use the newest downloaded provider or the candidate Intent as a substitute. Recognized source/app compiler bundles must contain one strict consistent route map; missing, malformed or conflicting required metadata is unavailable, not empty. Older bundles without a supported route projection may be explicitly previous-unknown; no guessed node/group/app identity is returned. Candidate metadata is projected from the fresh complete compilation. These describe stored configuration intent, not actual connection witnesses or runtime health.

Private implementation input `impactGeneration` contains Generation, ManifestSHA256, Known, Nodes ([]PlanImpactNode), Groups/Rules/Apps ([]string). `projectSourceImpact(base config.ActivePointer, previous *impactGeneration, target impactGeneration) (PlanImpact,error)` is pure: it validates identities/bounds, computes detached canonical unions, preserves exact rollback identity, and chooses start/replace only. A nonnil active generation requires matching previous metadata even when Known=false. Missing/bad inputs return no partial impact. Authoritative bundle capture and route-map decoding remain separate coordinator responsibilities.

All details participate in canonical Plan digest and existing deep current-input comparison. Apply recaptures previous authority and recompiles target before effects. Omitted, forged, reordered, stale or mismatched details cannot pass merely by recalculating the client digest. V1 historical requests replay their recorded result before new v2 validation; v1 new source requests are refused without publication.
