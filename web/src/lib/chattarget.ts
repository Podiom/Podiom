// What the shell hands Chat when something opens a conversation, and how much of it
// survives a trip through the URL.
//
// Only the session id is durable enough to live in a hash — see deeplink.ts. Everything
// else here is state the opener knows and a shareable link cannot: the first turn a
// roadmap start wants sent, the agent a new conversation should belong to. That split is
// what carryChatTarget exists to enforce.

// ChatTarget is one request to open a conversation.
//
// sessionId names an existing session; agentName instead starts a fresh one. seed is a
// first turn to send on the caller's behalf, and permission asks Chat to bring a pending
// approval forward — a notification about one lands here.
export interface ChatTarget {
  sessionId?: string;
  agentName?: string;
  seed?: string;
  permission?: boolean;
}

// carryChatTarget decides what the chat target becomes when the hash router lands on a
// session.
//
// Navigating assigns location.hash, and the hashchange that follows re-derives the target
// from the URL — which knows only the session id. A target parked by the caller a moment
// earlier has to survive that round trip: a roadmap start hands over the task prompt as
// `seed`, and losing it opens the session with nothing to send. The parked target is kept
// only when it names the same session the hash does, so a seed meant for one conversation
// can never leak into another.
//
// `permission` comes from the URL rather than from the parked target, so it is merged on
// top instead of being taken from whichever side won.
export function carryChatTarget(
  pending: ChatTarget | null,
  sessionId: string,
  permission = false,
): ChatTarget {
  const base = pending?.sessionId === sessionId ? pending : { sessionId };
  return permission ? { ...base, permission: true } : base;
}
