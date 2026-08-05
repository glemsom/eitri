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
# failures are reported and never stop the other workers. Ctrl+C (or SIGTERM)
# stops claiming after the current batch finishes; a second signal forces exit.
# See docs/agents/batch.md.

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
		[ -n "$wt" ] || continue
		git worktree remove --force "$wt" 2>/dev/null || true
	done
	git worktree prune 2>/dev/null || true
}
trap cleanup EXIT

# --- Stop handling ---------------------------------------------------------

STOP_REQUESTED=0

request_stop() {
	if [ "$STOP_REQUESTED" -eq 1 ]; then
		echo "Second signal received — forcing exit" >&2
		exit 130
	fi
	STOP_REQUESTED=1
	echo "Stop requested — finishing current batch; no new issues will be claimed (send signal again to force exit)" >&2
}
trap request_stop INT TERM

# Run workers in their own session so a terminal Ctrl+C only reaches the
# dispatcher (it finishes the current batch instead of killing workers).
WORKER_SHIELD=""
if command -v setsid >/dev/null 2>&1 && setsid --wait true >/dev/null 2>&1; then
	WORKER_SHIELD="setsid --wait"
fi


# --- Sandbox check ---------------------------------------------------------

ensure_sandbox_git_writable() {
	# eitri's default bwrap sandbox binds the filesystem read-only and punches
	# writable holes only for the workspace and extra_writable_paths. The git
	# metadata for a worktree lives in the *main* repo's .git dir
	# (.git/worktrees/issue-N), which is read-only unless explicitly bound
	# writable. Workers then cannot commit/push inside the worktree; they fall
	# back to shadow-repo hacks that leave the worktree detached and dirty,
	# which silently breaks the rebase/merge queue below. Fail early with an
	# actionable message instead.
	local cfg="${EITRI_CONFIG:-$HOME/.eitri/config.json}"
	local profile="default"
	if [ -f "$cfg" ]; then
		profile=$(jq -r '.sandbox.profile // "default"' "$cfg")
	fi
	# Profile "none" disables the sandbox entirely — git works.
	if [ "$profile" = "none" ]; then
		return 0
	fi
	# If bwrap is missing or unusable, eitri falls back to direct execution.
	if ! command -v bwrap >/dev/null 2>&1 || ! bwrap --ro-bind / / true >/dev/null 2>&1; then
		return 0
	fi
	# Default profile: the main repo's git dir must be writable in the sandbox.
	local gitdir repo
	gitdir=$(git rev-parse --absolute-git-dir 2>/dev/null || echo "$REPO/.git")
	repo=$(cd "$REPO" && pwd -P)
	if [ -f "$cfg" ] && jq -e --arg d "$gitdir" --arg r "$repo" \
		'.sandbox.extra_writable_paths // [] | any(. == $d or . == $r or . == ($r + "/"))' "$cfg" >/dev/null 2>&1; then
		return 0
	fi
	echo "Error: eitri sandbox (profile '$profile') blocks git writes to $gitdir" >&2
	echo "       Worktree git metadata lives there; workers cannot commit/push, breaking the merge queue." >&2
	echo "       Add it to the sandbox's writable paths in $cfg:" >&2
	echo "       \"sandbox\": { \"profile\": \"default\", \"extra_writable_paths\": [\"$gitdir\"] }" >&2
	return 1
}

# --- Labels ----------------------------------------------------------------

ensure_in_progress_label() {
	# gh refuses to add a label that does not exist; create it on demand.
	if gh label list --json name --jq 'any(.name == "in-progress")' 2>/dev/null | grep -q true; then
		return 0
	fi
	echo "Creating missing in-progress label"
	if ! gh label create in-progress --color "fbca04" --description "Issue currently being worked on by an agent" >/dev/null 2>&1; then
		echo "Error: in-progress label is missing and could not be created (insufficient permissions?)" >&2
		return 1
	fi
}

# --- Crash recovery --------------------------------------------------------

cleanup_stale_claims() {
	# A previous dispatcher may have died between claiming an issue and
	# cleaning up. Drop in-progress labels whose worktree no longer exists so
	# those issues can be picked again.
	local num
	for num in $(gh issue list --label in-progress --state open --json number --jq '.[].number' 2>/dev/null || true); do
		if [ ! -d "$WORKTREES_DIR/issue-$num" ]; then
			echo "Removing stale in-progress label from issue #$num (no worktree)"
			gh issue edit "$num" --remove-label in-progress >/dev/null 2>&1 || true
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
	gh issue list --label ready-for-agent --state open \
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
	gh pr list --state open --json number,body,headRefName \
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
			if gh pr merge "$pr" --squash --delete-branch >/dev/null 2>&1; then
				echo "Merged PR #$pr (issue #$num)"
				merged=1
				break
			fi
			echo "Warning: merge of PR #$pr (issue #$num) failed (attempt $attempts/3); retrying" >&2
			continue
		fi

		# Rebase conflict — spawn a focused resolution run in the worktree.
		echo "Rebase conflict on PR #$pr (issue #$num) — resolution run $attempts/3"
		local resolve_prompt rpid rst=0
		resolve_prompt=$(build_resolve_prompt "$branch" "$pr")
		( cd "$wt" && exec $WORKER_SHIELD eitri --persona generic -b "$resolve_prompt" ) > "$wt/log.resolve" 2>&1 &
		rpid=$!
		while :; do
			if wait "$rpid"; then
				rst=0
				break
			else
				rst=$?
			fi
			[ "$rst" -eq 130 ] || [ "$rst" -eq 143 ] || break
		done
		if [ "$rst" -ne 0 ]; then
			echo "Warning: resolution run for PR #$pr failed (attempt $attempts/3)" >&2
			continue
		fi
		if git -C "$wt" rev-parse -q --verify REBASE_HEAD >/dev/null 2>&1; then
			echo "Warning: rebase still in progress after resolution run for PR #$pr" >&2
			continue
		fi
		git -C "$wt" push --force origin "HEAD:$branch" >/dev/null 2>&1 || true
		if gh pr merge "$pr" --squash --delete-branch >/dev/null 2>&1; then
			echo "Merged PR #$pr (issue #$num)"
			merged=1
			break
		fi
	done

	if [ "$merged" -ne 1 ]; then
		echo "Error: could not merge PR #$pr (issue #$num) after 3 attempts — leaving open" >&2
		gh pr comment "$pr" \
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
	if ! ensure_sandbox_git_writable; then
		exit 1
	fi
	if ! ensure_in_progress_label; then
		exit 1
	fi
	cleanup_stale_claims

	while true; do
		if [ "$STOP_REQUESTED" -eq 1 ]; then
			echo "Stop requested — not claiming more issues."
			break
		fi
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
		local pids=() num title prompt pid wt spawned=0

		# Claim + spawn one worker per issue.
		for num in $issues; do
			if ! gh issue edit "$num" --add-label in-progress >/dev/null 2>&1; then
				echo "Warning: could not claim issue #$num (add-label failed); skipping" >&2
				continue
			fi
			if ! git worktree add "$WORKTREES_DIR/issue-$num" --detach origin/main >/dev/null 2>&1; then
				echo "Warning: could not create worktree for issue #$num; releasing claim" >&2
				gh issue edit "$num" --remove-label in-progress >/dev/null 2>&1 || true
				continue
			fi
			WORKTREES+=("$WORKTREES_DIR/issue-$num")
			title=$(gh issue view "$num" --json title --jq '.title' 2>/dev/null || echo "issue #$num")
			prompt=$(build_prompt "$num" "$title")
			echo "Starting worker for issue #$num — $title"
			( cd "$WORKTREES_DIR/issue-$num" && exec $WORKER_SHIELD eitri --persona generic -b "$prompt" ) > "$WORKTREES_DIR/issue-$num/log" 2>&1 &
			pid=$!
			pids+=("$num:$pid")
			spawned=$((spawned + 1))
		done

		if [ "$spawned" -eq 0 ]; then
			echo "Error: could not spawn any workers (claim or worktree creation failed) — aborting" >&2
			exit 1
		fi

		# Wait for all workers; report per-issue exit status.
		# wait returns >128 when interrupted by a trapped signal — retry then
		# instead of mistaking the signal for a worker failure.
		for entry in "${pids[@]:-}"; do
			[ -n "$entry" ] || continue
			num=${entry%%:*}
			pid=${entry##*:}
			st=0
			while :; do
				if wait "$pid"; then
					st=0
					break
				else
					st=$?
				fi
				[ "$st" -eq 130 ] || [ "$st" -eq 143 ] || break
			done
			if [ "$st" -eq 0 ]; then
				echo "Worker for issue #$num succeeded"
			else
				echo "Warning: worker for issue #$num failed (exit $st)" >&2
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
			branch=$(gh pr view "$pr" --json headRefName --jq '.headRefName' 2>/dev/null || true)
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
