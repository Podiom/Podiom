# Capacitor-based Native Applications for Podiom

## Background

Podiom currently provides its user interface as a Svelte/Vite web application served by `podiomd`. The same interface is used to manage agents, sessions, projects, roadmap tasks, schedules, and goals.

There is a need to make Podiom available as native applications for both **iOS and Android** without introducing and maintaining separate mobile frontend implementations.

Capacitor provides a native runtime that can be added to the existing web application. It packages the existing HTML, CSS, and JavaScript application inside native iOS and Android applications while providing access to native platform APIs when required.

Capacitor should therefore be introduced as the foundation for Podiom's mobile applications.

Push notifications are explicitly **out of scope for the initial implementation**. The architecture should, however, allow native push notifications to be introduced later without requiring a different mobile application architecture.

## Goal

Provide installable native Podiom applications for:

- iOS
- Android

The applications should reuse the existing Svelte frontend and communicate with an existing `podiomd` instance rather than introducing a separate mobile frontend or Podiom runtime.

Conceptually:

```text
                  Podiom frontend
                  Svelte / Vite
                       │
             ┌─────────┴─────────┐
             │                   │
          Browser            Capacitor
                                 │
                         ┌───────┴───────┐
                         │               │
                       iOS             Android
                         │               │
                         └───────┬───────┘
                                 │
                         Podiom HTTP/WS API
                                 │
                              podiomd
```

## Requirements

### R1. Capacitor integration

Capacitor MUST be integrated with the existing Podiom web application.

The existing Svelte/Vite application MUST remain the primary frontend implementation.

A separate React Native, SwiftUI, Jetpack Compose, or other mobile frontend MUST NOT be required.

The native iOS and Android projects SHOULD be generated and maintained using Capacitor's standard project structure.

### R2. Shared UI

The iOS, Android, and browser applications MUST use the same underlying Svelte components wherever practical.

Platform-specific UI implementations SHOULD only be introduced where required by significant differences in platform behavior.

Changes made to common Podiom functionality SHOULD normally become available across:

```text
Web
iOS
Android
```

without implementing the feature three times.

### R3. Responsive mobile interface

The existing Podiom UI MUST be adapted to support phone-sized displays.

Desktop-oriented layouts such as persistent sidebars, multi-column views, dialogs, and navigation MAY use alternative responsive layouts on smaller screens.

Mobile-specific changes SHOULD primarily be implemented through responsive Svelte components and CSS rather than duplicated mobile components.

The web application MUST remain usable after these changes.

The mobile interface MUST account for native display constraints such as safe areas, status bars, device cutouts, and home indicators.

### R4. Native iOS application

The project MUST produce a native iOS application that can be opened, built, signed, and run using Xcode.

The application MUST support installation on physical iOS devices.

The architecture MUST allow future distribution through TestFlight and the Apple App Store.

### R5. Native Android application

The project MUST produce a native Android application that can be opened, built, signed, and run using Android Studio.

The application MUST support installation on physical Android devices.

The architecture MUST allow future distribution through Google Play.

### R6. Existing Podiom backend

The mobile applications MUST use an existing `podiomd` instance as their backend.

Capacitor MUST NOT introduce a second implementation of:

- Agents
- Sessions
- Goals
- Scheduling
- Projects
- Roadmap tasks
- Persistence
- Provider execution

These responsibilities remain with `podiomd`.

### R7. Initial connection screen

When no Podiom instance has been configured, the mobile application MUST display a dedicated connection screen before the main Podiom interface is shown.

The screen SHOULD visually and behaviorally resemble the existing web screen used to enter a **gateway token**, providing a familiar setup experience across Podiom clients.

Unlike the existing web flow, the mobile connection screen MUST allow the user to provide both:

- **Podiom address**
- **Gateway token**

For example:

```text
┌─────────────────────────────────┐
│                                 │
│             Podiom              │
│                                 │
│ Connect to Podiom               │
│                                 │
│ Address                         │
│ ┌─────────────────────────────┐ │
│ │ https://podiom.example.com  │ │
│ └─────────────────────────────┘ │
│                                 │
│ Gateway token                   │
│ ┌─────────────────────────────┐ │
│ │ ••••••••••••••••••••••••  │ │
│ └─────────────────────────────┘ │
│                                 │
│          [ Connect ]            │
│                                 │
│     ──────── or ────────        │
│                                 │
│   [ Find Podiom on network ]    │
│                                 │
└─────────────────────────────────┘
```

The application MUST validate that the configured address points to a reachable Podiom instance and that the supplied gateway token is accepted before completing setup.

The configured address and authentication information MUST persist between application launches.

Sensitive authentication information MUST be stored using an appropriate secure mechanism available to the native application.

If the stored connection can no longer be established, the user MUST have a way to return to the connection screen and change the address or gateway token.

### R8. Local Podiom discovery

The mobile application MUST provide an alternative to manually entering the Podiom address by allowing the user to search the local network for available Podiom gateways.

The initial connection screen MUST provide an action such as:

**Find Podiom on network**

When initiated, the application SHOULD discover Podiom instances available on the same local network as the mobile device.

Discovery SHOULD require minimal or no configuration of the Podiom installation.

Where practical, Podiom SHOULD use a standard local service discovery mechanism such as **mDNS/DNS-SD (Bonjour)** rather than scanning arbitrary IP addresses and ports.

Conceptually:

```text
iPhone / Android
       │
       │ local discovery
       ▼
 ┌───────────────────────────────┐
 │ Local network                 │
 │                               │
 │ MacBook       Podiom ✓        │
 │ Home Assistant Podiom ✓       │
 │ NAS           -               │
 └───────────────────────────────┘
```

Discovered instances SHOULD be presented to the user:

```text
Podiom instances

● Podiom on MacBook
  192.168.1.42:8787

● Podiom on Home Assistant
  homeassistant.local

[ Search again ]
```

Selecting an instance MUST populate or establish the Podiom address without requiring the user to manually type it.

The user MUST still provide a valid gateway token unless a future authentication mechanism removes that requirement.

Manual address entry MUST remain available when discovery fails or when the Podiom instance is not on the same local network.

The discovery implementation MUST account for native platform requirements for local network access, including any permissions required by iOS or Android.

### R9. Podiom instance configuration

The mobile application MUST support connecting to a user-configured Podiom endpoint.

The connection model MUST NOT assume that `podiomd` is available at `127.0.0.1`, as localhost from within the mobile application refers to the mobile device itself.

Supported addresses MAY include:

```text
https://podiom.example.com
https://podiom.internal.example
http://192.168.1.50:8787
```

The application SHOULD support both instances discovered automatically on the local network and instances configured manually by address.

HTTP and WebSocket communication MUST use the configured Podiom instance consistently.

### R10. Home Assistant hosted Podiom

The mobile application MUST support connecting to Podiom when `podiomd` is running as a **Home Assistant add-on**.

The supported v1 connection is the add-on's opt-in, API-only LAN listener. Home
Assistant Supervisor MUST declare its container port disabled by default and
allow the user to map it to a chosen host port (`8787` is recommended, not
assumed). The user enters `http://<HA-LAN-IP>:<mapped-port>` manually.

The mobile application MUST NOT treat a Home Assistant sidebar or
`/api/hassio_ingress/...` URL as a Podiom endpoint. Those paths require an HA
browser session and are not a stable standalone-client API.

A Home Assistant-hosted Podiom instance MAY be omitted from local discovery
when the add-on container cannot reliably advertise the user-selected host
port. Manual address entry remains mandatory.

The LAN listener MUST expose only Podiom health, HTTP API, and WebSocket
traffic. It MUST require the gateway token on every API/WS request and MUST NOT
expose the HA-authenticated SPA, terminal, onboarding bootstrap, or
token-exempt schedule webhook surface.

The Capacitor application SHOULD use the same Podiom HTTP and WebSocket APIs regardless of whether the target instance is:

```text
Standalone podiomd
Container
Home Assistant add-on
Remote/server installation
```

HTTP and WebSocket behavior MUST remain identical to standalone Podiom after
the direct base URL is selected. Podiom MUST document the Supervisor port
mapping, gateway-token retrieval, trusted-LAN limitation, and the fact that
plain HTTP carries the token in cleartext.

### R11. Native capabilities

Capacitor MUST be the abstraction used when Podiom requires access to native device functionality that cannot reasonably be provided by the web runtime.

Future capabilities may include:

- Push notifications
- Deep links
- Biometric authentication
- Haptic feedback
- App lifecycle handling
- Native sharing
- Secure credential storage
- Local network discovery

These capabilities do not need to be implemented as part of the initial mobile application work unless required elsewhere in this specification.

### R12. CI — GitHub Actions

The existing GitHub Actions CI pipeline MUST be extended to include the Capacitor applications.

CI MUST verify that changes to the shared Svelte application remain compatible with the native projects.

At minimum, CI MUST:

- Build the existing Svelte/Vite frontend.
- Synchronize the generated web assets with Capacitor.
- Validate the Capacitor configuration.
- Build or otherwise validate the Android project.
- Build or otherwise validate the iOS project on a macOS GitHub Actions runner.
- Fail when changes cause either native project to become invalid or unsynchronizable.

The CI structure SHOULD reuse the existing Podiom build and test pipeline where practical.

Conceptually:

```text
GitHub Actions

        ┌── Go tests/build
        │
Commit ─┼── Svelte/Vite build
        │
        ├── Capacitor sync
        │
        ├── Android build/validation
        │
        └── iOS build/validation
```

App Store and Google Play publishing are NOT required as part of the initial implementation.

Signing credentials and production release automation SHOULD NOT be required for normal pull request CI.

The CI design SHOULD make it possible to introduce signed TestFlight, App Store, and Google Play builds later without restructuring the mobile projects.

## Push notifications

Native push notifications are **not part of the initial implementation**.

The first version of Podiom Mobile only needs to establish the native application foundation and connectivity to `podiomd`.

A future requirement may introduce:

```text
podiomd
    │
    ▼
Podiom Push Service
    │
    ├── APNs ──► Podiom iOS
    │
    └── FCM  ──► Podiom Android
```

Introducing push notifications later MUST NOT require replacing the Capacitor-based mobile architecture.

## Out of scope

The initial implementation does not include:

- Push notifications through APNs or FCM.
- A Podiom-hosted push relay.
- Reimplementation of the Podiom UI using native components.
- A separate mobile backend.
- Running Claude Code or Codex directly on the mobile device.
- Podiom-hosted remote-access infrastructure for exposing local installations over the internet.
- Automatic App Store or Google Play publishing.
- Production application signing as part of normal CI.
- Feature parity work unrelated to making the existing Podiom UI usable on mobile.

## Acceptance criteria

- Podiom's existing Svelte/Vite frontend is integrated with Capacitor.
- An iOS Capacitor project exists and can run Podiom on a physical iPhone.
- An Android Capacitor project exists and can run Podiom on a physical Android device.
- Both applications use the existing Svelte frontend rather than separate mobile implementations.
- Core Podiom functionality remains backed by `podiomd`.
- The UI is usable at common phone screen sizes.
- Mobile-specific layout changes do not break the desktop web interface.
- A first-time mobile user is presented with a Podiom connection screen before entering the application.
- The connection screen allows both Podiom address and gateway token to be entered.
- The connection screen visually follows the existing Podiom gateway-token experience where practical.
- The application can validate the configured Podiom instance and gateway token.
- Connection information persists between application launches.
- The user can return to the connection screen to change the configured instance.
- The user can search the local network for available Podiom gateways instead of manually entering an address.
- Discovered Podiom instances can be selected directly from the discovery result.
- Manual address entry remains available when local discovery is unavailable or unsuccessful.
- A Podiom instance running as a Home Assistant add-on can be connected to using a supported network configuration.
- HTTP and WebSocket communication work from the native applications.
- The architecture supports adding additional Capacitor native plugins in the future.
- GitHub Actions validates both the iOS and Android projects.
- Pull requests fail CI when changes break the Capacitor/native builds.
- No APNs, FCM, or Podiom Push Service implementation is required for completion of this work.

## Future work

Once the Capacitor applications are established, subsequent work can introduce native push notifications.

A likely next phase would cover:

- Device registration.
- APNs integration for iOS.
- FCM integration for Android.
- Podiom push relay.
- Notification deep links.
- Notification actions.
- Secure remote connectivity.
- TestFlight publishing.
- App Store publishing.
- Google Play publishing.

Push notifications should be treated as a separate capability built on top of the Capacitor foundation rather than a prerequisite for the initial Podiom mobile applications.
