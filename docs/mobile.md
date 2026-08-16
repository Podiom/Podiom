# Podiom Mobile (iOS & Android)

Podiom ships native iOS and Android apps built with [Capacitor](https://capacitorjs.com).
They are not a separate product: they package the same Svelte UI that `podiomd`
serves in a browser, and they talk to an existing daemon over its normal HTTP and
WebSocket API. Nothing about agents, sessions, goals, scheduling or persistence
moves onto the phone.

```text
                  Podiom frontend
                  Svelte / Vite
                       │
             ┌─────────┴─────────┐
             │                   │
          Browser            Capacitor
                                 │
                         ┌───────┴───────┐
                       iOS             Android
                         │               │
                         └───────┬───────┘
                                 │
                         Podiom HTTP/WS API
                                 │
                              podiomd
```

## Making a daemon reachable from your phone

By default `podiomd` listens on `127.0.0.1`, which a phone cannot reach —
`localhost` on a phone is the phone. Bind it to your network and restrict who
may connect, in `~/.podiom/config.yaml`:

```yaml
server:
  bind: 0.0.0.0
  port: 8787
  allow_from: ["192.168.1.0/24"]   # your LAN; loopback is always allowed
```

`allow_from` is a source-IP guard applied before authentication. It is strongly
recommended whenever `bind` is not loopback — see [security.md](security.md).

> **No TLS.** `podiomd` speaks plain HTTP. On your own LAN that is usually an
> acceptable trade, but the gateway token crosses the network in cleartext.
> Across anything less trusted, terminate TLS in front of the daemon with a
> reverse proxy and give the app the `https://` address.

### Home Assistant add-on

Home Assistant's sidebar URL (for example
`http://192.168.1.7:8123/6142004a_podiom`) is an Ingress entry, not a Podiom API
address. It loads the Home Assistant frontend and depends on an HA browser
session, which the standalone Podiom app does not have.

The add-on instead provides a separate API-only LAN port. It is disabled by
default:

1. Open the Podiom add-on in Home Assistant and go to **Configuration**.
2. Expand **Network** / **Show disabled ports**.
3. Map the **Podiom mobile API** (`8100/tcp`) to a free host port. `8787` is the
   recommended value.
4. Save and restart the add-on.
5. Copy the **Gateway token** from the add-on's Configuration page.
6. In the Podiom mobile app enter `http://<HA-LAN-IP>:<host-port>` — for example
   `http://192.168.1.7:8787` — plus that token.

Do not add the sidebar path. The LAN listener exposes `/healthz`, the Podiom API,
and WebSocket endpoint only; opening its root URL in a browser deliberately
returns 404. The terminal, onboarding bootstrap, and web dashboard remain
available only through authenticated HA Ingress.

The port is plain HTTP and the gateway token crosses the LAN in cleartext. Use
it only on a trusted local network. Keep using HA Ingress/Nabu Casa for remote
browser access; the mobile app does not currently authenticate with HA or use
this port over the internet.

## Connecting

On first launch the app shows a connection screen asking for two things:

- **Address** — `http://192.168.1.50:8787`, `https://podiom.example.com`, or a
  host with no scheme (`192.168.1.50:8787`), which is read as `http://`. A
  reverse-proxy sub-path (`https://example.com/podiom/`) works too.
- **Gateway token** — get it with `podiom token show`, or copy it from the
  Podiom add-on's Configuration page on Home Assistant.

Both are checked before anything is stored: the app calls `/healthz` to confirm
the address really is a Podiom instance, then `/api/auth/check` to confirm the
token is accepted, so the error tells you which half is wrong.

The address and token persist between launches. The token is kept in the
platform keystore — the iOS Keychain, Android's `EncryptedSharedPreferences` —
never in `localStorage`.

**To connect to a different instance**, open Settings → General → Podiom
instance → Disconnect. That forgets both the address and the token. If only the
token has gone stale (after `podiom token rotate`), the app returns to the
connection screen by itself with the address still filled in.

### Finding instances automatically

`podiomd` advertises itself on the local network over mDNS/DNS-SD as
`_podiom._tcp`, so **Find Podiom on network** on the connection screen lists the
instances it can see. Selecting one fills in the address; you still supply the
token.

Advertising is on by default and can be turned off:

```yaml
server:
  advertise: false
```

It is skipped automatically when `bind` is loopback — announcing an address no
other device can reach would list an instance that then fails to connect — and
in Home Assistant mode. An add-on's user-selected host port cannot be advertised
reliably from its container, so enter HA-hosted instances manually.

The first search triggers a system permission prompt for local network access on
iOS. If discovery finds nothing, type the address instead; it is always
available.

> **Debugging discovery on a macOS host:** `dns-sd -B _podiom._tcp` may show
> nothing even while the daemon is advertising correctly. `dns-sd` asks the
> system `mDNSResponder`, which does not pick up a second responder sharing port
> 5353 on the same machine. Phones on the LAN query by multicast and do see it.

### Connection troubleshooting

- **“Not a Podiom API” for a Home Assistant address:** you entered the sidebar
  or Ingress URL. Enable the mobile API port and enter only
  `http://<HA-LAN-IP>:<mapped-port>`.
- **Cannot reach the address:** confirm the port mapping was saved, restart the
  add-on, and verify the phone is on the same LAN rather than an isolated guest
  network. Check that the selected host port is not used by another service.
- **Token rejected / HTTP 401:** copy the current token from the add-on's
  Configuration page. If it was rotated, the old value stops working
  immediately.
- **iOS cannot see local instances:** allow Podiom under **Settings → Privacy &
  Security → Local Network**. This permission also governs direct LAN requests.
- **Discovery finds standalone Podiom but not the HA add-on:** expected; Home
  Assistant instances use manual address entry.

## Building the apps

Everything is driven from the repo root, where the Capacitor project lives. The
Svelte app in `web/` is an npm workspace of it, so there is one `npm install`
and one lockfile.

```bash
npm install          # installs the root shell and web/ together
npm run sync         # builds web/dist and copies it into ios/ and android/
```

Then open the project in the platform IDE:

```bash
npx cap open ios       # Xcode
npx cap open android   # Android Studio
```

Run on a device from the IDE, or:

```bash
npx cap run ios
npx cap run android
```

Both `ios/` and `android/` are committed. `npx cap sync` regenerates parts of
them (adding a plugin rewrites the iOS `Package.swift` and the Android gradle
files), so **run `npm run sync` and commit the result whenever you change
`capacitor.config.ts` or add a plugin** — CI fails otherwise.

Requirements: Node 22+, Xcode 15+ for iOS, JDK 21 and the Android SDK for
Android. Capacitor 8 resolves iOS dependencies with Swift Package Manager, so
CocoaPods is not needed.

## App icon and launch screen

The generated icons and launch artwork are committed, but they are outputs — the
sources are the SVGs in `assets/`, and everything under
`ios/App/App/Assets.xcassets` and `android/app/src/main/res/{mipmap,drawable}-*`
is regenerated from them:

```bash
npm run assets       # capacitor-assets generate --ios --android
```

| Source | Feeds |
| --- | --- |
| `assets/icon-only.svg` | iOS app icon, Android legacy icon |
| `assets/icon-foreground.svg` | Android adaptive-icon foreground |
| `assets/icon-background.svg` | Android adaptive-icon background |
| `assets/splash[-dark].svg` | iOS `Splash` imageset, Android `drawable-*/splash.png` |

Edit the SVGs rather than the PNGs, then re-run the command and commit the
result. Each source file carries a comment explaining its sizing, which is not
arbitrary: the Android foreground compensates for the 16.7% inset the generator
writes into `mipmap-anydpi-v26/ic_launcher.xml`, and the two splash files are
identical because Podiom's UI is light-only.

iOS has one hand-maintained piece the generator does not touch — the
`LaunchBackground` colour set, which `UILaunchScreen` in `Info.plist` paints
behind the centred splash image. It must stay equal to the splash background
(parchment `#f4ece2`), or the uncovered margin on tall 3x devices reads as a
band. See the comment on that key in `ios/App/App/Info.plist`.

The apps deliberately ship **no storyboards**. `SceneDelegate` builds the window
and root `CAPBridgeViewController` in code, and the launch screen is the
declarative `UILaunchScreen` dictionary, so a `Main.storyboard` or
`LaunchScreen.storyboard` reappearing after a Capacitor upgrade is template
drift to delete, not a fix to keep.

## Native code

Capacitor is the only bridge to native APIs. Plugins in use:

| Plugin | Why |
| --- | --- |
| `@aparajita/capacitor-secure-storage` | Keychain / Keystore for the gateway token |
| `@capacitor/app` | Android back button, app lifecycle |
| `@capacitor/browser` | Sign-in flows that need a real browser |
| `@capacitor/keyboard` | Resize behaviour for the chat composer |
| `@capacitor/status-bar` | Status bar styling |
| `podiom-discovery` | First-party mDNS browse — see `plugins/podiom-discovery` |

`podiom-discovery` is in-repo rather than a dependency: the browse is about 150
lines per platform (`NWBrowser` on iOS, `NsdManager` on Android), and this app
holds a credential that grants shell access to a machine.

## Not included

Push notifications are deliberately out of scope for now. Web Push is disabled
on native because it needs a service worker, which a WebView does not provide;
adding APNs/FCM later is a separate piece of work that builds on this
foundation rather than replacing it.

Also absent: App Store / Google Play publishing, release signing in CI, and any
reimplementation of the UI in native components.

## CI

`.github/workflows/ci.yml` builds the Android debug APK on Ubuntu and the iOS
app for the simulator on macOS, both unsigned, on every pull request. Each job
runs `npx cap sync` and then `.github/scripts/check-native-sync.sh`, which fails
if the committed native projects have drifted from the Capacitor configuration.
