# Architecture

## System boundary

EvoOps separates probabilistic presentation from deterministic business control. An optional model may synthesize a narrative, but policy selects anomaly thresholds, typed code builds signals and actions, and the approval gate owns side effects. The same compiled Eino workflow is therefore usable for both live execution and offline release evaluation.

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

## Online sequence

1. `gather_operational_data` invokes typed Eino tools for metrics, inventory, and campaign state.
2. `diagnose_signals` applies the selected versioned policy and creates evidence-linked signals and proposed actions.
3. `retrieve_playbooks` queries a multi-level corpus. Dense and BM25 rankings are fused with weighted RRF; related leaves can merge to their parent; deterministic reranking favors exact business phrases.
4. `synthesize_plan` uses a local synthesizer or an Eino OpenAI-compatible `ChatModel`. Provider failure falls back to deterministic text and stays visible in the trajectory.
5. `approval_and_execution_gate` executes only actions below the configured risk threshold. Medium/high-risk actions remain in a durable approval request.
6. Approval resumes the exact persisted action IDs. Newly generated work cannot be inserted into an existing approval.

Replay supplies a caller-selected policy, runs the same graph, suppresses repository writes, and replaces side effects with `would_execute`. This avoids maintaining a second evaluator implementation that can drift from production.

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

The local dense channel uses feature hashing plus a small commerce synonym map for deterministic CI. `dataset.Repository` and the retrieval result/trace schema are the replacement boundaries for production embeddings, Milvus, Elasticsearch, or another vector backend.

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

The mutable policy surface includes anomaly thresholds, hybrid-retrieval parameters, query-rewrite/rerank controls, prompt/routing revision, workflow budgets, cost budget, and approval threshold. Failure attribution emits allowed fields; candidate generation enforces that allowlist in code.

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
