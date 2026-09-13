import { describe, expect, it } from "vitest";

import { MAX_PHOTO_BYTES, normalizePhoto } from "./photoAttachments";

// Every validation guard runs before normalizePhoto touches the DOM, so they
// are the only part of it the node environment can reach: a file that passes
// them still rejects, just inside loadBitmap where `window` is missing. That
// is enough to pin each guard by the message it owns — which is what the UI
// shows when someone drops in the wrong file.
describe("normalizePhoto", () => {
  it.each(["image/jpeg", "image/png", "image/gif", "image/webp"])(
    "lets a supported type (%s) past the guards",
    async (type) => {
      const file = new File(["pixels"], `photo.${type.slice("image/".length)}`, { type });

      await expect(normalizePhoto(file)).rejects.not.toThrow(
        /use JPEG, PNG, GIF, or WebP|the file is empty|10 MiB/,
      );
    },
  );

  it("rejects an unsupported type", async () => {
    const file = new File(["notes"], "notes.txt", { type: "text/plain" });

    // The file name prefixes every message, so it is part of the assertion.
    await expect(normalizePhoto(file)).rejects.toThrow(
      "notes.txt: use JPEG, PNG, GIF, or WebP.",
    );
  });

  it("rejects an empty file", async () => {
    const file = new File([], "empty.png", { type: "image/png" });

    await expect(normalizePhoto(file)).rejects.toThrow("empty.png: the file is empty.");
  });

  it("rejects a file over MAX_PHOTO_BYTES", async () => {
    const file = new File(["pixels"], "huge.png", { type: "image/png" });
    // defineProperty stands in for a 10 MiB allocation the suite does not need.
    Object.defineProperty(file, "size", { value: MAX_PHOTO_BYTES + 1 });

    await expect(normalizePhoto(file)).rejects.toThrow(
      "huge.png: photos must be 10 MiB or smaller.",
    );
  });

  // Only a boundary file keeps the `>` honest: `>=` rejects this one too.
  it("lets a file of exactly MAX_PHOTO_BYTES past the size guard", async () => {
    const file = new File(["pixels"], "limit.png", { type: "image/png" });
    Object.defineProperty(file, "size", { value: MAX_PHOTO_BYTES });

    await expect(normalizePhoto(file)).rejects.not.toThrow("10 MiB");
  });
});
