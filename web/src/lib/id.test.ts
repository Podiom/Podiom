import { afterEach, describe, expect, it, vi } from "vitest";

import { randomID } from "./id";

// randomID sniffs globalThis.crypto at call time, so vi.stubGlobal alone lands
// each test on the branch it wants — no module reset or re-import needed.
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("randomID with crypto.randomUUID", () => {
  // Deliberately not UUID-shaped: a verbatim pass-through must not reformat
  // or regenerate what the runtime handed back.
  it("returns the runtime UUID verbatim", () => {
    vi.stubGlobal("crypto", { randomUUID: () => "runtime-id-123" });

    expect(randomID()).toBe("runtime-id-123");
  });

  it("prefers randomUUID when getRandomValues is also offered", () => {
    const getRandomValues = vi.fn();
    vi.stubGlobal("crypto", {
      randomUUID: () => "9ec3f8f1-6b1a-4b9f-9b8b-3d9a5a2a2a01",
      getRandomValues,
    });

    expect(randomID()).toBe("9ec3f8f1-6b1a-4b9f-9b8b-3d9a5a2a2a01");
    expect(getRandomValues).not.toHaveBeenCalled();
  });
});

describe("randomID with only crypto.getRandomValues", () => {
  // A deterministic fill makes the bit masking exact instead of probabilistic:
  // all-0xff leaves only the forced version/variant bits able to differ, and
  // all-0x00 proves those bits are set rather than merely preserved.
  function fillWith(byte: number) {
    return (array: Uint8Array) => array.fill(byte);
  }

  it("masks all-ones bytes down to a version-4 variant-1 UUID", () => {
    vi.stubGlobal("crypto", { getRandomValues: fillWith(0xff) });

    expect(randomID()).toBe("ffffffff-ffff-4fff-bfff-ffffffffffff");
  });

  it("forces the version and variant bits on all-zero bytes", () => {
    vi.stubGlobal("crypto", { getRandomValues: fillWith(0x00) });

    expect(randomID()).toBe("00000000-0000-4000-8000-000000000000");
  });

  it("renders arbitrary bytes in canonical version-4 UUID shape", () => {
    vi.stubGlobal("crypto", {
      getRandomValues: (array: Uint8Array) => array.map((_, i) => i),
    });

    const id = randomID();

    expect(id).toBe("00010203-0405-4607-8809-0a0b0c0d0e0f");
    expect(id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it("asks getRandomValues to fill a 16-byte buffer", () => {
    const getRandomValues = vi.fn(fillWith(0xff));
    vi.stubGlobal("crypto", { getRandomValues });

    randomID();

    const buffer = getRandomValues.mock.calls[0][0];
    expect(buffer).toBeInstanceOf(Uint8Array);
    expect(buffer).toHaveLength(16);
  });

  it("ignores a randomUUID that is not callable", () => {
    vi.stubGlobal("crypto", {
      randomUUID: "not-a-function",
      getRandomValues: fillWith(0xff),
    });

    expect(randomID()).toBe("ffffffff-ffff-4fff-bfff-ffffffffffff");
  });
});

describe("randomID without crypto", () => {
  it("returns an id-<base36>-<base36> string built from time and randomness", () => {
    vi.stubGlobal("crypto", undefined);
    vi.spyOn(Date, "now").mockReturnValue(1_788_436_800_000);
    vi.spyOn(Math, "random").mockReturnValue(0.123456789);

    expect(randomID()).toBe("id-mtlh3eo0-4fzzzxjy");
  });

  it("takes the same last resort when crypto offers neither method", () => {
    vi.stubGlobal("crypto", {});

    expect(randomID()).toMatch(/^id-[0-9a-z]+-[0-9a-z]{1,8}$/);
  });
});
