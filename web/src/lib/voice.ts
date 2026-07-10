// Shared helper for the VoiceButton call sites: append a transcript to what
// the user already typed, joined with a single space.
export function appendTranscript(current: string, transcript: string): string {
  const base = current.replace(/\s+$/, "");
  if (!base) return transcript;
  return `${base} ${transcript}`;
}
