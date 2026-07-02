package onboard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// ErrAborted is returned up the call stack when the user cancels a prompt with
// Ctrl-C or Esc. The caller can treat it as a clean, zero-exit cancellation.
var ErrAborted = errors.New("onboarding cancelled by user")

// ErrNoTTY is returned when onboarding has no interactive terminal to drive the
// wizard (e.g. piped input with no controlling terminal, as in CI).
var ErrNoTTY = errors.New("onboarding requires an interactive terminal")

// customSentinel is the synthetic select option that reveals a free-text input.
const customSentinel = "✎ type my own…"

// ui wraps huh forms, wiring them to the attached terminal and preserving the
// raw-mode robustness the wizard relies on under `curl | bash`.
type ui struct {
	tty   *os.File
	out   io.Writer
	sane  *term.State
	theme *huh.Theme
}

func newUI(tty *os.File, out io.Writer, sane *term.State) *ui {
	return &ui{tty: tty, out: out, sane: sane, theme: podiomTheme()}
}

// interactive reports whether there's a usable terminal to render forms into.
func (u *ui) interactive() bool {
	return u.tty != nil && term.IsTerminal(int(u.tty.Fd()))
}

// restoreTTY re-applies the saved canonical terminal state before launching a
// form, so a provider CLI that left the terminal in raw mode can't poison
// bubbletea's restore baseline. No-op when not attached to a terminal.
func (u *ui) restoreTTY() {
	if u.tty != nil && u.sane != nil {
		_ = term.Restore(int(u.tty.Fd()), u.sane)
	}
}

// run executes a form wired to the attached terminal and normalizes a user
// abort (Ctrl-C/Esc) into ErrAborted.
func (u *ui) run(form *huh.Form) error {
	u.restoreTTY()
	form = form.WithTheme(u.theme)
	if u.tty != nil {
		// Reading and rendering through /dev/tty is what lets the wizard work
		// under `curl | bash`, where stdin is the installer's script pipe.
		form = form.WithInput(u.tty).WithOutput(u.tty)
	}
	err := form.Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

// selectOne shows an arrow-key list and returns the chosen option.
func (u *ui) selectOne(title string, options []string, def string) (string, error) {
	v := def
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title(title).Options(huh.NewOptions(options...)...).Value(&v),
	))
	if err := u.run(form); err != nil {
		return "", err
	}
	return v, nil
}

// selectOrCustom shows an arrow-key list with a "type my own" escape hatch. When
// the sentinel is chosen, a follow-up input is revealed in the same form run.
func (u *ui) selectOrCustom(title string, options []string, def string) (string, error) {
	choice := def
	custom := ""
	opts := append(append([]string{}, options...), customSentinel)
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title(title).Options(huh.NewOptions(opts...)...).Value(&choice),
		),
		huh.NewGroup(
			huh.NewInput().Title("Type your own").Value(&custom),
		).WithHideFunc(func() bool { return choice != customSentinel }),
	)
	if err := u.run(form); err != nil {
		return "", err
	}
	if choice == customSentinel {
		return custom, nil
	}
	return choice, nil
}

// input shows a single-line text field. An empty submission falls back to def,
// which is also shown as the placeholder.
func (u *ui) input(title, description, def string) (string, error) {
	v := ""
	field := huh.NewInput().Title(title).Value(&v)
	if description != "" {
		field = field.Description(description)
	}
	if def != "" {
		field = field.Placeholder(def)
	}
	if err := u.run(huh.NewForm(huh.NewGroup(field))); err != nil {
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		return def, nil
	}
	return v, nil
}

// confirm shows a Yes/No prompt.
func (u *ui) confirm(title string, def bool) (bool, error) {
	v := def
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Affirmative("Yes").Negative("No").Value(&v),
	))
	if err := u.run(form); err != nil {
		return false, err
	}
	return v, nil
}

// multiline shows a multi-line text editor seeded with def.
func (u *ui) multiline(title, def string) (string, error) {
	v := def
	form := huh.NewForm(huh.NewGroup(
		huh.NewText().Title(title).Lines(10).Value(&v),
	))
	if err := u.run(form); err != nil {
		return "", err
	}
	return v, nil
}

// spinnerWhile runs fn while animating a spinner with the given title. With no
// terminal it simply runs fn. The completed line is replaced by a check mark.
func (u *ui) spinnerWhile(title string, fn func() error) error {
	if !u.interactive() {
		return fn()
	}
	u.restoreTTY()
	done := make(chan error, 1)
	go func() { done <- fn() }()

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	ticker := time.NewTicker(90 * time.Millisecond)
	defer ticker.Stop()
	fmt.Fprint(u.out, "\x1b[?25l")       // hide cursor
	defer fmt.Fprint(u.out, "\x1b[?25h") // restore cursor
	i := 0
	for {
		select {
		case err := <-done:
			fmt.Fprint(u.out, "\r\x1b[K") // clear the spinner line
			if err == nil {
				fmt.Fprintln(u.out, noticeStyle.Render("✓ ")+title)
			}
			return err
		case <-ticker.C:
			fmt.Fprintf(u.out, "\r%s %s", noticeStyle.Render(frames[i%len(frames)]), title)
			i++
		}
	}
}
