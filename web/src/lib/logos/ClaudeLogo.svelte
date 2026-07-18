<script lang="ts">
  let { size = 16 }: { size?: number } = $props();
</script>

<svg
  class="provider-logo"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  aria-hidden="true"
  fill="none">
  <path d="M12 3.2v17.6" />
  <path d="M4.2 7.4 19.8 16.6" />
  <path d="M19.8 7.4 4.2 16.6" />
  <path d="M6.8 4.6 17.2 19.4" />
  <path d="M17.2 4.6 6.8 19.4" />
  <circle cx="12" cy="12" r="2.2" />
</svg>

<style>
  svg {
    stroke: #d3663f;
    stroke-width: 1.65;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  svg circle {
    fill: #d3663f;
    stroke: none;
  }
</style>
