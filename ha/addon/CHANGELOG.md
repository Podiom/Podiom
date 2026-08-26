# Changelog

<!-- Release CI prepends an entry per release: version, commits since the
     previous tag, the bundled tool pins from ha/versions.env, and a note on
     any CLI version drift vs the previous entry. -->

## Unreleased

- **Opt-in mobile access for Home Assistant installs.** The new API-only
  `8100/tcp` port is disabled by default; map it to a host port such as `8787`
  under Network to connect the iOS or Android app on a trusted LAN. The port
  requires the existing gateway token and never exposes the dashboard,
  onboarding bootstrap, terminal, or token-exempt schedule webhooks.
- **New option: Language toolchains.** Pick which compilers and runtimes the
  container provides (`go`, `node`, `python`, `rust`, `swift`). They install
  into `/data/podiom/toolchains/` in the background on start and appear on
  every agent's `PATH`; unticking one deletes it. See DOCS.md.
- Defaults to `node` + `python`. **Existing installs** pick up that default on
  their next start, which downloads a ~90 MB Python interpreter in the
  background — untick `python` if you do not want it.
- The image now carries the system libraries those toolchains need in order to
  link (+177 MB), since `/usr` cannot be written at runtime.
- **Headless browsing now works out of the box.** The image carries Chromium's
  shared libraries (+238 MB), for the same reason as above — `/usr` cannot be
  written at runtime, so an agent could never install them itself. The browser
  itself is not bundled: the first `playwright install chromium` downloads it
  into `/data/home/.cache/ms-playwright`, on `/data`, where it survives updates
  — about 1 GB, so it counts against your backups.
  Firefox and WebKit are not supported. See DOCS.md.

## 0.0.0

- Initial packaging of Podiom as a Home Assistant add-on.
