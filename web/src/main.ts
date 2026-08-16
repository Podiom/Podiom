import "./app.css";
import { mount } from "svelte";
import App from "./App.svelte";
import { hydrate } from "./lib/connection";

const target = document.getElementById("app");
if (!target) {
  throw new Error("missing #app mount point");
}

// Restore the configured instance before mounting. On native the address and
// token live in the platform keystore, which is async, and App.svelte issues
// its first requests as soon as it mounts — mounting first would fire those
// against the WebView's own origin and flash the connection screen at a user
// who is already set up. In a browser hydrate() resolves immediately.
//
// Deliberately not top-level await: that would force the build target up to
// es2022 for every browser the web app supports, to solve a problem only the
// native apps have. The previous `export default app` went with it; nothing
// imported this module.
void hydrate().then(() => {
  mount(App, { target });
});
