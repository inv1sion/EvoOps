# Prompt Evolution Evaluation Result

## Experiment

- Date: 2026-08-31
- Evolution run: `32141139-746d-4c05-936b-6e168e7d017f`
- Harness suite: `ad-roi-harness-v1`
- Golden cases: 4 synthetic, versioned advertising cases
- Answer model: `qwen3.7-flash-2026-07-15`
- Prompt optimizer: `qwen3.7-flash-2026-07-15`
- Independent verifier/judge: `qwen3.7-max-2026-06-08`

The experiment replayed every baseline and candidate case twice with side effects disabled. The Judge evaluated one stable run per case. The candidate was not promoted automatically.

## Result

| Metric | Baseline Prompt | Candidate Prompt | Delta |
|---|---:|---:|---:|
| Overall Harness score | 1.0000 | 1.0000 | 0.0000 |
| Case pass rate | 100% (4/4) | 100% (4/4) | 0 pp |
| Model-quality score | 1.0000 | 1.0000 | 0.0000 |
| Groundedness | 1.0000 | 1.0000 | 0.0000 |
| Numeric accuracy | 1.0000 | 1.0000 | 0.0000 |
| Safety violations | 0 | 0 | 0 |
| Average workflow latency | 15,547.25 ms | 17,825.75 ms | +2,278.50 ms |
| Average cost units | 4.245 | 4.245 | 0 |

Gate decision: **pass**. The candidate received an evaluation credential and remains `evaluated`; it was not assigned canary traffic or promoted.

This result demonstrates a real model-generated Prompt mutation with no quality or safety regression. It does **not** demonstrate quality improvement: the four-case synthetic baseline already scored 1.0. The small dataset and LLM-judge ceiling effect make the result suitable as an engineering-loop verification, not as a production effectiveness claim.

## Generated Prompt artifact

- Parent Prompt: `diagnosis-v1`
- Candidate Prompt: `ad-roi-prompt-1788189745646631800`
- Generator: `eino-prompt-optimizer`
- Generation latency: 19,408 ms
- Static immutable-boundary validation: passed

Generated patch:

> 输出须为连续单段落文本，严格控制在180字内，禁止使用列表或换行。涉及暂停广告动作时，须在句末独立成句声明审批要求，禁止合并至其他建议中。若证据缺失关键数据，仅客观陈述现状并提示按规范复核，不推测原因。

The full prompt, parent version, patch, generator/model, rationale, failure evidence, validation checks, per-case runs, Judge outputs, and comparison metrics are persisted in the runtime evolution record. A sanitized compact result is checked in at `data/results/prompt-evolution-2026-08-31.json`.

## Next evaluation milestone

Before quoting improvement percentages on a resume, expand the suite with independently labeled hard cases and a held-out split. Recommended minimum reporting includes 30–50 cases, bootstrap confidence intervals, prompt-optimizer success rate, unsupported-claim rate, safety violation rate, and latency percentiles. Until then, the defensible statement is “4/4 controlled replay cases passed with zero safety violations and no regression after a model-generated Prompt mutation.”
