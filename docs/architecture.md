# Architecture

## System boundary

EvoOps separates probabilistic presentation from deterministic business control. An optional model may synthesize the advertising diagnosis, but policy selects the low-ROI threshold, typed code builds signals and actions, and the approval gate owns side effects. The same compiled Eino workflow is therefore usable for both live execution and offline release evaluation.

```text
online plane                              learning and release plane
────────────                              ──────────────────────────
request                                   labeled Harness suite
  ↓                                              ↓
Eino workflow ── full trajectory ───────→ exact-path replay ×2
  ↓                                              ↓
risk gate                                  six layer scoring
  ↓                                              ↓
approval / execution                      failure attribution
                                                 ↓
                                          allowlisted mutation
                                                 ↓
                                          canary / promote / rollback
```

## Model-driven path (default with credentials)

The Eino Workflow now contains a bounded native tool-calling loop: merchant memory → model-selected read tools → deterministic diagnosis or read-only answer → synthesis → approval gate. The planner exposes only campaign reads and knowledge searches, binds tenant/policy arguments in application code, and returns tool results with matching call IDs. Data/knowledge questions cannot create operations; **all actions in model-driven mode require confirmation**, including low-risk tasks. See [the detailed protocol and examples](tool-calling.md).

`execution_mode` and per-step `model_turns` distinguish this path from the offline fixed workflow. Replay uses whichever path the application is configured to use; it never approves or executes a model-path operation. The planner prompt is not part of automatic summary-prompt evolution.

## Fixed online sequence (no credentials or Tool Calling disabled)

1. `gather_campaign_data` invokes one typed Eino tool for campaign status, spend, revenue, current ROI, and previous ROI.
2. `load_merchant_memory` reads the store-scoped memory profile. Each fact retains its feedback, run, confidence, and timestamp lineage.
3. `diagnose_campaign_roi` ignores inactive campaigns, compares active plans with the selected policy threshold, and creates evidence-linked signals. Each low-ROI plan receives a low-risk attribution-review task and a high-risk pause proposal. Matching memory can reorder and annotate those actions, but cannot alter risk, tool arguments, or approval policy.
4. `retrieve_ad_playbook` queries the advertising corpus. The deterministic backend supports offline replay; external mode uses text-embedding-v3, Milvus BM25, weighted RRF, Qwen reranking, and PostgreSQL/Redis-backed L3→L2→L1 merging.
5. `synthesize_ad_plan` uses a local synthesizer or an Eino OpenAI-compatible `ChatModel`. The policy's prompt revision selects real grounding instructions. Provider failure falls back to deterministic text and stays visible in the trajectory.
6. `approval_and_execution_gate` automatically executes only the diagnostic task. Campaign suspension remains in a durable approval request.
7. Approval resumes the exact persisted action IDs. Newly generated work cannot be inserted into an existing approval.

Replay supplies a caller-selected policy, runs the same graph, suppresses repository writes, replaces side effects with `would_execute`, and uses an isolated memory snapshot. Versioned memory fixtures let longitudinal Harness cases exercise personalization without allowing local feedback to contaminate a release evaluation.

## Merchant memory and feedback learning

Feedback action IDs must resolve to actions from the referenced run; clients cannot inject arbitrary operations. The learner emits three typed facts: diagnosis episodes, explicit action preferences, and observed action outcomes. `observed_kpis` values are normalized improvement deltas, so positive values mean improvement and negative values mean regression.

Profiles are isolated by store ID and persisted under hashed filenames. A later live run selects the most specific explicit preference for an operation/target before considering observed outcomes. The selected fact appears both on the action as `memory_refs` and in the evidence chain as `merchant_memory`. Harness fixtures verify preference application and the ordinary safety layer independently verifies that guarded actions still wait for approval.

## Retrieval pipeline

```text
article
  ├─ parent chunk
  └─ sentence leaves

query ─→ optional rewrite ─┬→ deterministic dense ranking ─┐
                           └→ BM25 sparse ranking ─────────┤
                                                          ↓
                                                  weighted RRF
                                                          ↓
                                                  candidate budget
                                                          ↓
                                                  parent auto-merge
                                                          ↓
                                             phrase-aware reranking
                                                          ↓
                                             top-K + retrieval trace
```

The local dense channel uses feature hashing plus a small commerce synonym map for deterministic CI. `dataset.KnowledgeSearcher` is the runtime boundary: external mode implements it with DashScope embeddings/reranking, Milvus leaf search, PostgreSQL parent/version storage, and Redis parent caching while preserving the same tool result and retrieval trace. See [docs/rag.md](rag.md).

The trace retains original/effective queries, dense/sparse/fused/final rankings, merged IDs, scores, latency, and cost units. Retrieval quality can therefore be diagnosed independently of downstream answer quality.

## Trajectory schema

```text
Run
├── mode, request, status, policy_version, timestamps
├── Step[]
│   ├── name, kind, input, output, duration, error
│   └── ToolCall[]
│       └── name, arguments, result, duration, error
├── DiagnosisResult
│   ├── Signal[] → evidence_ids
│   ├── Evidence[] → source + stable ref
│   └── Action[] → risk + approval/execution state
└── pending_approval
```

The repository writes one JSON document per aggregate with a process lock and atomic replacement. `repository.Repository` is the boundary for database-backed event and checkpoint storage.

## Harness and reproducibility

Each case executes twice. A normalized SHA-256 fingerprint includes workflow structure, tool arguments and stable results, signal classifications, action structure, and retrieval rankings while excluding UUIDs, timestamps, and latency. The trajectory layer hard-fails when the two fingerprints differ.

The aggregate release decision requires every case and layer to pass. A candidate is also blocked if total score falls more than `0.01`, or a layer falls more than `0.03`, below a freshly replayed active baseline. See [harness.md](harness.md).

## Self-evolution boundary

The mutable policy surface includes the campaign ROI threshold, hybrid-retrieval parameters, query-rewrite/rerank controls, grounded prompt revision, workflow budgets, cost budget, and approval threshold. Failure attribution emits allowed fields; candidate generation enforces that allowlist in code.

Source code, tool implementations, identity adapters, and arbitrary tool arguments remain outside the loop. Safety attribution may tighten approval to `low`; no automated path relaxes it.

A passing candidate stores a release credential:

- Harness report ID;
- Harness suite version;
- active policy version used as baseline;
- evaluation timestamp.

Canary and promotion reject missing or stale credentials. Promotion additionally requires canary status. See [self-evolution.md](self-evolution.md).

## Security model

The local API maps `X-EvoOps-Role` to `operator`, `approver`, or `admin`:

- `approver` or `admin` can approve/reject guarded actions;
- `admin` can evaluate, canary, promote, and rollback policies;
- all decisions remain in the trajectory or evolution record.

These headers are a standalone-demo adapter. A production boundary must verify OIDC/JWT claims, tenant/resource scope, signed approvals, and per-tool argument policies.

## Failure behavior

- Tool/workflow errors fail the run while retaining completed steps.
- Model synthesis failure falls back to deterministic output and remains a trajectory error for Harness inspection.
- Rejected approvals close the run and mark the exact pending actions rejected.
- Harness failure persists the report and attribution but leaves active/canary state unchanged.
- Invalid or stale release credentials block rollout.
- Rollback restores the previous active policy and clears canary assignment.
