package notify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryRegistryTypeHasAProducer checks each registered notification type is
// actually published somewhere.
//
// A type nothing produces is a preference row the user can toggle that will never
// do anything — and nothing else in the codebase would reveal it, because the
// registry entry looks complete on its own.
func TestEveryRegistryTypeHasAProducer(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	// Constants named anywhere in non-test Go source. Producers reference a type
	// through its exported constant, never as a literal, so this is what a wired
	// producer looks like from the outside.
	notifyPackageDir := filepath.Join(root, "internal", "notify")
	named := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "bin", "web", "docs", "ha", "ios", "android":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// This package declares the types, renders their text and dispatches them;
		// none of that is producing one. A producer is by definition a caller from
		// outside, so the whole package is excluded — otherwise render.go's switch,
		// which names every type, would satisfy the check on its own.
		if filepath.Dir(path) == notifyPackageDir {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range regexp.MustCompile(`\bType[A-Z][A-Za-z]+\b`).FindAllString(string(body), -1) {
			named[m] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Goal timeline types are published by mapping an event kind rather than by a
	// direct call, so their constant never appears at a producer site.
	viaGoalEvents := map[string]bool{}
	for _, notifType := range goalEventTypes {
		viaGoalEvents[notifType] = true
	}

	for _, info := range All() {
		if viaGoalEvents[info.Type] {
			continue
		}
		want := constantNameFor(info.Type)
		if !named[want] {
			t.Errorf("%q is never published (registry says it comes from %s; expected the constant %s "+
				"at a producer call site). Wire the producer, or remove the entry so the user is not "+
				"offered a preference that does nothing.", info.Type, info.Producer, want)
		}
	}
}

// constantNameFor derives a type's exported constant name from its value:
// "goal.run_started" -> "TypeGoalRunStarted".
func constantNameFor(notifType string) string {
	var b strings.Builder
	b.WriteString("Type")
	for _, part := range strings.FieldsFunc(notifType, func(r rune) bool { return r == '.' || r == '_' }) {
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// TestConstantNameDerivationMatchesTheRegistry guards the assumption the producer
// test rests on: that every type's constant follows the naming convention. A type
// whose constant is named differently would silently pass the producer check.
func TestConstantNameDerivationMatchesTheRegistry(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal", "notify", "registry.go"))
	if err != nil {
		t.Fatal(err)
	}
	declared := string(body)
	for _, info := range All() {
		want := constantNameFor(info.Type)
		if !strings.Contains(declared, want+" ") && !strings.Contains(declared, want+"\t") {
			t.Errorf("%q: expected a constant named %s in registry.go; the producer coverage test "+
				"relies on that naming convention", info.Type, want)
		}
	}
}
