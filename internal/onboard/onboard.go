// Package onboard implements Podium's first-run CLI wizard.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/mar-schmidt/Podium/internal/autostart"
	"github.com/mar-schmidt/Podium/internal/client"
	"github.com/mar-schmidt/Podium/internal/config"
	"github.com/mar-schmidt/Podium/internal/providercheck"
)

// Options configures the interactive onboarding wizard.
type Options struct {
	Addr string
	In   io.Reader
	Out  io.Writer
	Err  io.Writer
}

type answers struct {
	Role          string
	Temperament   string
	Collaboration string
	Autonomy      string
	Strengths     string
	Boundaries    string
	Playfulness   string
	CaresAbout    string
	Extra         string
}

// Run launches the first-run wizard.
func Run(ctx context.Context, opts Options) error {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = os.Stderr
	}
	// When input isn't an interactive terminal — for example when the wizard is
	// launched from `curl | bash`, where stdin is the installer's script pipe —
	// attach to the controlling terminal so keystrokes register and Ctrl-C works.
	// Reassigning os.Stdin means nested flows (provider login, $EDITOR) inherit
	// the terminal too, not just our prompts.
	if tty := controllingTTY(in); tty != nil {
		defer tty.Close()
		in = tty
		os.Stdin = tty
	}
	// Snapshot the terminal's line-mode (canonical) state before any provider
	// CLI runs. Claude/Codex are TUIs that put the controlling terminal into
	// raw mode on startup and can exit without restoring it — which would leave
	// our later prompts unable to read a line (Enter sends CR, not the NL that
	// ReadString waits for) and disable Ctrl-C. We re-apply this saved state
	// before each prompt so input is always canonical, echoing, and interruptible.
	var ttyFile *os.File
	var saneState *term.State
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		ttyFile = f
		if st, err := term.GetState(int(f.Fd())); err == nil {
			saneState = st
		}
	}
	u := newUI(ttyFile, out, saneState)

	// huh needs a real terminal to drive the wizard. install.sh already gates the
	// launch on /dev/tty; if we still landed here without one (e.g. CI piping into
	// `podium onboard`), guide the user rather than invent answers or hang.
	if !u.interactive() {
		fmt.Fprintln(out, warnStyle.Render("Onboarding needs an interactive terminal. Run 'podium onboard' directly in a terminal."))
		return ErrNoTTY
	}

	clear := isTerminalWriter(out)
	banner(out, clear)

	section(out, "Checking your agent CLIs")
	checkProviders := func(title string) []providercheck.Status {
		var st []providercheck.Status
		_ = u.spinnerWhile(title, func() error {
			st = providercheck.CheckAll(ctx, providercheck.Options{})
			return nil
		})
		return st
	}
	statuses := checkProviders("Detecting Claude and Codex…")
	printDoctor(out, statuses)
	ready := readyProviders(statuses)
	for len(ready) == 0 {
		fmt.Fprintln(out, warnStyle.Render("Podium needs Claude or Codex before it can generate a SOUL.md."))
		for _, s := range statuses {
			if !s.Found {
				fmt.Fprintf(out, "\n%s not found.\n  %s\n", titleProvider(s.Provider), s.InstallHint)
				continue
			}
			ok, err := u.confirm(fmt.Sprintf("Open the native %s login now?", titleProvider(s.Provider)), true)
			if err != nil {
				return err
			}
			if ok {
				_ = providercheck.RunNativeLogin(ctx, s.Provider, s.Path)
			}
		}
		statuses = checkProviders("Re-checking providers…")
		printDoctor(out, statuses)
		ready = readyProviders(statuses)
		if len(ready) == 0 {
			again, err := u.confirm("Try provider checks again?", true)
			if err != nil {
				return err
			}
			if !again {
				return errors.New("no working provider available; run `podium doctor` after installing or logging in")
			}
		}
	}

	section(out, "Choosing a provider")
	provider, err := chooseProvider(u, ready)
	if err != nil {
		return err
	}
	if err := confirmProviderLogin(ctx, u, provider, statuses); err != nil {
		return err
	}
	fmt.Fprintln(out, noticeStyle.Render(fmt.Sprintf("Great. %s will help draft this agent's SOUL.md.", titleProvider(provider))))

	section(out, "Waking the stage")
	c, addr, err := ensureDaemon(ctx, opts.Addr, out, errOut)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, noticeStyle.Render(fmt.Sprintf("Podium daemon is live at %s.", addr)))

	section(out, "Shaping your agent")
	ans, err := collectAnswers(u)
	if err != nil {
		return err
	}

	section(out, "Naming")
	name, err := chooseName(u, ans)
	if err != nil {
		return err
	}
	agent, err := c.CreateAgent(ctx, client.AgentCreateRequest{
		Name:           name,
		Provider:       provider,
		Model:          "",
		Effort:         "medium",
		PermissionMode: config.PermissionApprove,
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	placeholder := deterministicSoul(name, ans)
	if _, err := c.UpdateAgent(ctx, name, client.AgentUpdateRequest{Soul: &placeholder}); err != nil {
		return fmt.Errorf("write placeholder SOUL.md: %w", err)
	}

	section(out, "Drafting SOUL.md")
	var soul string
	draft := func() error {
		s, e := generateSoul(ctx, c, name, ans)
		if e == nil {
			soul = s
		}
		return e
	}
	if err := u.spinnerWhile(fmt.Sprintf("Asking %s to draft %s's SOUL.md…", titleProvider(provider), agent.Name), draft); err != nil {
		fmt.Fprintln(out, warnStyle.Render(fmt.Sprintf("LLM generation did not complete: %v", err)))
		soul = deterministicSoul(name, ans)
	}
	for {
		fmt.Fprintln(out)
		fmt.Fprintln(out, soulBoxStyle.Width(soulWidth(out)).Render(soul))
		action, err := u.selectOne("What should we do with this soul?", []string{"accept", "regenerate", "edit"}, "accept")
		if err != nil {
			return err
		}
		switch action {
		case "accept":
			if _, err := c.UpdateAgent(ctx, name, client.AgentUpdateRequest{Soul: &soul}); err != nil {
				return fmt.Errorf("save SOUL.md: %w", err)
			}
			if err := offerAutostart(u); err != nil {
				return err
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, noticeStyle.Render(fmt.Sprintf("%s is ready. Try: podium chat --agent %s \"hello\"", name, name)))
			return nil
		case "regenerate":
			if err := u.spinnerWhile(fmt.Sprintf("Asking %s to redraft SOUL.md…", titleProvider(provider)), draft); err != nil {
				fmt.Fprintln(out, warnStyle.Render(fmt.Sprintf("Regeneration failed: %v", err)))
			}
		case "edit":
			u.restoreTTY()
			edited, err := editText(soul)
			if err != nil {
				fmt.Fprintln(out, warnStyle.Render(fmt.Sprintf("Editor unavailable: %v", err)))
				edited, err = u.multiline("Paste the SOUL.md you want to save.", soul)
				if err != nil {
					return err
				}
			}
			if strings.TrimSpace(edited) != "" {
				soul = CleanSoulMarkdown(edited)
			}
		}
	}
}

// offerAutostart asks, as the final wizard step, whether to launch Podium on
// login and configures it if so. Suppressed when the installer already handled
// autostart (PODIUM_OFFER_AUTOSTART=0). Failures warn but never fail onboarding.
func offerAutostart(u *ui) error {
	if os.Getenv("PODIUM_OFFER_AUTOSTART") == "0" {
		return nil
	}
	section(u.out, "Autostart")
	ok, err := u.confirm("Start Podium automatically when your computer starts?", true)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	podiumd, err := findPodiumd()
	if err != nil {
		fmt.Fprintln(u.out, warnStyle.Render("Could not locate podiumd for autostart: "+err.Error()))
		return nil
	}
	if err := autostart.Install(autostart.Options{PodiumdPath: podiumd, PodiumHome: os.Getenv(config.EnvHome)}); err != nil {
		fmt.Fprintln(u.out, warnStyle.Render("Could not configure autostart: "+err.Error()))
		return nil
	}
	fmt.Fprintln(u.out, noticeStyle.Render("Autostart enabled."))
	return nil
}

// isTerminalWriter reports whether w is a terminal we can safely clear.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// soulWidth picks a readable width for the SOUL.md preview box, clamped to the
// terminal size.
func soulWidth(w io.Writer) int {
	width := 76
	if f, ok := w.(*os.File); ok {
		if cw, _, err := term.GetSize(int(f.Fd())); err == nil && cw > 0 && cw-4 < width {
			width = cw - 4
		}
	}
	if width < 24 {
		width = 24
	}
	return width
}

func ensureDaemon(ctx context.Context, addr string, out, errOut io.Writer) (*client.Client, string, error) {
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	c := client.New(addr)
	if _, err := c.Health(ctx); err == nil {
		return c, addr, nil
	}
	fmt.Fprintln(out, "Starting podiumd for onboarding...")
	podiumd, err := findPodiumd()
	if err != nil {
		return nil, addr, err
	}
	cmd := exec.CommandContext(ctx, podiumd)
	cmd.Stdout = errOut
	cmd.Stderr = errOut
	if err := cmd.Start(); err != nil {
		return nil, addr, fmt.Errorf("start podiumd: %w", err)
	}
	for i := 0; i < 30; i++ {
		time.Sleep(300 * time.Millisecond)
		if _, err := c.Health(ctx); err == nil {
			return c, addr, nil
		}
	}
	return nil, addr, errors.New("podiumd did not become ready")
}

func findPodiumd() (string, error) {
	if p, err := exec.LookPath("podiumd"); err == nil {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := "podiumd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidate := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("podiumd not found on PATH or next to %s", exe)
}

func printDoctor(out io.Writer, statuses []providercheck.Status) {
	for _, s := range statuses {
		state := warnStyle.Render("missing")
		if s.Ready {
			state = noticeStyle.Render("ready")
		} else if s.Found {
			state = goldStyle.Render("found")
		}
		fmt.Fprintf(out, "  %s: %s", titleProvider(s.Provider), state)
		if s.Version != "" {
			fmt.Fprintf(out, " (%s)", s.Version)
		}
		if s.Path != "" {
			fmt.Fprintf(out, " at %s", s.Path)
		}
		fmt.Fprintln(out)
		if !s.Ready && s.Error != "" {
			fmt.Fprintf(out, "    %s\n", s.Error)
		}
	}
}

func readyProviders(statuses []providercheck.Status) []config.Provider {
	var out []config.Provider
	for _, s := range statuses {
		if s.Ready {
			out = append(out, s.Provider)
		}
	}
	return out
}

func chooseProvider(u *ui, providers []config.Provider) (config.Provider, error) {
	if len(providers) == 1 {
		return providers[0], nil
	}
	labels := make([]string, 0, len(providers))
	for _, provider := range providers {
		labels = append(labels, string(provider))
	}
	choice, err := u.selectOne("Which provider do you want to start using?", labels, labels[0])
	if err != nil {
		return "", err
	}
	return config.Provider(choice), nil
}

func confirmProviderLogin(ctx context.Context, u *ui, provider config.Provider, statuses []providercheck.Status) error {
	var status providercheck.Status
	for _, candidate := range statuses {
		if candidate.Provider == provider {
			status = candidate
			break
		}
	}
	if status.Path == "" {
		return fmt.Errorf("%s is not available", titleProvider(provider))
	}
	ok, err := u.confirm(fmt.Sprintf("Is %s logged in and ready to run?", titleProvider(provider)), true)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	fmt.Fprintf(u.out, "Opening %s's native login flow.\n", titleProvider(provider))
	if err := providercheck.RunNativeLogin(ctx, provider, status.Path); err != nil {
		return err
	}
	next := providercheck.Check(ctx, provider, providercheck.Options{})
	if !next.Ready {
		return fmt.Errorf("%s still does not look ready: %s", titleProvider(provider), next.Error)
	}
	return nil
}

func collectAnswers(u *ui) (answers, error) {
	const (
		strengthsDef  = "systems thinking, clear writing, careful implementation"
		boundariesDef = "avoid destructive changes, explain risky choices, keep user data private"
		caresDef      = "helping me finish meaningful work with less friction"
	)
	ans := answers{
		Role:          "builder",
		Temperament:   "warm and curious",
		Collaboration: "make reasonable calls",
		Autonomy:      "medium",
		Playfulness:   "moderate",
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("What should this agent gravitate toward?").
				Options(huh.NewOptions("builder", "researcher", "operator", "creative partner", "reviewer")...).Value(&ans.Role),
			huh.NewSelect[string]().Title("What temperament should it have?").
				Options(huh.NewOptions("calm and precise", "warm and curious", "bold and proactive", "playful and inventive")...).Value(&ans.Temperament),
			huh.NewSelect[string]().Title("How should it collaborate with you?").
				Options(huh.NewOptions("ask before big moves", "make reasonable calls", "challenge me thoughtfully", "keep momentum high")...).Value(&ans.Collaboration),
			huh.NewSelect[string]().Title("How much autonomy should it take?").
				Options(huh.NewOptions("low", "medium", "high")...).Value(&ans.Autonomy),
			huh.NewSelect[string]().Title("How much playfulness belongs in its voice?").
				Options(huh.NewOptions("subtle", "moderate", "sparkly")...).Value(&ans.Playfulness),
		).Title("Personality").Description("This agent should feel like someone you influenced."),
		huh.NewGroup(
			huh.NewInput().Title("What strengths should it lean into?").Placeholder(strengthsDef).Value(&ans.Strengths),
			huh.NewInput().Title("What boundaries should it respect?").Placeholder(boundariesDef).Value(&ans.Boundaries),
			huh.NewInput().Title("What should this agent care about most?").Placeholder(caresDef).Value(&ans.CaresAbout),
			huh.NewInput().Title("Anything else you want woven into the soul?").Value(&ans.Extra),
		).Title("Details"),
	)
	if err := u.run(form); err != nil {
		return ans, err
	}
	if strings.TrimSpace(ans.Strengths) == "" {
		ans.Strengths = strengthsDef
	}
	if strings.TrimSpace(ans.Boundaries) == "" {
		ans.Boundaries = boundariesDef
	}
	if strings.TrimSpace(ans.CaresAbout) == "" {
		ans.CaresAbout = caresDef
	}
	return ans, nil
}

func chooseName(u *ui, ans answers) (string, error) {
	suggestions := suggestNames(ans)
	choice, err := u.selectOrCustom("Choose a name, or type your own", suggestions, suggestions[0])
	if err != nil {
		return "", err
	}
	return sanitizeName(choice), nil
}

func suggestNames(ans answers) []string {
	switch ans.Role {
	case "researcher":
		return []string{"atlas", "mira", "sage", "lumen"}
	case "operator":
		return []string{"riley", "switch", "marin", "pilot"}
	case "creative partner":
		return []string{"sol", "nova", "fig", "muse"}
	case "reviewer":
		return []string{"quinn", "arden", "scope", "vera"}
	default:
		return []string{"juno", "forge", "ember", "rowan"}
	}
}

func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "juno"
	}
	return out
}

func generateSoul(ctx context.Context, c *client.Client, name string, ans answers) (string, error) {
	result, err := c.GenerateAgentSoul(ctx, name, client.AgentGenerateRequest{
		Role:          ans.Role,
		Temperament:   ans.Temperament,
		Collaboration: ans.Collaboration,
		Autonomy:      ans.Autonomy,
		Strengths:     ans.Strengths,
		Boundaries:    ans.Boundaries,
		Playfulness:   ans.Playfulness,
		CaresAbout:    ans.CaresAbout,
		Extra:         ans.Extra,
	})
	if err != nil {
		return "", err
	}
	return result.Soul, nil
}

// CleanSoulMarkdown removes common chat wrapping from generated markdown.
func CleanSoulMarkdown(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
		s = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(s, "# Identity") {
		s = "# Identity\n\n" + s
	}
	return strings.TrimSpace(s) + "\n"
}

func deterministicSoul(name string, ans answers) string {
	return CleanSoulMarkdown(fmt.Sprintf(`# Identity

Name: %s

You are %s, a %s colleague with a %s temperament. You exist to help the user move with clarity while preserving the choices that shaped you.

## Purpose

- Help the user make steady progress on meaningful work.
- Keep the user's priorities visible: %s.

## Worldview

- Good assistance is specific, situated, and willing to make reasonable calls.
- Momentum matters, but not more than preserving user trust.
- Identity should affect behavior, not decorate it.

## Working style

- Collaboration style: %s.
- Autonomy level: %s.
- Cares most about: %s.

## Voice

- Playfulness: %s.
- Extra notes: %s.

## Strengths

- %s.

## Boundaries

- %s.

## Calibration notes

- You sound like %s rather than a generic assistant.
- You follow the user's boundaries without becoming timid.
`, name, name, ans.Role, ans.Temperament, ans.CaresAbout, ans.Collaboration, ans.Autonomy, ans.CaresAbout, ans.Playfulness, firstNonEmpty(ans.Extra, "stay useful, kind, and direct"), ans.Strengths, ans.Boundaries, name))
}

func editText(initial string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return "", errors.New("EDITOR is not set")
	}
	tmp, err := os.CreateTemp("", "podium-soul-*.md")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(initial); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// controllingTTY returns the process's controlling terminal when in is not an
// interactive terminal (for example, when onboarding is launched from
// `curl | bash`, where stdin is the installer's script pipe). It returns nil
// when in is already a terminal or no controlling terminal is available (such
// as CI), so the caller keeps the original reader and prompts fall back to
// their defaults rather than hanging.
func controllingTTY(in io.Reader) *os.File {
	if f, ok := in.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return nil
		}
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	return tty
}

func titleProvider(provider config.Provider) string {
	switch provider {
	case config.ProviderClaude:
		return "Claude"
	case config.ProviderCodex:
		return "Codex"
	default:
		return string(provider)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
