# Controlled Self-Evolution

## Lifecycle

```text
active
  ↓ fresh Harness
failure attribution
  ↓ field allowlist
candidate
  ↓ candidate Harness versus active baseline
evaluated + release credential
  ↓ explicit deterministic traffic assignment
canary
  ↓ human decision
active or blocked
  ↓ regression
rolled_back
```

`evolve` automates the path only through evaluated/canary. It never promotes a candidate.

## Mutation surface

| Failure class | Eligible examples | Direction |
|---|---|---|
| Retrieval | candidate K, dense/sparse weights, merge/relevance thresholds, query rewrite | Improve recall/ranking |
| Trajectory | prompt/routing revision, step/tool budgets | Repair deterministic route |
| Safety | approval threshold, tool allowlist | Tighten only |
| Outcome | anomaly thresholds, prompt revision | Improve labeled signal/action coverage |
| Cost | top K, candidate K, rerank switch, cost budget | Reduce work without quality regression |

The current candidate generator implements conservative mutations for the most defensible subset. The attribution allowlist is enforced before every assignment, so adding a new optimizer cannot silently broaden its authority.

When the active Harness is clean and feedback is sparse, EvoOps proposes an efficiency hypothesis by reducing the hybrid candidate pool. Useful feedback with positive observed KPI may increase anomaly sensitivity; predominantly negative feedback may reduce false positives by raising selected thresholds.

## Release credential

A candidate becomes `evaluated` only when its own report passes and its parent matches the current active policy. The following fields are persisted on the policy:

```text
evaluation_report_id
evaluated_suite_version
evaluated_against_version
evaluated_at
```

Canary and promotion re-check these fields against current state. If another policy has become active, an older candidate must be evaluated again rather than reusing a stale score.

## Canary and rollback

Store IDs are hashed into stable buckets. A canary percentage between 1 and 50 selects the candidate for a deterministic subset while all other stores stay on active.

Promotion requires canary status and a valid release credential. It records the previous active version. Rollback marks the current policy `rolled_back`, restores that previous version, and clears canary state.

## Non-goals

EvoOps does not automatically modify Go source, invent tools, change identity policy, promote releases, or approve business side effects. Those boundaries require code review or a human decision because their blast radius cannot be contained by an offline metric alone.
