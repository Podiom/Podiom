// waveform.ts — the equalizer figure both full-screen gates draw.
//
// The bar heights and which of them are accented are part of the visual
// identity, not per-screen decoration: the connection screen and the offline
// screen show the same silhouette so the app reads as one place even when it
// cannot reach anything. Only the colour and animation of a bar differ between
// them, and each screen owns that itself.

export const WAVE_HEIGHTS = [
  16, 24, 20, 32, 26, 40, 30, 22, 36, 28, 46, 34, 24, 42, 52, 38, 58, 44, 60, 44, 58, 38, 52, 42,
  24, 34, 46, 28, 36, 22, 30, 40, 26, 32, 20, 24, 16,
];

export const WAVE_ACCENTS = new Set([6, 14, 18, 22, 30]);
