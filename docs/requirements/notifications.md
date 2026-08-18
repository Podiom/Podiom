# Notification Management

## Background

Podiom performs a significant amount of work outside an active user session.

Schedules can execute automatically, goals can plan and review their own progress, roadmap tasks can be executed by agents, and sessions can reach states where the user may want to be informed or needs to take action.

Podiom already exposes structured domain events and lifecycle states suitable for notification handling, including:

- Schedule runs.
- Goal runs and goal timeline events.
- Goal progress and metric updates.
- Goal action items.
- Goal access requests.
- Goal completion proposals.
- Goal rate-limit blocks.
- Deferred questions from goals and schedules.
- Session questions.
- Session permission requests.
- Roadmap tasks entering review.
- Execution failures and other actionable states.

Notifications should therefore not be limited to exceptional cases where "Podiom needs the user".

When a Podiom mobile application is installed, notifications should become a central way for users to stay informed about autonomous agent activity and interact with Podiom without continuously monitoring the dashboard.

The user must remain in control of which events result in notifications.

---

## Goal

Provide a central notification system that:

- Maps meaningful Podiom domain events to notification events.
- Allows users to opt in or out of different notification types.
- Supports informational as well as actionable notifications.
- Supports native push notifications to the Podiom iOS and Android applications.
- Supports the existing Web Push channel.
- Allows actions to be performed directly from native notifications where appropriate.
- Opens the correct goal, schedule, task, or session when additional context is required.
- Stores notification history inside the user's own Podiom installation.
- Separates notification generation, notification delivery, and domain actions.
- Allows additional delivery channels to be introduced later without modifying notification producers.

---

## Architecture

Notification Management is part of `podiomd`.

`podiomd` remains the authoritative source for notification state, preferences, domain state, and user actions.

The architecture consists of three primary components.

### podiomd

`podiomd` owns:

- Notification event generation.
- Mapping Podiom domain activity to notifications.
- Notification preferences and subscriptions.
- Notification persistence.
- Read and unread state.
- Resolved state.
- Notification content.
- Available notification actions.
- Validation and execution of notification actions.
- Selecting destinations that should receive a notification.
- Notification Center APIs.

### Podiom Push Relay

The Podiom Push Relay is a centrally hosted, multi-tenant transport service operated by Podiom.

It owns:

- Authentication of independently operated Podiom installations.
- Device routing information required for native push.
- Firebase Cloud Messaging delivery.
- Isolation between Podiom installations.

The Push Relay does **not** own:

- Notification preferences.
- Notification history.
- Notification read state.
- Notification resolved state.
- Goals.
- Sessions.
- Schedules.
- Tasks.
- Agent state.
- Notification action execution.

Detailed requirements for the Push Relay are defined in `push-relay.md`.

### Podiom Mobile

The Capacitor-based iOS and Android applications own:

- Native push registration.
- Notification permission handling.
- Native notification presentation.
- Registration of the mobile device with Podiom.
- Displaying notification actions.
- Deep-linking into the appropriate Podiom resource.
- Sending actions directly to the originating `podiomd`.
- Handling situations where the originating Podiom installation is unreachable.

Conceptually:

```text
                    PODIOM INSTALLATION

Domain events
     │
     ▼
Notification Engine
     │
     ├── Preferences
     │
     ▼
Notification
     │
     ├──────────────► Notification Center
     │
     └── Delivery
           │
           ▼

══════════ Cloud boundary ══════════

      Podiom Push Relay
           │
           ▼
          FCM
       ┌───┴────┐
       ▼        ▼
     iOS      Android
    via APNs

════════════════════════════════════

       Podiom Mobile
           │
           │ user action
           ▼

      DIRECT CONNECTION

           │
           ▼
         podiomd
           │
           ▼
     Domain operation
```

The Push Relay is not part of the return path for notification actions.

---

# Requirements

## R1. Central notification model

Podiom MUST define a transport-independent notification model.

A notification SHOULD contain at least:

- Unique notification ID.
- Notification type.
- Notification category.
- Title.
- Human-readable body.
- Importance level.
- Creation timestamp.
- Agent name where applicable.
- Session ID where applicable.
- Goal ID where applicable.
- Schedule identifier where applicable.
- Task ID where applicable.
- Related domain resource.
- Navigation target.
- Whether user action is available.
- Available actions where applicable.
- Read state.
- Resolved state.

Transport-specific values such as FCM registration tokens MUST NOT be part of the core notification model.

---

## R2. Notification, Delivery, and Action separation

Podiom MUST treat the following as separate concepts:

### Notification

Represents something meaningful that happened in Podiom.

### Delivery

Represents an attempt to inform the user about the notification through a channel.

### Action

Represents a user operation against the underlying Podiom domain object.

Conceptually:

```text
GoalActionRequested
        │
        ▼
Notification
        │
        ├── Native Push delivery
        ├── Web Push delivery
        └── Future channel delivery

User selects "Done"
        │
        ▼
GoalActionItem domain operation
```

A notification MAY exist even if no external delivery channel is enabled.

A delivery failure MUST NOT delete or invalidate the notification.

---

## R3. Notification types

Each notification MUST have a stable machine-readable type.

Initial notification types SHOULD include:

```text
session.question
session.permission_required
session.action_required
session.execution_failed

schedule.started
schedule.succeeded
schedule.failed
schedule.question

goal.run_started
goal.run_succeeded
goal.run_failed
goal.progress
goal.metric_changed
goal.plan_changed
goal.question
goal.action_requested
goal.access_requested
goal.completion_proposed
goal.rate_limited
goal.status_changed

task.started
task.completed
task.review_required
task.failed

system.execution_failed
system.warning
```

Presentation text MUST NOT be used to determine notification behavior.

Notification type identifiers SHOULD remain backwards-compatible after release.

---

## R4. Notification categories

Notification preferences SHOULD be grouped into understandable user-facing categories.

Initial categories SHOULD include:

### Agent interaction

- Questions.
- Permission requests.
- Action required.
- Execution failures.

### Goals

- Goal runs.
- Progress updates.
- Metric updates.
- Plan changes.
- Questions.
- Action items.
- Access requests.
- Completion proposals.
- Failures.
- Rate limits.
- Status changes.

### Schedules

- Schedule started.
- Schedule succeeded.
- Schedule failed.
- Schedule asks a question.

### Tasks

- Task started.
- Task completed.
- Task ready for review.
- Task failed.

### System

- Important execution failures.
- Important warnings.

---

## R5. Notification preferences

Users MUST be able to configure which notification types result in external notifications.

The preference model MUST NOT assume that notification delivery is always native push.

Conceptually:

```text
NotificationSubscription

event_type: schedule.succeeded
channel: native_push
enabled: true
```

This SHOULD allow future configuration such as:

```text
                         Mobile Push   Discord
Questions                    ✓           ✓
Permission required          ✓           -
Schedule failed              ✓           ✓
Schedule succeeded           -           ✓
Goal progress                -           -
```

The initial UI does not need to expose this matrix.

The underlying model SHOULD support multiple delivery channels without requiring a future redesign.

---

## R6. Initial preference UI

The initial version SHOULD expose approximately the following configuration:

```text
Notifications

Agent interaction
✓ Questions
✓ Permission requests
✓ Action required
✓ Important execution failures

Goals
✓ Questions
✓ Action items
✓ Access requests
✓ Completion proposed
✓ Failures and blocked goals
✓ Rate limits
□ Goal run started
□ Goal run completed
□ Progress updates
□ Metric updates
□ Plan changes
□ Status changes

Schedules
✓ Questions
✓ Failures
□ Schedule started
□ Schedule completed successfully

Tasks
✓ Ready for review
□ Task started
□ Task completed

System
✓ Important failures
✓ Important warnings
```

The exact wording MAY evolve.

---

## R7. Default preferences

Events requiring user intervention SHOULD normally be enabled by default.

Examples:

- Agent question.
- Permission required.
- Goal action item.
- Goal access request.
- Goal completion proposal.
- Goal rate-limit block.
- Goal failure.
- Schedule failure.
- Task ready for review.

Potentially high-frequency informational notifications SHOULD normally be disabled by default.

Examples:

- Schedule started.
- Successful schedule completion.
- Goal run started.
- Routine goal progress.
- Metric updates.
- Plan changes.

Users MUST be able to opt into these events.

---

## R8. Notification preferences are Podiom state

Notification preferences MUST be stored by `podiomd`.

They MUST NOT be stored exclusively by:

- The mobile application.
- Firebase.
- The Podiom Push Relay.

Device registration and notification preferences MUST be separate concepts.

A device registration answers:

> Where can Podiom reach the user?

A notification preference answers:

> Which Podiom events should notify the user?

---

## R9. Goal notification sources

Existing goal activity SHOULD be mapped to notification candidates where appropriate.

Relevant goal events include:

```text
planning_started
review_started
progress
metric_update
plan_change
access_requested
status_change
completion_proposed
rate_limited
question_asked
action_requested
```

Tool-use audit events SHOULD NOT generate native notifications by default.

Goal run states SHOULD also be usable as notification sources:

```text
running
succeeded
failed
rate_limited
interrupted
```

---

## R10. Schedule notification sources

Users MUST be able to subscribe separately to at least:

- Schedule started.
- Schedule succeeded.
- Schedule failed.
- Schedule asked a question.

Successful schedule notifications SHOULD contain:

- Schedule name.
- Result indication or concise summary where available.
- Navigation to the durable session created by the run.

Failed schedule notifications SHOULD contain:

- Schedule name.
- Concise error indication.
- Navigation to the relevant run/session.

---

## R11. Task notification sources

Users SHOULD be able to subscribe to:

- Task started.
- Task completed.
- Task ready for review.
- Task failed where applicable.

A task entering `review` SHOULD be considered a high-value notification because it represents work awaiting user review.

---

## R12. Session notification sources

Interactive sessions SHOULD support notification candidates for:

- Agent question.
- Permission request.
- Action required.
- Important execution failure.

Normal assistant responses SHOULD NOT produce push notifications by default.

Support for optional "agent replied" notifications MAY be introduced later.

---

## R13. Goal action items

A newly created open Goal Action Item SHOULD be eligible for notification.

Example:

```text
Alice needs your help

Publish the Podiom release announcement.

[ Open ] [ Done ] [ Can't do ]
```

Where practical:

- `Done` SHOULD directly complete the action item.
- `Can't do` MAY either mark the item blocked or open Podiom when additional context is useful.
- `Open` MUST navigate to the action item.

The corresponding operation MUST be performed against `podiomd`.

---

## R14. Questions

Pending questions from:

- Interactive sessions.
- Goals.
- Schedules.

SHOULD be eligible for notifications.

Questions with simple predefined options MAY expose those options as native notification actions.

Example:

```text
Which release channel should I use?

[ Stable ] [ Beta ] [ Open ]
```

Questions requiring:

- Free-form text.
- Secret input.
- Complex multi-select input.
- Significant additional context.

SHOULD open the corresponding Podiom view.

Secret answers MUST NOT be entered through lock-screen notification actions.

---

## R15. Permission requests

Session permission requests SHOULD generate actionable notifications when enabled.

Example:

```text
Alice wants to execute a command

[ Deny ] [ Allow ]
```

The operation MUST use the existing Podiom permission mechanism.

The notification system MUST NOT introduce a separate permission state.

---

## R16. Goal access requests

Pending goal access requests SHOULD be eligible for notifications.

Where safe and no additional input is required:

```text
Alice requests access to GitHub MCP

[ Deny ] [ Approve ]
```

MAY be offered.

Requests requiring credentials, secrets, or additional configuration MUST open the relevant Podiom interface.

---

## R17. Goal completion proposals

When an agent proposes that a goal is complete, the notification SHOULD support:

```text
Alice thinks "Release Podiom 1.0" is complete

[ Review ] [ Mark done ]
```

`Review` MUST open the relevant goal.

`Mark done` MAY directly invoke the existing goal completion operation where the current state still permits it.

---

## R18. Goal rate limits

A pending goal rate-limit block SHOULD generate an important notification when enabled.

Because resolution may require selecting:

- Provider.
- Profile.
- Model.
- Effort.

the notification SHOULD normally navigate to the goal recovery UI rather than trying to resolve the condition directly.

---

## R19. Actionable notifications

A notification MAY expose native actions when the corresponding domain operation can safely be performed without opening the application UI.

Actions MUST be represented as structured data rather than inferred from notification text.

Example logical representation:

```json
{
  "notification_id": "not_123",
  "type": "goal.action_requested",
  "actions": [
    {
      "id": "open",
      "label": "Open"
    },
    {
      "id": "done",
      "label": "Done"
    }
  ]
}
```

Only a predefined set of application-supported action identifiers SHOULD be accepted.

---

## R20. Notification action execution

Notification actions MUST NOT be executed or proxied by the Podiom Push Relay.

The mobile application MUST send the operation directly to the originating `podiomd`.

Conceptually:

```text
Notification
"Alice requests GitHub access"

      [ Approve ]
           │
           ▼
     Podiom Mobile
           │
           │ authenticated API call
           ▼
        podiomd
           │
           ▼
AccessRequest → approved
```

The same Podiom domain operation MUST be used regardless of whether it originated from:

- Web UI.
- Mobile UI.
- Native notification action.

The Push Relay MUST NOT hold credentials capable of performing Podiom domain actions.

---

## R21. Action safety

Not every action SHOULD be executable directly from a notification.

The application SHOULD open Podiom when:

- Additional context should be reviewed.
- Free-form input is required.
- Secret input is required.
- Multiple fields are required.
- The action is destructive or unusually high-impact.
- The underlying native notification platform cannot represent the interaction appropriately.

---

## R22. Stale actions

Every action MUST be validated by `podiomd` against current domain state.

Example:

1. An access request generates a push notification.
2. The user denies the request from the desktop UI.
3. The user later taps `Approve` on the stale phone notification.

The approval MUST NOT overwrite the already-resolved state.

The server MUST reject stale or invalid actions safely.

The mobile client SHOULD update or dismiss the notification where appropriate.

---

## R23. Unreachable Podiom instances

Receiving a push notification does NOT imply that the mobile application can currently reach the originating Podiom installation.

For example:

```text
Home network

podiomd
   │
   └── outbound push ──► Push Relay ──► iPhone on 5G
```

The notification can reach the phone even though the phone cannot currently connect back to `podiomd`.

If a user performs an action while `podiomd` is unreachable:

- The action MUST NOT be reported as successful.
- The Push Relay MUST NOT execute the action.
- The notification SHOULD remain unresolved.
- The application SHOULD inform the user that Podiom is currently unreachable.
- The application SHOULD allow the user to retry once connectivity is restored.

High-impact actions MUST NOT initially be queued for automatic execution when connectivity returns.

---

## R24. Notification navigation

Every notification SHOULD have a navigation target.

Examples:

```text
session.question
    → session

session.permission_required
    → session permission request

schedule.failed
    → schedule run/session

schedule.succeeded
    → schedule run/session

goal.progress
    → goal timeline

goal.action_requested
    → goal action item

goal.question
    → goal question

goal.access_requested
    → goal access request

goal.completion_proposed
    → goal completion review

goal.rate_limited
    → goal recovery

task.review_required
    → roadmap task
```

Tapping the notification body MUST open the related resource where possible.

---

## R25. Notification importance

Notifications SHOULD support an importance level independent of notification type.

Initial levels SHOULD be:

```text
passive
normal
important
critical
```

Examples:

```text
Goal metric update          → passive
Schedule succeeded          → normal
Goal action requested       → important
Permission request          → important
Goal blocked                → important
Critical system failure     → critical
```

Delivery channels MAY map importance to platform-specific capabilities.

---

## R26. Notification persistence

Notifications SHOULD be persisted by `podiomd`.

Persistence enables:

- Notification history.
- Read/unread state.
- Resolution state.
- Multiple-device synchronization.
- Notification Center.
- Debugging.
- Opening old notifications.
- Recovering actionable requests after an OS notification has been dismissed.

External push delivery MUST NOT be the authoritative notification record.

---

## R27. Notification Center

Podiom SHOULD expose a Notification Center in the web and mobile interfaces.

Example:

```text
Notifications

Today

● Alice needs permission
  Session: Release preparation
  2 min ago

● Daily repository scan completed
  Schedule: Repository health
  21 min ago

● Release Podiom progressed to 82%
  Goal
  1 h ago

○ Documentation task completed
  Roadmap
  3 h ago
```

The Notification Center SHOULD:

- Display recent notifications.
- Distinguish unread notifications.
- Distinguish unresolved actionable notifications.
- Navigate to the related Podiom resource.
- Expose actions where appropriate.

---

## R28. Notification state

Notification state MUST distinguish at least:

```text
unread
read
resolved
```

`read` means the user has seen the notification.

`resolved` means the underlying actionable condition has been handled.

Reading a notification MUST NOT automatically resolve its underlying domain object.

Resolving the underlying:

- Question.
- Permission request.
- Goal action.
- Access request.
- Completion proposal.
- Other actionable object.

SHOULD resolve the corresponding notification.

---

## R29. Delivery state

Delivery state SHOULD be represented independently from notification state.

Conceptually:

```text
Notification
├── id
├── type
├── read_at
└── resolved_at

NotificationDelivery
├── notification_id
├── channel
├── destination
├── status
├── attempted_at
└── error
```

Initial delivery states MAY be limited to:

```text
pending
accepted
failed
```

Advanced end-device delivery receipts are not required.

---

## R30. Multiple devices

One Podiom installation MUST support multiple notification devices.

Example:

```text
Podiom Home

Notification preferences
├── schedule.failed = true
├── schedule.succeeded = false
└── goal.action_requested = true

Devices
├── iPhone
├── iPad
└── Android tablet
```

Initial behavior SHOULD deliver an enabled native-push notification to all enabled registered mobile devices.

Per-device preference overrides are NOT required initially.

---

## R31. Multiple Podiom installations

A notification MUST identify the Podiom installation that produced it.

The identifier MUST be independent of:

- IP address.
- Hostname.
- Current network path.
- Home Assistant ingress URL.

This allows future support for one mobile app being connected to multiple Podiom installations.

Changing the address of a Podiom installation MUST NOT conceptually create a new installation.

---

# Capacitor Mobile Requirements

The native Podiom applications are implemented using Capacitor and the existing Svelte/Vite frontend.

Notification Management requires additional native capabilities in both the iOS and Android applications.

---

## R32. Capacitor push integration

The Capacitor application MUST integrate native push notification support.

The common TypeScript/Svelte layer SHOULD own the application-level notification behavior.

Platform-specific native configuration MAY be used where required for:

- iOS notification capabilities.
- Android notification channels.
- FCM integration.
- Background notification handling.
- Native action categories.

The implementation SHOULD minimize duplicated Swift and Kotlin business logic.

---

## R33. Firebase Cloud Messaging registration

Both the iOS and Android applications MUST register with Firebase Cloud Messaging.

Android push MUST use FCM directly.

iOS push MUST use FCM with APNs as the Apple delivery transport.

The mobile application MUST receive an FCM registration token and make it available to the Podiom device-registration flow.

The mobile application MUST handle FCM token refresh.

---

## R34. Native notification permission

The mobile application MUST request notification permission using the appropriate native platform mechanisms.

Permission requests SHOULD occur in a user-understandable context rather than automatically before the user understands why Podiom wants notifications.

The application SHOULD explain that notifications can include:

- Agent questions.
- Permission requests.
- Goal activity.
- Schedule activity.
- Action items.
- Failures.

The user's OS-level denial MUST NOT prevent use of the rest of Podiom Mobile.

---

## R35. Device registration with podiomd

The mobile application MUST register itself with its connected `podiomd` instance.

The registration SHOULD include:

- Podiom mobile device ID.
- Platform.
- FCM registration token.
- Device/application metadata required for notification delivery.

The mobile application SHOULD use a Podiom-generated opaque device ID rather than expose FCM tokens throughout the Podiom domain model.

The FCM token MUST be treated as sensitive routing information.

---

## R36. Device registration synchronization

The mobile application MUST refresh its Podiom device registration when:

- The application is first configured.
- The FCM token changes.
- The user reconnects the application to a Podiom instance.
- The relevant push registration becomes invalid.
- Other platform lifecycle events make refresh necessary.

Registration updates SHOULD be idempotent.

---

## R37. Native notification actions

The iOS and Android applications MUST support a predefined set of native notification action groups.

Examples MAY include:

```text
permission_request
    ├── allow
    └── deny

goal_action_item
    ├── open
    ├── done
    └── blocked

access_request
    ├── approve
    └── deny

completion_proposal
    ├── review
    └── mark_done

question_choice
    └── dynamic/simple supported choices
```

Native action configuration SHOULD be derived from stable action identifiers supplied by Podiom.

Arbitrary server-supplied executable behavior MUST NOT be supported.

---

## R38. iOS actionable notifications

The iOS project MUST register the required native notification categories and actions.

The Capacitor application MUST be able to receive the selected action and translate it into the corresponding Podiom operation.

Notification actions MUST work when the application is:

- Foregrounded.
- Backgrounded.
- Launched as a result of the notification interaction.

---

## R39. Android actionable notifications

The Android project MUST support the notification channels and action buttons required by Podiom.

The application MUST be able to receive an action and translate it into the corresponding Podiom operation.

Notification actions MUST work when the application is:

- Foregrounded.
- Backgrounded.
- Launched as a result of the notification interaction.

---

## R40. Deep links and resource routing

The mobile application MUST support opening a specific Podiom resource from notification metadata.

The routing layer MUST support at least:

- Session.
- Goal.
- Schedule run.
- Task.
- Goal action item.
- Goal question.
- Goal access request.
- Goal completion review.

If the user is not currently connected to the originating Podiom instance, the application SHOULD first resolve/select that instance before opening the target.

---

## R41. App lifecycle

Notification handling MUST account for different mobile application states:

```text
foreground
background
terminated
```

The application MUST produce consistent routing and action behavior regardless of the state from which it was opened.

---

## R42. Foreground behavior

When the mobile application is actively displaying the relevant Podiom resource, it SHOULD avoid presenting redundant native notifications where practical.

In-app updates MAY instead use:

- Live UI updates.
- Toasts.
- Badges.
- Notification Center state.

External notification suppression MUST NOT cause the underlying persisted notification to disappear.

---

## R43. Connectivity handling

Before executing a native notification action, the mobile application MUST establish that it can reach the originating `podiomd`.

If connectivity is unavailable:

- The application MUST NOT display the action as completed.
- The local notification state SHOULD remain actionable.
- The user SHOULD receive a clear retryable error.

---

## R44. Secure mobile credentials

Credentials used by the mobile application to authenticate against `podiomd` SHOULD be stored using an appropriate secure native storage mechanism.

Notification payloads MUST NOT contain the Podiom gateway token or equivalent authentication credentials.

---

## R45. Notification settings in mobile app

The mobile application MUST expose the Podiom notification preference UI.

The settings SHOULD be read from and written to `podiomd`.

The mobile application MUST NOT maintain an authoritative separate copy of notification preferences.

OS-level notification permission status SHOULD be shown separately from Podiom's notification subscriptions.

For example:

```text
Push notifications
Enabled on this device ✓

Podiom notifications

Agent interaction
✓ Questions
✓ Permission requests

Goals
✓ Action items
□ Progress updates
```

---

## R46. Native badge support

The mobile application MAY display an application badge representing unread or unresolved notifications.

If implemented, the badge SHOULD derive from Podiom notification state rather than from locally accumulated push messages.

---

# Native Push Delivery

## R47. Podiom Push Relay

Native push MUST use the centrally hosted Podiom Push Relay.

Users running their own Podiom installation MUST NOT be required to:

- Deploy a Push Relay.
- Create a Firebase project.
- Configure FCM.
- Configure APNs credentials.
- Manage push certificates.

The official Push Relay serves all independently operated Podiom installations as a multi-tenant service.

Detailed requirements are defined in `push-relay.md`.

---

## R48. Push delivery flow

The expected delivery flow is:

```text
podiomd
    │
    │ authenticated outbound HTTPS
    ▼
Podiom Push Relay
    │
    ▼
Firebase Cloud Messaging
    │
    ├── Android
    └── APNs → iOS
```

`podiomd` MUST NOT require Firebase credentials.

A privately hosted Podiom instance only requires outbound HTTPS connectivity to the Push Relay.

---

## R49. Push payload privacy

Push payloads SHOULD contain the minimum content required to:

- Present the notification.
- Identify the originating Podiom installation.
- Identify the notification.
- Navigate to the relevant resource.
- Identify available native actions.

Sensitive data MUST NOT normally be included.

Examples that MUST NOT be included by default:

- Secrets.
- Gateway tokens.
- Environment variable values.
- Complete prompts.
- Complete transcripts.
- Authentication credentials.
- Large tool outputs.
- Sensitive file contents.

---

## R50. Notification previews

The architecture SHOULD allow a future privacy setting such as:

```text
Notification previews

○ Hide content
  "Podiom has a new notification"

● Show summary
  "Alice needs permission"

○ Show details
  "Alice wants to execute a command"
```

`podiomd`, not the Push Relay, determines which content is allowed in the payload.

---

## R51. Existing Web Push

Existing Web Push functionality SHOULD remain supported.

Native push MUST NOT replace Web Push.

Both SHOULD operate behind the notification delivery abstraction.

Conceptually:

```text
Notification
     │
     ▼
Dispatcher
     │
     ├── WebPushChannel
     └── NativePushChannel
```

---

## R52. Future channels

The architecture MUST allow additional delivery channels without modifying notification producers.

Potential future channels include:

- Discord.
- Slack.
- ntfy.
- Gotify.
- Pushover.
- Email.
- Generic webhook.

These channels are out of scope for the initial native push implementation.

---

# Failure behavior

## R53. Best-effort delivery

Notification delivery MUST be best-effort.

Failure to send an external notification MUST NOT fail:

- An agent turn.
- A schedule run.
- A goal run.
- A roadmap task.
- A domain operation.

Example:

```text
Schedule succeeds
      │
      ├── Persist successful ScheduleRun
      ├── Persist Notification
      └── Attempt push
             │
             └── Push Relay unavailable
```

The schedule remains successful.

---

## R54. Delivery failure logging

Push delivery failures SHOULD be logged separately from domain execution failures.

Where practical, `NotificationDelivery` SHOULD record the failure.

---

# CI — GitHub Actions

## R55. Backend tests

GitHub Actions SHOULD test:

- Domain event → notification mapping.
- Notification preference filtering.
- Default preferences.
- Notification persistence.
- Read/unread behavior.
- Resolution behavior.
- Notification action generation.
- Stale action rejection.
- Delivery-channel isolation.
- Multi-device behavior.
- Schedule mappings.
- Goal mappings.
- Task mappings.
- Question mappings.
- Access-request mappings.
- Permission mappings.

---

## R56. Mobile tests and validation

GitHub Actions MUST validate notification-related changes to the Capacitor applications.

CI SHOULD include:

- Svelte/TypeScript build.
- Capacitor sync.
- iOS project validation.
- Android project validation.
- Notification routing tests where practical.
- Action identifier mapping tests.
- FCM registration integration boundaries.
- Deep-link/navigation tests where practical.

Production Firebase credentials MUST NOT be required for normal pull request CI.

---

# Initial scope

The initial implementation SHOULD include:

1. Central notification domain model.
2. Persistent notification history.
3. Notification preferences.
4. Notification Center.
5. Goal notification mappings.
6. Schedule notification mappings.
7. Task review notifications.
8. Session question and permission notifications.
9. Native iOS and Android push registration.
10. Push Relay delivery integration.
11. Actionable native notifications for selected operations.
12. Deep-linking to Podiom resources.
13. Direct mobile-to-`podiomd` notification actions.
14. Existing Web Push compatibility.
15. Automated CI coverage.

---

# Out of scope

The initial implementation does not require:

- Slack notifications.
- Discord notifications.
- Email notifications.
- ntfy or Gotify.
- Notification digests.
- Quiet hours.
- Per-project notification preferences.
- Per-agent notification preferences.
- Per-goal notification preferences.
- Per-device preference overrides.
- Arbitrary commands executed directly from a notification.
- Secret entry from native notification actions.
- Push Relay proxying of Podiom domain actions.
- Guaranteed execution of actions while the Podiom installation is unreachable.
- Marketing or promotional notifications.

---

# Acceptance criteria

- Podiom has a central transport-independent notification model.
- Notifications, deliveries, and domain actions are separate concepts.
- Notifications are persisted by `podiomd`.
- Users can configure which Podiom events generate external notifications.
- Goal, schedule, session, and task activity are supported notification sources.
- Informational notifications can be enabled as well as actionable notifications.
- Sensible high-value notifications are enabled by default.
- Podiom exposes a Notification Center.
- Notifications support unread, read, and resolved states.
- Notification resolution follows the underlying domain state.
- Existing Web Push continues to work.
- iOS devices can register for native Podiom push.
- Android devices can register for native Podiom push.
- Multiple devices can be connected to one Podiom installation.
- Native push is delivered through the Podiom Push Relay and FCM.
- Users are not required to configure Firebase or APNs.
- Appropriate notifications expose native actions.
- Actions are executed directly against the originating `podiomd`.
- The Push Relay cannot execute Podiom domain actions.
- Stale actions cannot overwrite already-resolved state.
- Unreachable Podiom installations are handled safely.
- Tapping a notification opens the relevant Podiom resource.
- Capacitor iOS and Android applications support push registration, actions, lifecycle handling, and resource routing.
- Push delivery failures do not fail the originating Podiom operation.
- Notification functionality is covered by automated CI.
- Additional delivery channels can be introduced later without redesigning the notification domain.