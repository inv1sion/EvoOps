<div align="center">

# ⚡ EvoOps

### A governed self-evolving Agent for advertising ROI

**Traceable decisions · Exact-path replay · Prompt evolution · Human-controlled execution**

<p>
  <a href="README.en.md"><img src="https://img.shields.io/badge/Read_in-English-1683ff?style=for-the-badge" alt="Read in English"></a>
  <a href="README.md"><img src="https://img.shields.io/badge/阅读-简体中文-ef4444?style=for-the-badge" alt="阅读简体中文"></a>
</p>

<p>
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.25+">
  <img src="https://img.shields.io/badge/CloudWeGo-Eino-00A6FF?style=flat-square" alt="CloudWeGo Eino">
  <img src="https://img.shields.io/badge/Evaluation_Harness-6_Layers-7C3AED?style=flat-square" alt="Six-layer Evaluation Harness">
  <img src="https://img.shields.io/badge/LLM-Qwen-FF6A00?style=flat-square" alt="Qwen">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache--2.0-22c55e?style=flat-square" alt="Apache-2.0"></a>
</p>

[Highlights](#why-this-project-is-different) · [Self-evolution](#core-loop) · [Evaluation](#evaluation-harness) · [Architecture](#runtime-architecture) · [Quick start](#quick-start) · [API](#http-api)

</div>

---

EvoOps is a traceable, self-evolving advertising ROI agent built with Go and [CloudWeGo Eino](https://github.com/cloudwego/eino). It reads active campaign performance, detects plans below a versioned ROI threshold, creates a low-risk attribution-review task, and guards campaign suspension behind human approval. Its center of gravity is the engineering loop that turns production trajectories into constrained policy candidates and blocks unsafe or regressive candidates with a reproducible six-layer Evaluation Harness.

The included synthetic stores cover four focused advertising cases: healthy delivery, an already-paused plan, an active low-ROI plan, and store-scoped preference memory. The framework boundary remains reusable for other decision-and-execution agents without mixing those domains into this demo.

## At a glance

| Capability | Implementation |
|---|---|
| Agent runtime | Go + CloudWeGo Eino compiled workflow and typed tools |
| Evolution target | Versioned Prompt artifacts and allowlisted policy parameters |
| Evaluation | Six-layer Harness plus independent LLM-as-Verifier/Judge |
| Retrieval | Dense + BM25, weighted RRF, parent auto-merge, reranking |
| Learning | Store-scoped long-term memory built from explicit feedback and KPI outcomes |
| Governance | Immutable safety boundary, approval gate, canary, release credential, rollback |

## Why this project is different

- **Exact-path replay:** the Harness invokes the same compiled Eino workflow and typed tools as the live agent, with side effects replaced by `would_execute`.
- **Complete trajectory:** every node, tool input/output, retrieval ranking, latency, evidence reference, action state, policy version, and approval event is persisted.
- **Merchant memory and feedback learning:** explicit action feedback and normalized KPI outcomes become tenant-scoped, source-linked memory facts that personalize later plans without changing risk or bypassing approval.
- **Hybrid hierarchical retrieval:** a deterministic dense channel and BM25 sparse channel are fused with weighted RRF, followed by parent auto-merge, business-phrase reranking, and policy-controlled query rewriting.
- **Six-layer Harness:** retrieval, trajectory, tool safety, business outcome, LLM-verifier/judge quality, and cost/latency are scored separately. Safety, reproducibility, and semantic grounding are hard gates.
- **Constrained Prompt self-evolution:** failure attribution and Judge feedback are passed to an Eino Prompt Optimizer. It generates a bounded Prompt Patch; EvoOps persists the full prompt text, parent version, model, rationale, evidence, and static validation result before exact-path candidate replay. It never rewrites the immutable approval, evidence, memory, or tool-permission boundary.
- **Release credentials:** an evaluated policy records the Harness report, suite version, and active baseline it passed against. Stale candidates cannot enter canary or promotion.
- **Human control:** medium/high-risk operations pause durably, canary assignment is deterministic, promotion is explicit, and rollback restores the previous policy.
- **Offline-capable:** deterministic diagnosis and the original five structural layers run without an API key. With a credential, an independent Eino model synthesizes the answer and Qwen Max verifies facts and judges response quality as the sixth layer.

The retrieval and observability ideas were adapted from my earlier [MedQA-Agentic-RAG](https://github.com/inv1sion/MedQA-Agentic-RAG) project. EvoOps reimplements the online system in Go/Eino and adds the self-evolution Harness, policy governance, release gates, and commerce evaluation set.

## Core loop

```mermaid
flowchart LR
    A[Live trajectory + feedback + KPI] --> B[Active-policy Harness]
    B --> C[Failure attribution]
    C --> D[Allowed mutation set]
    D --> P[LLM Prompt Patch + static safety validation]
    P --> E[Versioned full Prompt candidate]
    E --> F[Candidate exact-path replay ×2]
    F --> G{Six-layer gate}
    G -->|blocked| H[Persist evidence and failure class]
    G -->|pass| I[Attach release credential]
    I --> J[Deterministic canary]
    J --> K{Human decision}
    K -->|promote| L[New active policy]
    K -->|regress| M[Rollback]
    L --> A
```

Self-evolution here means governed optimization, not uncontrolled source-code rewriting. Candidate mutations are small, explainable, replayable, and reversible.

## Evaluation Harness

| Layer | What is measured | Gate behavior |
|---|---|---|
| Retrieval | Precision@K, Recall@K, MRR, NDCG, rewrite use, retrieval cost | Minimum recall and quality score |
| Trajectory | Eino node sequence, required-tool recall, step/tool budgets, errors, dual-replay fingerprint | Hard gate |
| Safety | Forbidden operations that could bypass approval | Hard gate; any violation blocks |
| Outcome | Signal F1, action F1, weighted business-utility coverage | Minimum outcome quality |
| Model quality | Grounding, numeric accuracy, action support, completeness, approval disclosure | Hard gate; unsupported claims or numeric errors block |
| Cost | Tool/model/retrieval cost units and end-to-end node latency | Policy and case budgets |

Candidate score must also remain within the total and per-layer regression tolerances of a freshly replayed active baseline. See [docs/harness.md](docs/harness.md) for schemas, formulas, and extension instructions.

Every evolution run also persists a compact baseline/candidate comparison: case pass rate, model-quality score, groundedness, numeric accuracy, average workflow latency, cost units, safety violations, Prompt lineage, and gate decision. A real four-case Qwen experiment is documented in [docs/evaluation-results.md](docs/evaluation-results.md); it proves a passing no-regression Prompt mutation, not a fabricated quality uplift from an already-perfect synthetic baseline.

## Runtime architecture

```mermaid
flowchart LR
    Q[Advertising ROI question] --> W[Eino Workflow]
    W --> C[Campaign performance tool]
    W --> L[Store-scoped merchant memory]
    C --> D[Deterministic low-ROI diagnosis]
    L --> D
    D --> R[Advertising playbook RAG]
    R --> S[Local or Eino LLM ad summary]
    S --> G{Risk gate}
    G -->|low risk| X[Execute tool]
    G -->|medium / high| H[Durable approval]
    H --> X
    W --> T[(Trajectory store)]
    X --> T
```

The agent can discover allowlisted MCP SSE tools through Eino's official MCP adapter. Local demo identity headers model the authorization boundary; a real deployment should replace them with verified OIDC/JWT claims and tenant-scoped tool policies.

## Quick start

Requirements: Go 1.25 or newer.

```bash
go test ./...
go run ./cmd/evoops demo
go run ./cmd/evoops harness
go run ./cmd/evoops evolve -canary 10
go run ./cmd/evoops serve
```

Open <http://localhost:8080>. Use `demo-store` for the main low-ROI and memory flow, or `healthy-store`, `paused-store`, and `low-roi-store` for isolated cases.

The console opens in **Advertising Assistant** mode with a compact question → low-ROI plans → actions experience. Switch to **Agent Lab** to inspect evidence, Eino trajectories, tenant-scoped memory, and persisted self-evolution reports. This keeps engineering observability available without exposing it in the merchant's default workflow.

The `evolve` command executes:

```text
active baseline replay
  → failure attribution
  → LLM-generated Prompt Patch
  → immutable-boundary validation
  → allowlisted policy mutation with full Prompt artifact
  → candidate replay twice per case
  → baseline/layer regression gates
  → optional canary
```

It never promotes automatically. Promotion requires a separate admin action after canary assignment.

The repository defaults to Alibaba Cloud Model Studio's OpenAI-compatible endpoint. The live agent uses `qwen3.7-flash-2026-07-15`; the independent verifier/judge uses `qwen3.7-max-2026-06-08`. Put the credential in the gitignored local `.env` file or inject it through the process environment:

```bash
export OPENAI_API_KEY=your-key
go run ./cmd/evoops serve
```

On PowerShell, use `$env:OPENAI_API_KEY = "..."` instead of `export`. Process variables take precedence over `.env`; `OPENAI_BASE_URL`, `OPENAI_MODEL`, `EVOOPS_JUDGE_MODEL`, `EVOOPS_PROMPT_OPTIMIZER_MODEL`, and `EVOOPS_LLM_EVAL_ENABLED` remain available as deployment-time overrides. The Prompt Optimizer defaults to `OPENAI_MODEL`. Never commit a live key; `.env` is ignored by Git and excluded from Docker build context.

## Useful commands

```bash
# Evaluate the active policy.
go run ./cmd/evoops harness

# Evaluate a stored candidate against a fresh active baseline.
go run ./cmd/evoops harness -policy CANDIDATE_VERSION

# Run the closed loop and assign 10% deterministic canary traffic if it passes.
go run ./cmd/evoops evolve -canary 10

# Verify production readiness of this repository.
go test ./...
go vet ./...
go build ./cmd/evoops
```

## HTTP API

| Method | Endpoint | Purpose |
|---|---|---|
| `POST` | `/api/runs` | Run a live diagnosis |
| `POST` | `/api/runs/stream` | Receive a run as SSE events |
| `GET` | `/api/runs/{id}` | Read the full trajectory |
| `POST` | `/api/runs/{id}/approve` | Resume or reject guarded actions |
| `POST` | `/api/runs/{id}/feedback` | Store usefulness, action, and KPI feedback |
| `GET` | `/api/stores/{id}/memory` | Read the tenant-scoped auditable memory profile |
| `GET` | `/api/harness/reports` | List persisted multi-layer reports |
| `POST` | `/api/harness/run/{version}` | Evaluate a policy against the active baseline |
| `POST` | `/api/evolution/run` | Execute attribution → candidate → Harness → optional canary |
| `GET` | `/api/evolution/runs` | List complete evolution records |
| `POST` | `/api/evolution/canary/{version}` | Assign deterministic canary traffic |
| `POST` | `/api/evolution/promote/{version}` | Promote a canaried policy with a valid release credential |
| `POST` | `/api/evolution/rollback` | Restore the previous active policy |

Approval example:

```bash
curl -X POST http://localhost:8080/api/runs/RUN_ID/approve \
  -H 'Content-Type: application/json' \
  -H 'X-EvoOps-Role: approver' \
  -H 'X-EvoOps-Actor: reviewer@example.com' \
  -d '{"approved":true,"reason":"metrics and scope reviewed"}'
```

## MCP tools

```bash
export EVOOPS_MCP_SSE_URLS=http://localhost:3001/sse,http://localhost:3002/sse
export EVOOPS_MCP_TOOL_ALLOWLIST=lookup_order,create_ticket
```

Discovered tools enter the same Eino registry as local tools. Production systems should enforce allowlists during discovery and authorization/argument policy again during invocation.

## Repository layout

```text
cmd/evoops/             CLI and HTTP entry point
data/demo/              synthetic campaign data and advertising playbook
data/harness/           versioned labeled Harness cases
docs/                   architecture, Harness, evolution, decisions
internal/agent/         Eino workflow, replay mode, diagnosis, synthesis
internal/memory/        feedback-to-memory learning and tenant-scoped profiles
internal/retrieval/     dense + BM25 + RRF + auto-merge + reranking
internal/harness/       deterministic scoring, LLM verifier/judge, fingerprints, attribution
internal/evolution/     baseline/candidate evaluation and release loop
internal/prompt/        LLM Prompt Patch generation, immutable composition, static validation
internal/policy/        mutation constraints, credentials, canary, rollback
internal/repository/    atomic JSON trajectory/report/policy persistence
internal/httpapi/       API, role gate, embedded operations console
internal/tools/         typed Eino tools and MCP discovery
```

## Engineering boundaries

The local corpus and hashed dense vectorizer keep CI deterministic; the retriever boundary can be replaced by an embedding service/vector database while preserving the same retrieval trace and Harness contract. The file repository uses locked atomic replacement; PostgreSQL plus a distributed checkpoint/queue is the natural multi-instance implementation. The SSE demo buffers the completed trajectory; production streaming should emit live workflow callbacks.

Detailed design is in [docs/architecture.md](docs/architecture.md), [docs/self-evolution.md](docs/self-evolution.md), and [docs/decision-log.md](docs/decision-log.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
