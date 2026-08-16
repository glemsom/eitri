# Comment-Value Policy

Standard for what comments are allowed in Eitri's Go source. Per-package cleanup batches (issue #292 child tickets) apply this one consistent standard.

## Rules

### References alone are never sufficient

A comment whose only content is a reference to an ADR, a spec section, a GitHub issue number, or a file path is removed.

```go
// See ADR-0003.
// Per spec section 4.2.
// Ref issue #142.
// See internal/engine/compact.go.
```

A reference is not information: the code already carries what it implements, and the ADR, spec, or issue can be found if the history is ever needed.

### A comment stays only when it adds information the code does not already tell

A comment must say something the code does not say on its own. Justified forms:

- the "why" behind a non-obvious decision (why this approach, why not the obvious one),
- a caveat (what breaks, what the caller must not assume),
- an invariant (what must always hold for this code to be correct).

Where the reasoning is captured in an ADR, spec, or issue, either state the substance inline or drop the comment — never just cite. The inline statement must stand on its own; a reader who has never seen the ADR must understand the point from the comment alone.

### Doc comments on exported symbols stay, minus stale refs

Doc comments on exported symbols (`// Foo does ...`) stay. Strip any stale ADR/spec/issue references from them; the substance stays.

## Applying this to cleanup batches

Each per-package batch deletes comments that fail the rules above and leaves everything that passes. When in doubt, drop the comment: a comment that merely restates the code, or cites a reference without substance, is noise, and noise is the problem this policy exists to remove.
