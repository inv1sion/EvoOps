# Engineering decision log

## ADR-001: own repository over a community demo fork

**Decision:** create a clean EvoOps repository and use official Eino code and documentation as the compatibility reference.

The evaluated community `kevinten-ai/eino-demos` repository is useful as a learning catalog, but at the time of evaluation it had a very small test surface and no license file in the repository root. Its README also described memory-only RAG/memory and listed workflow persistence, human-in-the-loop, and multi-tenant security as future work. Forking it would inherit ambiguous licensing and a demo-oriented package boundary.

The official [`cloudwego/eino`](https://github.com/cloudwego/eino) and [`cloudwego/eino-examples`](https://github.com/cloudwego/eino-examples) repositories provide the maintained API and Apache-2.0 examples for workflows, tools, streaming, MCP, callbacks, and interrupt/resume patterns. EvoOps therefore depends directly on released Eino modules and keeps its own domain architecture, tests, and license.

**Consequence:** slightly more initial code, but clearer ownership, a defensible license, smaller dependency surface, and an architecture that can be explained in an interview without pretending a forked platform was authored from scratch.

## ADR-002: deterministic control, optional language model

**Decision:** keep signal detection, action construction, risk classification, and policy evaluation deterministic. Use a language model only to synthesize the final explanation.

This lets the project run offline, prevents an LLM from bypassing approval, and makes replay tests reproducible. The tradeoff is less flexible free-form planning in the MVP. A future planner can propose typed plans, but those plans must still pass the same deterministic policy gate.

## ADR-003: policy evolution instead of source-code mutation

**Decision:** allow the system to propose only constrained, versioned policy changes.

Automatic code mutation is difficult to evaluate, authorize, and roll back under real business traffic. Thresholds, retrieval depth, routing, and prompt versions are suitable evolution units because they can be replayed and canaried. Source changes remain ordinary reviewed pull requests.

## ADR-004: one exact execution path for live runs and evaluation

**Decision:** the Evaluation Harness calls the compiled production Eino workflow with a supplied policy and a dry-run execution gate.

A separate mock evaluator is easier to write but can pass while production routing, tool arguments, or approval behavior has already drifted. Exact-path replay is more expensive, but it makes trajectory and safety assertions meaningful. UUIDs and timing are normalized only when comparing structural fingerprints.

## ADR-005: release credentials bind evaluation to rollout

**Decision:** store the Harness report ID, suite version, and active baseline on every evaluated candidate; require canary before promotion.

A policy that passed against an older baseline is not automatically safe relative to a newly active policy or changed suite. Binding the credential prevents stale candidates from bypassing the current release gate and makes promotion auditable.

## ADR-006: typed merchant memory cannot mutate the safety boundary

**Decision:** learn only typed, source-linked memory facts from explicit feedback and normalized KPI outcomes. Use them to annotate and order matching actions, never to modify action risk, tool arguments, or approval thresholds.

Free-form memory injected directly into a planner can become an unreviewed control channel and makes tenant isolation difficult to prove. EvoOps therefore validates feedback action IDs against the referenced run, persists profiles under hashed tenant keys, surfaces every applied fact in the evidence chain, and evaluates fixed memory snapshots in the Harness. The tradeoff is deliberately narrower personalization, but its behavior is explainable, replayable, and safe to evolve.

## ADR-007: one bounded advertising ROI scenario

**Decision:** keep the demo agent focused on campaign ROI diagnosis instead of routing across traffic, conversion, refund, and inventory domains.

A single business point makes the input contract, evidence, action boundary, and evaluation labels easy to explain and test. The production graph reads only campaign performance, detects active plans below a versioned ROI threshold, creates an attribution-review task, and guards campaign suspension. Broader commerce domains can reuse the framework later, but they must bring their own data contracts and Harness cases rather than silently expanding this agent's authority.
