import { describe, expect, it } from "vitest";

import { remoteError } from "./gitremote";

// This table is deliberately the same one as TestValidateRemoteRejectsWhatGitWouldExecute
// in internal/git/git_test.go. The server is the authority; this exists to keep the two
// in step, because a client stricter than the server blocks a legitimate save.
describe("remoteError", () => {
  it.each([
    "git@github.com:owner/repo.git",
    "https://github.com/owner/repo.git",
    "http://git.internal/owner/repo",
    "ssh://git@host:22/owner/repo.git",
    "git://host/owner/repo.git",
    "file:///srv/git/repo.git",
    "/srv/git/origin.git",
  ])("accepts %s", (remote) => {
    expect(remoteError(remote)).toBe("");
  });

  // Empty is not an error here: it is how the panel says "local repository".
  it("treats an empty remote as a local repository rather than an error", () => {
    expect(remoteError("")).toBe("");
    expect(remoteError("   ")).toBe("");
  });

  it.each([
    "--upload-pack=touch /tmp/pwned",
    "-oProxyCommand=x",
    "ext::sh -c 'id'",
    "ssh://-oProxyCommand=x/y",
    "ftp://host/repo.git",
    "git@host:",
    "github.com/owner/repo",
    "https://host/a b",
    "git@host:owner\nrepo",
  ])("rejects %j", (remote) => {
    expect(remoteError(remote)).not.toBe("");
  });

  // TrimSpace on the Go side strips a trailing newline, so this must be accepted on both
  // sides. Only an embedded control character is a real problem.
  it("accepts a remote with trailing whitespace, as the server does", () => {
    expect(remoteError("git@github.com:owner/repo.git\n")).toBe("");
  });
});
