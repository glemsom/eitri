#!/usr/bin/env bash
# agent-loop.sh — Parallel dispatcher for ready-for-agent issues via eitri -b
#
# Usage:
#   ./scripts/agent-loop.sh /path/to/repo [-j N]
#
# Claims up to N open ready-for-agent issues (oldest first), adds an
# `in-progress` label to each, creates one detached git worktree per issue
# (`.worktrees/issue-N`), and runs one `eitri -b` worker per worktree in
# parallel. Worker stdout goes to `.worktrees/issue-N/log` — never interleaved
# on the terminal. Workers create a branch, implement, push, and open a PR
# whose description contains `Closes #N`; they never merge.
#
# After all workers finish, the dispatcher merges PRs serially
# (`gh pr merge --squash --delete-branch`), rebasing each PR branch onto the
# latest `origin/main` first. Rebase conflicts spawn a focused `eitri -b`
# resolution run inside that worktree (capped at 3 attempts per PR); past the
# cap the PR is left open with a comment and the dispatcher moves on.
#
# Exits 0 only when nothing was left unmerged or orphaned. Per-issue worker
# failures are reported and never stop the other workers. See
# docs/agents/batch.md.

set -euo pipefail

REPO=""
JOBS=2

while [ $# -gt 0 ]; do
	case "$1" in
		-j)
			if [ $# -lt 2 ]; then
				echo "Usage: $0 /path/to/repo [-j N]" >&2
				exit 1
			fi
			JOBS="$2"
			shift 2
			;;
		-*)
			echo "Usage: $0 /path/to/repo [-j N]" >&2
			exit 1
			;;
		*)
			if [ -n "$REPO" ]; then
				echo "Usage: $0 /path/to/repo [-j N]" >&2
				exit 1
			fi
			REPO="$1"
			shift
			;;
	esac
done

if [ -z "$REPO" ]; then
	echo "Usage: $0 /path/to/repo [-j N]" >&2
	exit 1
fi

if ! [[ "$JOBS" =~ ^[0-9]+$ ]] || [ "$JOBS" -lt 1 ]; then
	echo "Error: -j must be a positive integer (got '$JOBS')" >&2
	exit 1
fi

if [ ! -d "$REPO" ]; then
	echo "Error: not a directory: $REPO" >&2
	exit 1
fi

for cmd in gh eitri jq git; do
	if ! command -v "$cmd" >/dev/null 2>&1; then
		echo "Error: required command not found: $cmd" >&2
		exit 1
	fi
done

cd "$REPO"

WORKTREES_DIR=".worktrees"

# --- Cleanup ---------------------------------------------------------------

WORKTREES=()

cleanup() {
	for wt in "${WORKTREES[@]:-}"; do
		git worktree remove --force "$wt" 2>/dev/null || true
	done
	git worktree prune 2>/dev/null || true
}
trap cleanup EXIT

# --- Crash recovery --------------------------------------------------------

cleanup_stale_claims() {
	# A previous dispatcher may have died between claiming an issue and
	# cleaning up. Drop in-progress labels whose worktree no longer exists so
	# those issues can be picked again.
	local num
	for num in $(gh issue list --repo "$REPO" --label in-progress --state open --json number --jq '.[].number' 2>/dev/null || true); do
		if [ ! -d "$WORKTREES_DIR/issue-$num" ]; then
			echo "Removing stale in-progress label from issue #$num (no worktree)"
			gh issue edit "$num" --repo "$REPO" --remove-label in-progress >/dev/null 2>&1 || true
		fi
	done
}

# --- Prompts ---------------------------------------------------------------

build_prompt() {
	local num="$1" title="$2"
	cat <<EOF
---
Description: Implement issue #${num} — ${title}
---

Implement the GitHub issue #${num} — ${title}. You are working in a detached git worktree.

Step 1:
- [ ] Create a branch for the implementation
- [ ] Implement the work described in the GitHub issue using the \`tdd\` skill if possible
- [ ] Update any relevant documentation
- [ ] Run \`make test\` and fix any issues found
- [ ] Commit and push changes to git
- [ ] Create a GitHub pull request whose description contains \`Closes #${num}\`

Do NOT merge the pull request — merging is handled serially by the dispatcher.
Do NOT switch to or check out \`main\` (it is checked out in the primary worktree),
and do NOT delete branches — the dispatcher owns cleanup.

No user confirmation required for \`ready-for-agent\` issues
EOF
}

build_resolve_prompt() {
	local branch="$1" pr="$2"
	cat <<EOF
Resolve the rebase conflicts on branch ${branch} (PR #${pr}).

Run \`git status\` to see the conflicted files, resolve them, then
\`git add\` the resolved files and \`git rebase --continue\`.
Finally \`git push --force origin HEAD:${branch}\` to update the PR.
Do NOT merge the PR and do NOT switch branches.
EOF
}

# --- Issue claiming --------------------------------------------------------

claim_issues() {
	# Oldest N open ready-for-agent issues without an in-progress label
	# (and without issue-type:parent, matching the previous prompt's constraint).
	gh issue list --repo "$REPO" --label ready-for-agent --state open \
		--json number,title,labels \
		--jq 'sort_by(.number) | .[] |
			select(([.labels[].name] | index("in-progress")) | not) |
			select(([.labels[].name] | index("issue-type:parent")) | not) |
			.number' \
		2>/dev/null | head -n "$JOBS" || true
}

# --- PR lookup / merge -----------------------------------------------------

find_pr() {
	local num="$1"
	# The open PR whose description references "Closes #num".
	gh pr list --repo "$REPO" --state open --json number,body,headRefName \
		--jq ".[] | select(.body != null) | select(.body | test(\"#${num}\\\\b\"; \"i\")) | .number" \
		2>/dev/null | head -n 1 || true
}

merge_pr() {
	local num="$1" pr="$2" branch="$3"
	local wt="$WORKTREES_DIR/issue-$num"
	local attempts=0 merged=0

	while [ "$attempts" -lt 3 ]; do
		attempts=$((attempts + 1))

		git -C "$wt" fetch origin main >/dev/null 2>&1 || true
		if git -C "$wt" rebase origin/main >/dev/null 2>&1; then
			# Clean rebase: force-push the rebased branch, then merge.
			git -C "$wt" push --force origin "HEAD:$branch" >/dev/null 2>&1 || true
			if gh pr merge "$pr" --repo "$REPO" --squash --delete-branch >/dev/null 2>&1; then
				echo "Merged PR #$pr (issue #$num)"
				merged=1
				break
			fi
			echo "Warning: merge of PR #$pr (issue #$num) failed (attempt $attempts/3); retrying" >&2
			continue
		fi

		# Rebase conflict — spawn a focused resolution run in the worktree.
		echo "Rebase conflict on PR #$pr (issue #$num) — resolution run $attempts/3"
		local resolve_prompt
		resolve_prompt=$(build_resolve_prompt "$branch" "$pr")
		if ! ( cd "$wt" && eitri --persona generic -b "$resolve_prompt" ) > "$wt/log.resolve" 2>&1; then
			echo "Warning: resolution run for PR #$pr failed (attempt $attempts/3)" >&2
			continue
		fi
		if git -C "$wt" rev-parse -q --verify REBASE_HEAD >/dev/null 2>&1; then
			echo "Warning: rebase still in progress after resolution run for PR #$pr" >&2
			continue
		fi
		git -C "$wt" push --force origin "HEAD:$branch" >/dev/null 2>&1 || true
		if gh pr merge "$pr" --repo "$REPO" --squash --delete-branch >/dev/null 2>&1; then
			echo "Merged PR #$pr (issue #$num)"
			merged=1
			break
		fi
	done

	if [ "$merged" -ne 1 ]; then
		echo "Error: could not merge PR #$pr (issue #$num) after 3 attempts — leaving open" >&2
		gh pr comment "$pr" --repo "$REPO" \
			--body "Dispatcher could not merge this PR (rebase conflicts or merge failure). Needs human intervention." \
			>/dev/null 2>&1 || true
		return 1
	fi
	return 0
}

# --- Main loop -------------------------------------------------------------

main() {
	local failures=0

	git worktree prune 2>/dev/null || true
	cleanup_stale_claims

	while true; do
		if ! git fetch origin main >/dev/null 2>&1; then
			echo "Error: git fetch origin main failed" >&2
			exit 1
		fi

		local issues
		issues=$(claim_issues)
		if [ -z "$issues" ]; then
			echo "No ready-for-agent issues remain. Done."
			break
		fi

		echo "Claimed $(echo "$issues" | wc -l | tr -d ' ') issue(s): $(echo "$issues" | tr '\n' ' ')"
		local pids=() num title prompt pid wt

		# Claim + spawn one worker per issue.
		for num in $issues; do
			if ! gh issue edit "$num" --repo "$REPO" --add-label in-progress >/dev/null 2>&1; then
				echo "Warning: could not claim issue #$num (add-label failed); skipping" >&2
				continue
			fi
			if ! git worktree add "$WORKTREES_DIR/issue-$num" --detach origin/main >/dev/null 2>&1; then
				echo "Warning: could not create worktree for issue #$num; releasing claim" >&2
				gh issue edit "$num" --repo "$REPO" --remove-label in-progress >/dev/null 2>&1 || true
				continue
			fi
			WORKTREES+=("$WORKTREES_DIR/issue-$num")
			title=$(gh issue view "$num" --repo "$REPO" --json title --jq '.title' 2>/dev/null || echo "issue #$num")
			prompt=$(build_prompt "$num" "$title")
			echo "Starting worker for issue #$num — $title"
			( cd "$WORKTREES_DIR/issue-$num" && eitri --persona generic -b "$prompt" ) > "$WORKTREES_DIR/issue-$num/log" 2>&1 &
			pid=$!
			pids+=("$num:$pid")
		done

		# Wait for all workers; report per-issue exit status.
		for entry in "${pids[@]:-}"; do
			num=${entry%%:*}
			pid=${entry##*:}
			if wait "$pid"; then
				echo "Worker for issue #$num succeeded"
			else
				echo "Warning: worker for issue #$num failed (exit $?)" >&2
			fi
		done

		# Serialized merge queue: every issue with an open PR goes through it,
		# even if its worker failed.
		for num in $issues; do
			wt="$WORKTREES_DIR/issue-$num"
			[ -d "$wt" ] || continue
			local pr branch
			pr=$(find_pr "$num")
			if [ -z "$pr" ]; then
				echo "Error: no open PR found for issue #$num — left unmerged/orphaned" >&2
				failures=$((failures + 1))
				continue
			fi
			branch=$(gh pr view "$pr" --repo "$REPO" --json headRefName --jq '.headRefName' 2>/dev/null || true)
			if [ -z "$branch" ]; then
				echo "Error: could not resolve branch for PR #$pr (issue #$num)" >&2
				failures=$((failures + 1))
				continue
			fi
			if ! merge_pr "$num" "$pr" "$branch"; then
				failures=$((failures + 1))
			fi
		done
	done

	if [ "$failures" -gt 0 ]; then
		echo "Dispatcher finished with $failures leftover(s) — see log above" >&2
		exit 1
	fi
	echo "All ready-for-agent issues processed cleanly."
}

main
