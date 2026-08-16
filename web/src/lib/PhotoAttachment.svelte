<script lang="ts">
  import { onMount } from "svelte";
  import { fetchPhotoAttachment } from "./api";
  import { isNative } from "./native";
  import type { Attachment } from "./types";

  let { attachment }: { attachment: Attachment } = $props();
  let previewURL = $state("");
  let failed = $state(false);
  let expanded = $state(false);

  onMount(() => {
    let active = true;
    void fetchPhotoAttachment(attachment.ID, true)
      .then((blob) => {
        if (!active) return;
        previewURL = URL.createObjectURL(blob);
      })
      .catch(() => {
        if (active) failed = true;
      });
    return () => {
      active = false;
      if (previewURL) URL.revokeObjectURL(previewURL);
    };
  });

  async function openOriginal() {
    // A WebView has no popup to open, and the browser path's fallback —
    // navigating the app document to a blob: URL — would replace the SPA with
    // no way back. Swap the cropped thumbnail for the full image in place.
    if (isNative) {
      try {
        const blob = await fetchPhotoAttachment(attachment.ID);
        if (previewURL) URL.revokeObjectURL(previewURL);
        previewURL = URL.createObjectURL(blob);
        expanded = true;
      } catch {
        failed = true;
      }
      return;
    }

    const popup = window.open("", "_blank");
    try {
      const blob = await fetchPhotoAttachment(attachment.ID);
      const url = URL.createObjectURL(blob);
      if (popup) popup.location.href = url;
      else window.location.href = url;
      window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch {
      popup?.close();
      failed = true;
    }
  }
</script>

<button class="photo" type="button" title={`View original ${attachment.Name}`} onclick={openOriginal}>
  {#if previewURL}
    <img src={previewURL} alt={attachment.Name} class:expanded />
  {:else if failed}
    <span class="missing">Photo unavailable</span>
  {:else}
    <span class="loading">Loading photo…</span>
  {/if}
  <span class="name">{attachment.Name}</span>
</button>

<style>
  .photo { display: grid; gap: 5px; width: min(260px, 100%); border: 0; padding: 0; background: transparent; color: inherit; text-align: left; cursor: zoom-in; }
  img { display: block; width: 100%; max-height: 240px; border-radius: 12px; object-fit: cover; background: #f1eee8; }
  /* Expanded in place on native, where there is no window to open the original in. */
  img.expanded { max-height: none; object-fit: contain; }
  .name { overflow: hidden; color: #81776d; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
  .loading, .missing { display: grid; min-height: 90px; place-items: center; border-radius: 12px; background: #f1eee8; color: #81776d; font-size: 12px; }
</style>
