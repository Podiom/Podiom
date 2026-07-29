package onboard

import (
	"context"
	"fmt"
	"io"
	"strings"

	podiomexec "github.com/Podiom/Podiom/internal/exec"
	podiomgit "github.com/Podiom/Podiom/internal/git"
)

// offerGitSetup walks the user through making git usable, so a project that
// wants source control finds it ready.
//
// Every part is skippable and nothing here fails onboarding: an agent can work
// on a project without git, it just cannot commit. Podiom sets up the user's
// own git — it never creates credentials of its own.
func offerGitSetup(ctx context.Context, u *ui, out io.Writer) error {
	section(out, "Git access")

	status := podiomgit.Check(ctx, podiomexec.Discovery{})
	if !status.Found {
		fmt.Fprintln(out, warnStyle.Render("git was not found on this machine."))
		fmt.Fprintln(out, dividerStyle.Render("  "+podiomgit.InstallHint()))
		fmt.Fprintln(out, dividerStyle.Render("  Projects can still work without it — they just cannot use source control."))
		return nil
	}
	fmt.Fprintln(out, noticeStyle.Render("git "+gitVersionLabel(status)))

	if status.UserName != "" && status.UserEmail != "" {
		fmt.Fprintln(out, dividerStyle.Render(fmt.Sprintf("  commits will be attributed to %s <%s>", status.UserName, status.UserEmail)))
	} else {
		set, err := u.confirm("Set the commit identity your agents will use?", true)
		if err != nil {
			return err
		}
		if set {
			name, err := u.input("Name", "Shown as the author of commits your agents make.", status.UserName)
			if err != nil {
				return err
			}
			email, err := u.input("Email", "Use the address your git host knows you by.", status.UserEmail)
			if err != nil {
				return err
			}
			if strings.TrimSpace(name) != "" && strings.TrimSpace(email) != "" {
				runner, err := podiomgit.New(podiomexec.Discovery{})
				if err != nil {
					return nil
				}
				if err := runner.ConfigSet(ctx, "user.name", strings.TrimSpace(name)); err != nil {
					fmt.Fprintln(out, warnStyle.Render(fmt.Sprintf("could not save user.name: %v", err)))
					return nil
				}
				if err := runner.ConfigSet(ctx, "user.email", strings.TrimSpace(email)); err != nil {
					fmt.Fprintln(out, warnStyle.Render(fmt.Sprintf("could not save user.email: %v", err)))
					return nil
				}
				fmt.Fprintln(out, noticeStyle.Render("commit identity saved"))
			}
		}
	}

	if key := podiomgit.PublicKey(); key != "" {
		fmt.Fprintln(out, dividerStyle.Render("  an SSH key is present"))
	} else {
		fmt.Fprintln(out, dividerStyle.Render("  no SSH key found — create one with ssh-keygen and add it to your git host,"))
		fmt.Fprintln(out, dividerStyle.Render("  or configure a credential helper. Podiom uses whatever you set up."))
	}
	fmt.Fprintln(out, dividerStyle.Render("  You can revisit this any time in Settings → Git."))
	return nil
}

func gitVersionLabel(status podiomgit.Status) string {
	if v := strings.TrimSpace(status.Version); v != "" {
		return strings.TrimPrefix(v, "git ")
	}
	return "found"
}
