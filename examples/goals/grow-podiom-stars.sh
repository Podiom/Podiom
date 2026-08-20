#!/usr/bin/env sh
# Example: create a real Podiom goal through the HTTP API.
# Needs: podiomd running locally, `podiom` on PATH, an existing lead agent named
# "jared", and a project id "launch-kit" from the cookbook ledger example.
# Edit lead_agent and project_id if your installation uses different names.
# What it does when run: creates an active goal and immediately starts the lead
# agent's background planning session. Goal planning and linked goal work run
# autonomously with full access, as documented in docs/goals.md.

set -eu

PODIOM_URL="${PODIOM_URL:-http://127.0.0.1:8787}"
PODIOM_TOKEN="${PODIOM_TOKEN:-$(podiom token show)}"

curl -fsS -X POST "$PODIOM_URL/api/goals" \
  -H "X-Podiom-Token: $PODIOM_TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @- <<'JSON'
{
  "title": "Grow Podiom GitHub stars to 100",
  "description": "Increase qualified interest in Podiom by making the repository easier to evaluate and run. Favor durable product improvements over short-lived promotion.",
  "success_criteria": "The Podiom/Podiom repository has at least 100 GitHub stars, the README and examples let a new visitor run a realistic first workflow, and every outreach or content step is recorded with evidence.",
  "metrics": [
    {
      "name": "GitHub stars",
      "target": 100,
      "current": 4,
      "unit": "stars"
    }
  ],
  "review_every": "24h",
  "lead_agent": "jared",
  "project_id": "launch-kit",
  "provider": "",
  "profile": "",
  "model": "",
  "effort": ""
}
JSON
