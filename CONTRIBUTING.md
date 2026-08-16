# Contributing to Podiom

Thanks for taking the time to improve Podiom. This project is a local-first
orchestration layer for agent CLIs, so contributions should keep the runtime
predictable, portable, and easy to inspect.

## Ways to contribute

- Report reproducible bugs with your OS, Podiom version or commit, command or
  UI path, expected behavior, and actual behavior.
- Propose features by describing the workflow they unlock and any security,
  storage, Home Assistant, or cross-platform implications.
- Improve docs when behavior is hard to discover or a command's output has
  changed.
- Send focused pull requests that solve one problem at a time.

Please do not include real tokens, agent transcripts, private workspace paths,
or sensitive logs in issues or pull requests. See [Security & logging](docs/security.md)
for the project's redaction and logging model.

## Development setup

Prerequisites:

- Go 1.26 or newer.
- Node.js 20 or newer.
- npm, available on your `PATH`.

Build everything:

```sh
make build
```

Run the daemon locally:

```sh
./bin/podiomd
```

Then open http://127.0.0.1:8787.

For frontend development with hot reload, keep `podiomd` running and start Vite.
Install from the repo root: `web/` is an npm workspace of the root package,
which also holds the Capacitor shell for the [mobile apps](docs/mobile.md), so
there is one install and one lockfile for both.

```sh
npm install
npm run dev -w web
```

## Validation

Before opening a pull request, run the checks that match your change:

```sh
go test ./...
go vet ./...
npm run check -w web
```

If you changed `capacitor.config.ts` or added a Capacitor plugin, re-sync the
committed native projects and include the result in your commit — CI fails
otherwise:

```sh
npm run sync
```

Useful shortcuts:

```sh
make test   # Go tests
make check  # go vet + Svelte check
```

For release or packaging changes, also run:

```sh
make package
```

For Home Assistant add-on changes, build and smoke-test the image where possible:

```sh
make ha-image
bash ha/test/smoke.sh ghcr.io/podiom/podiom-ha:dev
```

## Code guidelines

- Prefer small, explicit changes over broad refactors.
- Keep the daemon usable as a single self-contained Go binary with the embedded
  Svelte app.
- Preserve cross-platform behavior for Linux, macOS, and Windows.
- Keep runtime state rooted under `$PODIOM_HOME` unless the user explicitly
  configures otherwise.
- Treat agent output, tokens, paths, and run logs as sensitive by default.
- Add or update tests for behavior changes, storage migrations, permission
  handling, scheduling, CLI commands, and user-visible UI flows.
- Keep generated build artifacts out of commits unless they are intentionally
  tracked release or add-on assets.

Go code should be formatted with `gofmt`. Frontend changes should follow the
existing Svelte and TypeScript style in `web/src`.

## Pull request checklist

- The change is scoped to one bug, feature, or documentation improvement.
- Relevant docs under `README.md`, `docs/`, or `ha/addon/` are updated.
- Tests or checks have been run, or the PR explains why they could not be run.
- New configuration, storage, or API behavior is documented.
- Security-sensitive behavior has been reviewed against `docs/security.md`.

## Security reports

Please do not open a public issue for vulnerabilities or token/log exposure
risks. Use a private maintainer contact or GitHub's private vulnerability
reporting if it is enabled for the repository. Include the affected version,
impact, reproduction steps, and any suggested fix.

## License

By contributing, you agree that your contribution will be licensed under the
project's [MIT License](LICENSE).
