# Research: Using SKILL.md `description` field for instructions

**Question:** Is it a good approach to use the `description` field (YAML frontmatter) in `SKILL.md` to carry instruction content?

## Sources consulted

| Source | URL |
|---|---|
| Agent Skills Specification | https://agentskills.io/specification |
| Optimizing skill descriptions | https://agentskills.io/skill-creation/optimizing-descriptions |
| Best practices for skill creators | https://agentskills.io/skill-creation/best-practices |
| Grill-with-docs skill (example) | `../skills/grill-with-docs/SKILL.md` |
| All 27 skills in this repo | `../skills/*/SKILL.md` |

## How the spec defines the two fields

### `description` (frontmatter)

> Max 1024 characters. Non-empty. Describes what the skill does and when to use it.

The spec is unambiguous: the `description` field is about **what** (does) and **when** (to use). It is a triggering mechanism.

### Body content (after frontmatter)

> The Markdown body after the frontmatter contains the skill instructions. There are no format restrictions. Write whatever helps agents perform the task effectively.

The body is where **how** (instructions, procedures) lives.

### Progressive disclosure reinforces this separation

> Metadata (~100 tokens): The `name` and `description` fields are loaded at startup for all skills
> Instructions (< 5000 tokens recommended): The full `SKILL.md` body is loaded when the skill is activated

This is the critical architectural constraint. Putting instructions in the `description` means they are loaded for **every skill at every startup** — even skills never activated. Instructions in the body are loaded **only when the skill activates**.

## What the optimizing guide says

> "The description carries the entire burden of triggering."

> "A few sentences to a short paragraph is usually right — long enough to cover the skill's scope, short enough that it doesn't bloat the agent's context across many skills."

> "If should-trigger queries are failing, the description may be too narrow. Broaden the scope or add context about when the skill is useful."

The guide frames `description` purely as a trigger signal, never as a place for step-by-step instructions.

## Audit of skills in this repo

All 27 skills were examined. **None** put actual instructions inside the `description` field. All use the body for instructions, and the description for triggering. Examples:

| Skill | Description | Body |
|---|---|---|
| `grill-with-docs` | "A relentless interview to sharpen a plan or design, which also creates docs (ADR's and glossary) as we go." | `Run a grilling session using the grilling and domain-modeling skills.` |
| `research` | "Investigate a question against high-trust primary sources…" | 8 lines: how to spin up a sub-agent and what its job is |
| `implement` | "Implement a piece of work based on a spec or set of tickets." | Checklist: use TDD, run typechecking, run tests, commit |
| `code-review` | "Review the changes since a fixed point along two axes — Standards and Spec…" | Body: two-axis review process, sub-agent usage |

Some descriptions are longer than others (e.g., `wayfinder` at 698 chars, `domain-modeling` at 666 chars), but they remain **descriptive** — stating scope and trigger conditions — not **instructional**.

## Verdict

**Using `description` for instructions is an anti-pattern and should be avoided.**

### Why

1. **Context bloat** — Description is loaded for *every* skill at startup. Instructions in the body are loaded only when the skill activates. Putting instructions in description wastes context tokens on irrelevant skills.
2. **Spec violation** — The spec assigns distinct purposes: description = what/when (triggering), body = how (instructions).
3. **Triggering degradation** — Instructions in description muddy the triggering signal. The agent's matching logic works against clear intent statements ("analyze CSV", "review code"). Instruction prose ("Run `scripts/validate.py` first, then…") doesn't match user intents well, weakening triggering accuracy.
4. **Character limit** — 1024 characters is far too restrictive for useful instructions. Even short instructions in the body (like `research` at 8 lines) would overflow this limit if crammed into description.

### When it might seem necessary

Some skill systems historically used the description as the sole instruction channel. This is a legacy pattern from systems without progressive disclosure or where the agent only loaded the frontmatter. The Agent Skills spec is explicit that this is not the intended design.

### Recommendation

- **`description`** — Keep to triggering metadata: what the skill does and when to activate it. Aim for 1–3 sentences.
- **Body** — Write instructions here. Use the full markdown format with headings, code blocks, checklists.
- **Long instructions** — Use progressive disclosure: keep `SKILL.md` under 5000 tokens, move reference material to `references/` files loaded on demand.
