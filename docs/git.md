# Git

Podiom projects can carry real source control. A project declares how it wants
to be versioned, Podiom materializes the working copy, and the agent works with
plain `git` from the project directory.

## Credentials are yours

Podiom **never manufactures git credentials.** Git authenticates with whatever
you already have configured — an SSH key, or a credential helper. The GitHub App
token Podiom holds is used only for listing repositories, downloading archive
snapshots, and marketplace rate limits; it is deliberately not reused as a git
credential.

Two consequences worth stating plainly:

- Podiom can only reach the repositories *you* can reach. If a clone or push
  fails, the fix is in your git setup, not in Podiom.
- Nothing Podiom writes contains a token. Remotes are stored as you entered
  them, and `internal/git` is guarded by a test asserting it references no
  token-based credential plumbing.

## Declaring source control on a project

The `git:` block in `~/.podiom/projects/projects.yaml`:

```yaml
projects:
  - id: app
    name: App
    git:
      enabled: true                        # false → no source control at all
      remote: git@github.com:me/app.git    # "" → a local repo, created in place
      default_branch: main
      branching: branch-per-task           # or: direct
      branch_prefixes: {feature: feature/, bugfix: fix/, chore: chore/}
      commit: ask                          # ask | auto
```

Three postures, expressed by two fields:

| `enabled` | `remote` | Meaning | What Podiom does |
| --- | --- | --- | --- |
| `false` | — | No source control | Plain directory; the agent runs no git |
| `true` | empty | A local repository | `git init` in the project's code directory |
| `true` | set | Lives on a host | Clones it, then works in the checkout |

A project with no `git:` block at all reads back as undeclared, which is treated
as disabled. Projects created before this existed keep working exactly as they
did until you opt in.

`branching: direct` commits to the default branch. `branching: branch-per-task`
puts each piece of work on its own branch, named `<prefix><slug>`.
`commit: ask` means the agent commits only when you ask; `commit: auto` lets it
commit its own completed work.

## How the agent sees it

Every turn on a project-bound session carries a one-line source-control anchor —
whether git is in play, which branch, and the branching and commit policies. The
detail sits behind an MCP tool instead of being re-sent each turn:

| Tool | What it does |
| --- | --- |
| `podiom_project_context` | The project's identity, paths, stack, notes, and full source-control state. Takes no arguments — the project is resolved from the session, so the agent cannot address the wrong one. |
| `podiom_start_work` | Applies the branching policy: creates and checks out the right branch on a branch-per-task project, confirms the default branch on a direct one. Idempotent. |

`podiom_start_work` is what makes the branching policy *real*. As prompt text,
"put each fix on its own branch" is a rule an agent can quietly skip; performed
by Podiom, the checkout either happened or it did not. The policy still appears
in the per-turn anchor so an agent that never calls the tool is not left
guessing.

## When git is not set up

A session on a git-enabled project still opens and still works when git is
missing or unconfigured — it just cannot do source control. The agent is told
to:

1. ask **once**, pointing you at Settings → Git;
2. if you decline, do the work anyway, run no git commands, and say plainly that
   the changes are uncommitted;
3. never ask again.

`GET /api/git/status` reports what is missing (binary, commit identity,
credentials); `POST /api/git/identity` saves the commit identity Podiom's agents
will use.

## Home Assistant add-on

`git` and `openssh-client` are both installed in the add-on image. `openssh-client`
is only a *Recommends* of `git`, and the image installs with
`--no-install-recommends`, so it is listed explicitly — without it every SSH
remote would fail.

The image sets `HOME=/data/home` so state lands on the persistent volume, and
that is where git looks for `~/.gitconfig`. OpenSSH does **not** follow `$HOME`:
it expands `~` from the passwd entry instead. Left alone, the add-on's root user
would send `ssh` and `ssh-keygen` to `/root/.ssh` — off `/data`, and therefore
wiped by every add-on update. The image edits root's passwd home to `/data/home`
so there is exactly one home: `~/.ssh` and `~/.gitconfig` both persist, and
`ssh-keygen` with no arguments writes a key that survives updates.

Podiom itself reads the public key from either home (`sshDirs` in
`internal/git/git.go`), so a key sitting in a passwd home Podiom's `$HOME` does
not point at is still found and shown in Settings → Git.
