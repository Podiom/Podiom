---
# Example: daily read-only repository health check.
# Needs: an agent named "jared" and a project id "launch-kit" whose directory is
# a Git checkout. Change both names before copying if your installation differs.
# What it does when it fires: starts a schedule-origin session, inspects local
# repository state, and reports risks. It should not edit files or move tasks.
# Permission note: Bash is allowed so the agent can run read-only git/gh
# commands. Podiom allow-lists tool names, not individual shell commands.
agent: jared
project: launch-kit
cron: "0 8 * * *"
run_permission: preapproved
allowed_tools:
  - Read
  - LS
  - Glob
  - Grep
  - Bash
enabled: true
---

Run a read-only repository health check for this project.

Check local repository state first:

- `git status --short --branch`
- `git log -5 --oneline`

If GitHub access is already configured in this environment, also list open pull
requests and issues for the repository. Do not authenticate, install tools,
modify files, change branches, push, create tasks, create schedules, or update
goals.

Report:

- Current branch and whether the worktree is clean.
- Open PRs or issues that look blocked, stale, or waiting on review.
- One recommended next action for a human maintainer.
