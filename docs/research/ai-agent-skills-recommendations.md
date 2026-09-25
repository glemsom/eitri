# AI-agent skills: sourced recommendations

**Scope.** This note limits itself to the open Agent Skills specification and first-party Anthropic and OpenAI documentation. “Requirement” means normative format or product behavior stated by the source; “guidance” means a recommendation rather than a portability guarantee.

## 1. Discovery and metadata

### Requirements

- An Agent Skill is a directory that contains at least `SKILL.md`; `SKILL.md` **must** have YAML frontmatter followed by Markdown. Its `name` and `description` fields are required. [Agent Skills Specification](https://agentskills.io/specification)
- Under the specification, `name` must be 1–64 characters, lowercase alphanumeric or hyphens, cannot begin/end with a hyphen or contain `--`, and must equal the parent directory name. `description` must be non-empty and no more than 1,024 characters. [Agent Skills Specification](https://agentskills.io/specification)
- Anthropic documents the same two required fields and adds Claude-specific restrictions: name and description cannot contain XML tags, and names cannot contain `anthropic` or `claude`. [Anthropic: Agent Skills—Skill structure](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#skill-structure)
- Optional standard frontmatter is `license`, `compatibility`, `metadata`, and experimental `allowed-tools`; `metadata` is a string-to-string map and `allowed-tools` support may vary by host. [Agent Skills Specification](https://agentskills.io/specification)
- OpenAI’s optional `agents/openai.yaml` supplies product metadata, an implicit-invocation policy, and tool dependencies; it is product-specific rather than part of the open required frontmatter. [OpenAI: Build skills—Optional metadata](https://developers.openai.com/codex/skills/#optional-metadata)

### Guidance

- Treat the description as the discovery index: say both **what** the skill does and **when** to use it; use task-specific keywords. [Agent Skills Specification](https://agentskills.io/specification) [Anthropic: Agent Skills—How skills work](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#how-skills-work)
- For OpenAI hosts, make descriptions concise, define scope and boundaries, and put the primary use case and trigger terms first because descriptions can be shortened during discovery. [OpenAI: Build skills—How ChatGPT and Codex use skills](https://developers.openai.com/codex/skills/#how-chatgpt-and-codex-use-skills)
- Include `compatibility` only for real environment requirements; use reasonably unique keys in extension metadata. [Agent Skills Specification](https://agentskills.io/specification)

## 2. Progressive loading and activation

### Product behavior / requirements

- Anthropic says its host loads skill name and description at startup into the system prompt, reads the `SKILL.md` body only after a matching skill is triggered, and reads referenced resources only when needed. [Anthropic: Agent Skills—How skills work](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#how-skills-work)
- OpenAI says ChatGPT and Codex begin with each skill’s name and description, then load full `SKILL.md` instructions when they decide to use it. Codex limits its initial skills list to 2% of context or 8,000 characters when context size is unknown; it may shorten descriptions or omit skills with a warning. [OpenAI: Build skills](https://developers.openai.com/codex/skills/)
- OpenAI supports explicit activation (`@` in ChatGPT; `/skills` or `$` in Codex) and implicit activation when the task matches the description. `policy.allow_implicit_invocation: false` prevents implicit Codex invocation while retaining explicit `$` invocation. [OpenAI: Build skills—How ChatGPT and Codex use skills](https://developers.openai.com/codex/skills/#how-chatgpt-and-codex-use-skills) [Optional metadata](https://developers.openai.com/codex/skills/#optional-metadata)
- Codex discovers local skills by scanning `.agents/skills` from the current directory up to the repository root, plus user, admin, and bundled system locations; identically named skills are not merged. [OpenAI: Build skills—Where Codex loads local skills](https://developers.openai.com/codex/skills/#where-codex-loads-local-skills)

### Guidance

- Design in layers: short discovery metadata, a focused `SKILL.md`, then separately addressable scripts, references, and assets. The specification recommends under 5,000 instruction tokens and under 500 main-file lines, with detailed material moved to focused reference files. [Agent Skills Specification](https://agentskills.io/specification)
- Reference files by paths relative to the skill root, keep references one level from `SKILL.md`, and avoid deep chains. [Agent Skills Specification](https://agentskills.io/specification)
- Keep one skill focused on one job; prefer instructions to scripts unless deterministic behavior or external tooling is needed; write imperative steps with explicit inputs and outputs; test prompts against the description for triggering. [OpenAI: Build skills—Best practices](https://developers.openai.com/codex/skills/#best-practices)

## 3. Instruction injection and trust boundaries

### Requirements / product semantics

- In OpenAI’s instruction hierarchy, developer messages are application instructions and take priority over user messages; user messages are lower-priority inputs. The Responses `instructions` parameter likewise takes priority over `input`. [OpenAI: Prompt engineering—Message roles and instruction following](https://platform.openai.com/docs/guides/prompt-engineering#message-roles-and-instruction-following)
- Skills are executable instruction bundles, not inert documentation: Anthropic states that a malicious skill can direct tool or code execution beyond its stated purpose. [Anthropic: Agent Skills—Security considerations](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#security-considerations)

### Guidance

- Install only self-authored or trusted skills; audit **every** bundled file before use, including `SKILL.md`, scripts, images, and other resources. Check for unexpected network calls, file access, or behavior inconsistent with the skill’s stated purpose. [Anthropic: Agent Skills—Security considerations](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#security-considerations)
- Treat fetched external content as untrusted: Anthropic specifically warns that URL-fetched content can contain malicious instructions and that trusted skills’ dependencies can change. Keep tool permissions and sensitive-data access minimal. [Anthropic: Agent Skills—Security considerations](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#security-considerations)
- Separate stable instructions from dynamic data with clear Markdown/XML boundaries, and use typed arguments or schemas for dynamic values where applicable. [OpenAI: Prompt engineering—Message formatting with Markdown and XML](https://platform.openai.com/docs/guides/prompt-engineering#message-formatting-with-markdown-and-xml) [OpenAI: Prompt engineering—Version prompts in code](https://platform.openai.com/docs/guides/prompt-engineering#version-prompts-in-code)

## Actionable evaluation checklist

### Conformance and indexing

- [ ] The directory has one `SKILL.md`; frontmatter precedes Markdown; `name` and `description` are present. [Specification](https://agentskills.io/specification)
- [ ] Name obeys the specification syntax, length, and parent-directory match; any target-host restrictions are also checked. [Specification](https://agentskills.io/specification) [Anthropic](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#skill-structure)
- [ ] Description is non-empty, within 1,024 characters, names the capability and trigger conditions, and front-loads discriminating terms. [Specification](https://agentskills.io/specification) [OpenAI](https://developers.openai.com/codex/skills/#how-chatgpt-and-codex-use-skills)
- [ ] Optional metadata is portable only where standardized; host-specific configuration is isolated and documented. [Specification](https://agentskills.io/specification) [OpenAI](https://developers.openai.com/codex/skills/#optional-metadata)

### Loading and activation

- [ ] Startup metadata remains small; detailed instructions and references are not injected until selected. [Specification](https://agentskills.io/specification)
- [ ] `SKILL.md` is focused (recommended: <5,000 tokens / <500 lines); references are focused, relative, and at most one link deep. [Specification](https://agentskills.io/specification)
- [ ] Explicit invocation works, and representative positive, negative, and ambiguous prompts demonstrate intended implicit triggering; disable implicit invocation for skills that must be chosen deliberately. [OpenAI](https://developers.openai.com/codex/skills/#how-chatgpt-and-codex-use-skills) [OpenAI policy](https://developers.openai.com/codex/skills/#optional-metadata)

### Safety

- [ ] A reviewer has audited all packaged and downloaded content, especially scripts, network access, file operations, and declared dependencies. [Anthropic](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#security-considerations)
- [ ] Dynamic or fetched material is clearly separated from controlling instructions and is not allowed to redefine authority, permissions, or workflow policy. [OpenAI](https://platform.openai.com/docs/guides/prompt-engineering#message-roles-and-instruction-following) [Anthropic](https://docs.anthropic.com/en/docs/agents-and-tools/agent-skills/overview#security-considerations)
