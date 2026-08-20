---
# Example: interval project digest using every:, not cron:.
# Needs: an agent named "jared" and a project id "launch-kit". It works best
# when the project ledger entry has useful notes or instructions.
# What it does when it fires: starts a schedule-origin session and produces a
# short digest from readable project files and Podiom context.
# Permission note: this intentionally omits Bash. In preapproved mode, any
# unlisted side-effecting tool is denied automatically.
agent: jared
project: launch-kit
every: 6h
run_permission: preapproved
allowed_tools:
  - Read
  - LS
  - Glob
  - Grep
enabled: true
---

Write a concise project digest for the last six hours.

Use only the project context and readable files. Do not run shell commands,
modify files, create or update tasks, create schedules, or ask the user a
question.

Report:

- What changed or appears active.
- What looks blocked or stale.
- The next useful thing someone should inspect manually.
