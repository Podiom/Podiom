import { fileURLToPath } from "node:url";

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
  resolve: {
    alias: {
      // @capacitor-firebase/messaging ships a web implementation built on the Firebase
      // JS SDK. The bundler follows that import even though it is unreachable: browsers
      // use Web Push, and the native apps use the plugin's own Swift and Kotlin code.
      //
      // Installing the real SDK would pull it into the browser bundle and make every
      // self-hosted installation's browser talk to Podiom's Firebase project — which is
      // what the Web Push path exists to avoid. Its peer dependency is optional for that
      // reason, so it is stubbed rather than added.
      "firebase/messaging": fileURLToPath(
        new URL("./src/lib/firebase-messaging-web-stub.ts", import.meta.url),
      ),
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/healthz": { target: `http://${DAEMON}`, changeOrigin: true },
      // ws: the app's socket is /api/ws, so it matches this rule — without an
      // upgrade handler here the handshake silently fails in dev.
      "/api": { target: `http://${DAEMON}`, ws: true, changeOrigin: true },
    },
  },
});
