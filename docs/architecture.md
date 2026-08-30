# Architecture

## Design goals

EvoOps separates deterministic business control from probabilistic language generation. A model may summarize evidence, but policy selects thresholds, deterministic code constructs actionable signals, and the approval gate decides whether a tool may execute. This makes offline replay meaningful and keeps failures inspectable.

## Runtime sequence

1. `gather_operational_data` invokes typed Eino tools for metrics, inventory, and campaign state.
2. `diagnose_signals` applies the selected versioned policy and creates evidence-linked signals and proposed actions.
3. `retrieve_playbooks` performs lightweight local retrieval through an Eino tool. The interface can be replaced by an embedding/vector retriever without changing the workflow.
4. `synthesize_plan` uses the local synthesizer or an Eino OpenAI-compatible `ChatModel`. A provider error falls back to deterministic text and is retained in the trajectory.
5. `approval_and_execution_gate` executes only actions below the configured risk threshold and persists the remaining action IDs as an approval request.
6. Approval resumes the exact persisted action set and appends a separate trajectory step. Newly generated actions cannot be smuggled into an existing approval.

Each workflow step is stored after completion. A run therefore remains explainable even if a later node fails.

## Trajectory schema

```text
Run
├── request, status, policy_version, timestamps
├── Step[]
│   ├── name, kind, input, output, duration, error
│   └── ToolCall[]
│       └── name, arguments, result, duration, error
├── DiagnosisResult
│   ├── Signal[] -> evidence_ids
│   ├── Evidence[] -> source + stable ref
│   └── Action[] -> risk + approval/execution state
└── pending_approval
```

The current repository writes one JSON document per aggregate with a locked atomic-replace strategy. `repository.Repository` is the boundary for a database implementation.

## Self-evolution boundary

EvoOps does not rewrite its source code. It evolves a constrained policy object:

```text
trace + explicit feedback + observed KPI
                 ↓
failure attribution / conservative candidate proposal
                 ↓
offline replay: signal F1 + safety + retrieval cost
                 ↓
pass → deterministic store-ID canary → explicit promotion
                                   ↘ rollback
```

Candidate generation is intentionally conservative:

- negative feedback raises anomaly thresholds to reduce false positives;
- useful diagnoses with positive KPI outcomes slightly increase sensitivity;
- sparse feedback changes only retrieval depth;
- approval thresholds are not weakened by the optimizer;
- code, permission rules, and tool implementations are immutable to the loop.

Replay scoring weighs signal F1 at 65%, safety at 30%, and retrieval cost at 5%. Safety must equal 1.0 and the total score may not regress more than 0.01 from the active policy.

## Security model

The local API maps `X-EvoOps-Role` to `operator`, `approver`, or `admin`. This is deliberately simple for the standalone demo:

- `approver` or `admin`: approve/reject medium and high-risk operations;
- `admin`: start canary, promote, and rollback;
- all executions remain visible in the trajectory.

A production adapter should validate OIDC/JWT claims, map tenant and resource scope into context, sign approval records, and enforce per-tool argument policies. It should never trust identity headers from the public network.

## MCP boundary

Configured SSE servers are initialized with the official MCP Go SDK. Eino's official MCP adapter converts discovered tools to the same `tool.BaseTool` interface used by local tools. Tool allowlisting happens at discovery time. Risk classification and authorization should happen again at invocation time for defense in depth.

## Failure behavior

- Tool and workflow errors mark the run failed and retain completed steps.
- Model synthesis failure degrades to deterministic text and records the model error.
- A rejected approval marks all pending actions rejected and closes the run.
- Candidate evaluation failure leaves the active and canary versions unchanged.
- Rollback restores the previous active version and clears canary assignment.

