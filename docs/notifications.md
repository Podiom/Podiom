# Notifications

Podiom does most of its work while nobody is watching: schedules fire, goals plan
and review themselves, roadmap tasks run, and agents get stuck needing a human.
Notifications are how that reaches you.

## The model

A **notification** is the record that something meaningful happened. A **delivery**
is one attempt to tell you about it through one channel. An **action** is an
operation against the underlying domain object. All three are separate, which is
what makes the awkward cases behave:

- A notification exists whether or not any delivery channel is enabled. Turning off
  Web Push does not stop Podiom recording what happened.
- A delivery failure never invalidates a notification, and never fails the thing
  that produced it. A dead push service cannot fail an agent turn, a schedule run,
  a goal run, or any domain operation.
- Reading a notification is not handling it. Seeing that an agent wants permission
  is not granting it.

Notifications carry an **importance** (`passive`, `normal`, `important`,
`critical`) independent of their type, and a **navigation target** — a logical token
such as `goal_action_item`, not a URL. Clients own the mapping from token to route,
so renaming a route cannot break a notification already sitting on a phone.

## Where a notification comes from

Every notification type lives in one table: `internal/notify/registry.go`. An entry
carries the type, its category and label for the settings screen, its importance,
whether it is on by default, the domain object it is about, its navigation target,
and the actions it may offer.

Adding a notification type means adding one entry. The label and grouping are served
to clients, so a new type appears in the web and mobile settings screens with no
client change. Several tests keep this honest: the registry must match the types
listed in `docs/requirements/notifications.md`, every registered type must have a
real producer, and no code outside `internal/notify` may write a type string
literal.

Most goal notifications come from a single subscription to the goal timeline.
`core.appendGoalEvent` is the only way a goal event is written, so a new event kind
cannot silently skip notifications — and because requests and their resolutions are
both timeline entries, one hook covers a goal's whole notification lifecycle.

## Preferences

Preferences answer *which events should notify me*. They are stored by `podiomd`,
never by a client, Firebase, or the relay.

The table holds only explicit choices. A type with no row uses the registry default,
which is what lets a notification type added in a later release arrive with its
intended default and no data migration, while an opt-out survives upgrades.

Events that block progress are on by default (questions, permission requests, action
items, access requests, completion proposals, rate limits, failures, tasks ready for
review). High-frequency informational ones are off (schedule started, schedule
succeeded, goal run started, progress, metric and plan updates).

The settings UI shows one switch per type. Switching one off writes a row for every
known channel, including channels this daemon is not running — otherwise the choice
would silently revert to the default the day a new channel shipped.

## Delivery

Delivery is best-effort and happens on a worker goroutine, so publishing a
notification never blocks the code that produced it. Publishing is non-blocking by
construction: a wedged push service costs a log line, not a stalled turn.

Two channels exist today, behind one interface:

- **Web Push** to browsers, using a VAPID keypair in `$PODIOM_HOME/push`.
- **Native push** to iOS and Android through the hosted Podiom Push Relay.

A self-hosted Podiom needs no Firebase project, no APNs certificate, and nothing to
provision by hand — only outbound HTTPS to the relay. The default is the hosted relay at
`https://push.podiom.org`; override it to point at a development or self-hosted one:

```yaml
notifications:
  relay_url: "https://push.podiom.org"
```

There is deliberately no credential in the configuration. The daemon **enrolls itself**
the first time a device is registered, and keeps what the relay issues in
`$PODIOM_HOME/relay.json` (mode 0600).

Enrollment is lazy on purpose: an installation that never registers a phone never contacts
Podiom infrastructure at all.

Two things follow from the credential being unrecoverable — the relay returns it once and
has no endpoint that reads it back:

- `relay.json` is worth backing up with the rest of `$PODIOM_HOME`. Losing it orphans the
  relay-side tenant, and re-registering is rate limited per address.
- An **unreadable** `relay.json` is a hard error, never a reason to re-enroll. Treating it
  as "not enrolled" would abandon the tenant and every device under it, permanently.

The relay's `instance_id` and Podiom's installation id are different things and neither
replaces the other. The instance id names the tenant a credential authenticates; the
installation id tells the mobile app which daemon to send an action back to, and never
authorizes anything.

### What leaves the installation

Push payloads carry the minimum needed to present the notification, say where it
came from, route a tap, and render the actions. They deliberately never carry
prompts, transcripts, tool output, environment values, file contents, secrets, or
the gateway token. A payload crosses infrastructure Podiom does not own, so anything
sensitive stays behind the authenticated API the notification navigates to.

The relay is a transport and nothing more. It holds no credential that can perform a
Podiom operation, and it is not in the return path: a notification action goes from
the app straight to the originating `podiomd`.

## Devices

One installation supports many devices. A device is registered by the mobile app
with an id the app generates, so a push-token refresh updates the existing
registration rather than creating a second one for the same phone.

Push tokens are sensitive routing information. They are accepted at registration and
never returned by any API, never written into notification history, and never
included in a payload. Delivery history records device ids.

Enabling and disabling a device is registration state — whether this phone receives
anything — and is separate from preferences, which decide which events matter.
Muting one device does not change what the others receive.

A device also carries a **status**, which is delivery health rather than the user's choice:
`active`, or `invalid` once the relay reports its registration gone. The row is kept rather
than deleted, so the label and the mute survive, and registering a fresh token makes it
active again on both sides. A device can be enabled and invalid at once — they answer
different questions, and collapsing them would let a token rotation silently un-mute a
phone.

Devices are addressed by the opaque id Podiom assigned, never by push token. The relay
resolves that id inside the authenticated tenant, which is what makes ownership structural:
a token in a request body carries no ownership record, so there would be nothing to check
it against.

## Installation identity

Each installation has a stable id in `$PODIOM_HOME/installation.id`, generated on
first run. It is a random value derived from nothing: an identity based on hostname,
IP, port, or a Home Assistant ingress path would make moving Podiom to another
machine — or reaching it over a different network path — look like a different
installation, and registered devices would appear to belong to a stranger.

It lives in a file rather than the database so it survives a database reset or a
restore independently, and it is returned only over the authenticated API.

## Actions

An actionable notification exposes a small closed set of operations. Which ones are
valid is computed per read against live domain state, never stored, because what a
notification can do keeps changing after it was recorded.

Some things are deliberately never offered as a direct action:

- Approving a credential request, because approving it means supplying the secret.
- Answering a question that is free-form, secret, multi-select, or has more than
  three options.
- Resolving a goal rate limit, which needs a provider, profile, model and effort
  chosen together.

Those navigate into Podiom instead.

Actions run through `POST /api/notifications/{id}/actions/{actionID}`, which
dispatches to the same core operation the web UI uses — a notification action and a
click in the dashboard are the same operation, not two implementations.

### Stale actions

A notification can outlive the thing it is about. Deny an access request at your
desk, then tap Approve on the notification still on your phone, and the approval must
not overwrite the decision you already made.

Three layers prevent it, and they cover different failures:

1. The action set is recomputed before anything runs. An action that is no longer
   offered — or should never have been, like approving a credential request —
   returns `409` with the actions that *are* valid and the resource's current state.
2. The store's guarded updates settle genuine races between two devices acting at
   once, refusing to touch a row that has moved on.
3. `409` rather than `400` tells a client "this moved on, refresh" instead of "you
   sent nonsense", and the response body lets it tell "already done, as I intended"
   apart from "resolved differently by someone else".

## Notification state

`unread` → `read` describes your attention. `resolved` describes the underlying
condition being handled. They are independent: reading never resolves, and resolving
the domain object from any surface — the dashboard, the CLI, an agent, another phone
— resolves the notification everywhere.

## The Notification Center

The web UI reaches it from a bell in the nav, which opens a slide-over panel rather
than occupying a route: notifications are about goals, schedules, tasks and sessions
alike, so they need to be reachable from wherever the user already is.

Two states are shown separately, because they mean different things. A dot marks a
notification not yet seen. An accent marks an actionable one still waiting on the
user. Reading an ask does not answer it.

The badge counts only what needs the user — unread notifications marked important or
critical. Counting every unread row would leave it permanently lit by routine progress
and run activity, which is precisely the signal it exists to carry.

Toasts follow the same rule: an arriving notification interrupts only if it is
important or critical, and its wording comes from the daemon rather than being written
a second time in the frontend.

## API

See [websocket.md](websocket.md) for the `notification`, `notification_update` and
`notifications_read_all` messages, the notification payload shape, and the full REST
surface.
