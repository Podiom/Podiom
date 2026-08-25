// Whether a string is a git remote Podiom will accept, mirroring
// git.ValidateRemote in internal/git/git.go.
//
// The server is the authority — this exists so the user is told what is wrong
// while they type, rather than after a round trip. It must never be stricter
// than the Go side, or it blocks a save the server would have honoured.

// SCHEMES are the transports git can fetch over that Podiom is willing to hand it.
const SCHEMES = ["https", "http", "ssh", "git", "file"];

// UNSAFE matches a space or a control character. Neither belongs in a remote, and
// both are rejected on the Go side too so the two agree on the boundary.
const UNSAFE = /[\x00-\x1f\x7f ]/;

// remoteError names what is wrong with a remote, or returns "" when it is fine.
//
// The interesting cases are not typos. Podiom passes argv arrays, so a remote can
// never be reinterpreted as a shell string — but git itself reads a leading "-" as
// one of its own options and "<helper>::<arg>" as a remote helper it will execute,
// which is the same class of problem one level down.
export function remoteError(remote: string): string {
  const value = remote.trim();
  if (!value) return ""; // Empty means "local repository", which is a real answer.
  if (value.startsWith("-")) {
    return "A remote cannot start with a dash — git would read it as one of its own options.";
  }
  if (value.includes("::")) {
    return "That looks like a git remote helper, which git would execute. Use an https:// or ssh:// URL.";
  }
  if (UNSAFE.test(value)) {
    return "A remote cannot contain spaces or control characters.";
  }
  // An absolute path is a local repository — a bare repo on disk or a mount.
  if (value.startsWith("/")) return "";

  const schemeEnd = value.indexOf("://");
  if (schemeEnd >= 0) {
    const scheme = value.slice(0, schemeEnd);
    if (!SCHEMES.includes(scheme)) {
      return `Git cannot fetch over "${scheme}". Use https, ssh, git or file.`;
    }
    const host = hostOf(value.slice(schemeEnd + 3).split("/")[0]);
    if (!host && scheme !== "file") return "That URL has no host.";
    if (host.startsWith("-")) return "That URL has a host git would read as an option.";
    return "";
  }

  // Otherwise the scp-like form: [user@]host:path.
  const colon = value.indexOf(":");
  if (colon < 0) {
    return "That is not a git remote. Expected an https:// or ssh:// URL, or host:path.";
  }
  const host = hostOf(value.slice(0, colon));
  if (!host || host.includes("/") || host.startsWith("-")) {
    return "That is not a git remote. Expected an https:// or ssh:// URL, or host:path.";
  }
  if (!value.slice(colon + 1)) return "That names a host but no repository.";
  return "";
}

// hostOf drops the optional user@ prefix.
function hostOf(authority: string): string {
  const at = authority.indexOf("@");
  return at < 0 ? authority : authority.slice(at + 1);
}
