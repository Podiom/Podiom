import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";

// The SPA builds to web/dist/, which is embedded into podiomd via go:embed.
// During development, `npm run dev` serves on 5173 and proxies API/WebSocket
// traffic to a locally running podiomd (default 127.0.0.1:8787) so the single
// origin assumption of the embedded build also holds in dev.
const DAEMON = process.env.PODIOM_ADDR ?? "127.0.0.1:8787";

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  // Relative base: the built app must render under HA Ingress's rewritten
  // sub-path (/api/hassio_ingress/<token>/...), so asset URLs cannot assume
  // the origin root (HA14). podiomd injects a <base href> for deep paths.
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/healthz": { target: `http://${DAEMON}`, changeOrigin: true },
      "/api": { target: `http://${DAEMON}`, changeOrigin: true },
      "/ws": { target: `ws://${DAEMON}`, ws: true, changeOrigin: true },
    },
  },
});
