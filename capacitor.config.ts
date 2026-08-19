import type { CapacitorConfig } from "@capacitor/cli";

// Capacitor packages the existing Svelte SPA as the iOS and Android apps. There
// is no second frontend and no second backend: the same web/dist that podiomd
// embeds via go:embed is copied into both native projects, and both talk to a
// user-configured podiomd over its normal HTTP/WS API (R1, R2, R6).
const config: CapacitorConfig = {
  appId: "org.podiom.app",
  appName: "Podiom",
  webDir: "web/dist",
  server: {
    // The origins the WebView presents to the daemon. These are what
    // podiomd's CORS allowlist (internal/server/cors.go) must accept — the
    // two are a matched pair, so neither may change alone.
    //   iOS:     capacitor://localhost
    //   Android: https://localhost
    iosScheme: "capacitor",
    androidScheme: "https",
  },
};

export default config;
