// The app registers this plugin itself — web/src/lib/discovery.ts calls
// registerPlugin("PodiomDiscovery") — so nothing imports this entry point. It
// exists only because Capacitor requires every plugin package to have a
// resolvable JS entry; keeping it hand-written avoids adding a bundler and a
// build step for a file that is never executed.
export {};
