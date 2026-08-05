package exec

import (
	"context"
	"os/exec"
	"strings"
)

// Command builds an *exec.Cmd for a discovered binary with a platform-specific
// process group attached, so the whole tree (the CLI plus any children it
// spawns) can be killed together when a turn is cancelled or an agent hangs
// (R10.4). Use Kill (not cmd.Process.Kill) to terminate the group.
//
// The returned Cmd is not yet started; callers wire up stdin/stdout/stderr first.
func Command(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	configureProcGroup(cmd)
	return cmd
}

// ProfileEnv returns env with name set to dir, or with name removed entirely
// when dir is empty. name is always stripped first, so an inherited value can
// never leak from one profile's directory into another profile's process
// (R8.32, R8.34-R8.37). The input slice is not modified.
func ProfileEnv(env []string, name, dir string) []string {
	if name == "" {
		return env
	}
	prefix := name + "="
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	if dir == "" {
		return out
	}
	return append(out, prefix+dir)
}

// Kill terminates the process and its entire process group. It is safe to call
// on a nil process or one that has already exited.
func Kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return killProcGroup(cmd)
}
