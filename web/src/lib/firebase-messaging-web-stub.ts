// Stub for `firebase/messaging`, aliased in vite.config.ts.
//
// @capacitor-firebase/messaging ships a web implementation built on the Firebase JS SDK,
// and the bundler follows that import even though it is never reached: in a browser Podiom
// uses Web Push, and the native path only runs inside the Capacitor apps where the plugin's
// Swift and Kotlin implementations are used instead.
//
// Installing the real SDK would pull it into the browser bundle and make every
// self-hosted installation's browser talk to Podiom's Firebase project, which is exactly
// what the Web Push path avoids. Its peer dependency is optional for that reason.
//
// Each export throws rather than quietly returning nothing, so if this ever does get
// called the cause is obvious instead of appearing as push that silently never arrives.

const unavailable = () => {
  throw new Error(
    "firebase/messaging is not bundled: browsers use Web Push, and native push runs " +
      "through the Capacitor plugin's own platform implementation",
  );
};

export const getMessaging = unavailable;
export const getToken = unavailable;
export const deleteToken = unavailable;
export const onMessage = unavailable;

// isSupported answers rather than throws: the plugin calls it to decide whether the web
// implementation can be used at all, and the honest answer is no.
export const isSupported = async (): Promise<boolean> => false;
