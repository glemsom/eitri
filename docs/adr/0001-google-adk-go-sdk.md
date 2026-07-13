# Google ADK Go SDK for agent orchestration

Use `google.golang.org/adk/v2` as the agent orchestration layer instead of building from scratch. ADK handles the LLM interaction loop, session management, tool registration, and streaming — infrastructure we'd otherwise maintain ourselves. The cost is a heavy dependency (~200+ transitive modules) and ADK v2 only ships Google model backends, requiring a custom `model.LLM` for other providers (see ADR-0002).

**Status**: Accepted

## Considered Options

- **Build from scratch**: Full control, no dependency. But reimplements session management, tool registration, and streaming infrastructure — substantial effort for no user-facing benefit.
- **Google ADK Go SDK**: Proven integration from prior prototype. Declarative tool registration via Go struct tags, built-in session management, native streaming. Heavy dependency, locked to ADK upgrade cadence.

## Consequences

ADK's large API surface means major-version upgrades may require significant refactoring. Non-Google LLM providers require a custom `model.LLM` implementation.
