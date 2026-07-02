# SOUL.md Generation

Podiom treats an agent's `SOUL.md` as identity, not instructions. It should say
who the agent is, how it thinks, how it relates to the user, and what behavioral
defaults should show up in ordinary work.

`SOUL.md` is not the place for Podiom's global rules, tool policy, changelog, or
memory. Podiom composes those separately: base `AGENTS.md`, optional per-agent
`AGENTS.md`, `SOUL.md`, then `MEMORY.md` when it has content.

## Generated Shape

Generated souls use these sections:

```markdown
# Identity

Name: <agent-name>

<One short paragraph about who the agent is and why it exists.>

## Purpose

- <mission and default priorities>

## Worldview

- <specific beliefs or operating principles>

## Working style

- <concrete collaboration behavior>

## Voice

- <tone, rhythm, directness, humor, and anti-patterns>

## Strengths

- <where the agent should lean in>

## Boundaries

- <what the agent refuses, pauses on, or asks about>

## Calibration notes

- <tells that the soul is being followed well>
```

## Quality Bar

Specific beats vague. A useful soul lets someone predict how the agent will act
on a new task. "Careful reviewer who challenges optimistic assumptions before
touching code" is useful; "helpful and thoughtful" is too generic.

Behavior beats biography. Background is only useful when it changes decisions,
voice, or collaboration style.

Boundaries should be operational. Say when the agent refuses, asks first, slows
down, or names uncertainty. Keep Podiom's global safety rules in base
`AGENTS.md`, not in the soul.

Voice guidance should affect text. Include concrete defaults for directness,
brevity, humor, warmth, and phrases or tones to avoid.

## Generation Flow

`podiom agents create <name> --generate-soul` creates the agent, then asks
`podiomd` to generate and save a first `SOUL.md`.

`podiom agents update <name> --generate-soul` drafts a replacement from the
current soul and optional `--notes`, previews it, and asks before saving.

The daemon endpoint is `POST /api/agents/{name}/generate`. It returns:

```json
{
  "agent": "juno",
  "soul": "# Identity\n...",
  "saved": false
}
```

Generation creates an auditable agent-shaping session and denies tool permission
requests because SOUL.md generation does not need tools.
