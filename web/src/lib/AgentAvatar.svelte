<script lang="ts">
  // AgentAvatar — the single source of truth for how an agent's icon renders.
  // If the agent has an uploaded profile picture it shows that; otherwise it
  // falls back to the name-derived gradient tile with a monogram (the original
  // look). Every icon slot across the app renders through this component so a
  // picture appears everywhere at once.
  import { avatars } from "./avatars.svelte";
  import { agentGradient, avatarStyle, initial } from "./theme";

  let {
    name,
    size = 40,
    radius = Math.round(size * 0.31),
    fontSize = Math.round(size * 0.41),
    style = "",
    class: klass = "",
  }: {
    name: string;
    size?: number;
    radius?: number;
    fontSize?: number;
    style?: string;
    class?: string;
  } = $props();

  // Reactive to uploads/removals: version changes when a picture is set/cleared.
  let version = $derived(avatars.version(name));
  let url = $state<string | null>(null);

  $effect(() => {
    const v = version;
    const n = name;
    if (!v) {
      url = null;
      return;
    }
    let stale = false;
    void avatars.url(n, v).then((resolved) => {
      if (!stale) url = resolved;
    });
    return () => {
      stale = true;
    };
  });

  let imgStyle = $derived(
    [
      `width:${size}px`,
      `height:${size}px`,
      "flex:none",
      `border-radius:${radius}px`,
      "object-fit:cover",
      "box-shadow:0 8px 18px -8px rgba(80,40,20,.45)",
    ].join(";"),
  );
</script>

{#if url}
  <img src={url} alt={name} class={klass} style="{imgStyle};{style}" />
{:else}
  <div class={klass} style="{avatarStyle(agentGradient(name), size, radius, fontSize)};{style}">
    {initial(name)}
  </div>
{/if}
