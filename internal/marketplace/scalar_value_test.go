package marketplace

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// #362: scalarValue renders skill frontmatter values for the marketplace UI. It
// is a small pure recursive function over a *yaml.Node, and several behaviours
// are surprising enough to pin. Every expected output was checked against the
// real function. The YAML-alias fall-through (an anchor name rendered instead
// of the aliased value) is a latent bug tracked separately — deliberately NOT
// asserted here.
func renderScalar(t *testing.T, yamlValue string) string {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("k: "+yamlValue+"\n"), &node); err != nil {
		t.Fatalf("unmarshal %s: %v", yamlValue, err)
	}
	return scalarValue(node.Content[0].Content[1])
}

func TestScalarValue(t *testing.T) {
	t.Run("a sequence renders joined, indistinguishable from the literal", func(t *testing.T) {
		seq := renderScalar(t, "[a, b, c]")
		literal := renderScalar(t, `"a, b, c"`)
		if seq != "a, b, c" {
			t.Errorf("sequence: got %q", seq)
		}
		if seq != literal {
			t.Errorf("sequence %q and literal %q must be byte-identical downstream", seq, literal)
		}
	})

	t.Run("recursion flattens structure", func(t *testing.T) {
		twoMaps := renderScalar(t, "[{a: 1}, {b: 2}]")
		oneMap := renderScalar(t, "{a: 1, b: 2}")
		if twoMaps != "a: 1, b: 2" {
			t.Errorf("two single-key maps: got %q", twoMaps)
		}
		if oneMap != "a: 1, b: 2" {
			t.Errorf("one two-key map: got %q", oneMap)
		}
	})

	t.Run("nested keys are raw while values are trimmed", func(t *testing.T) {
		out := renderScalar(t, "{ \" pad \": \"  v  \" }")
		if out != " pad : v" {
			t.Errorf("got %q, want %q", out, " pad : v")
		}
	})

	t.Run("a block scalar is the only input with an embedded newline", func(t *testing.T) {
		out := renderScalar(t, "|\n  line one\n  line two")
		if out != "line one\nline two" {
			t.Errorf("got %q", out)
		}
		if !strings.Contains(out, "\n") {
			t.Errorf("the block scalar must keep its inner newline")
		}
	})

	t.Run("empty cases collapse together", func(t *testing.T) {
		for name, yamlValue := range map[string]string{
			"empty sequence": "[]",
			"empty string":   `""`,
			"empty block":    "|\n",
		} {
			if out := renderScalar(t, yamlValue); out != "" {
				t.Errorf("%s: got %q, want empty", name, out)
			}
		}
		// A null k: renders "null" today (the node's raw Value), NOT empty — the
		// issue's "a null k:" produces "" claim does not hold through yaml.v3's
		// Node API, where the scalar keeps its literal text. Pin what is real.
		if out := renderScalar(t, "null"); out != "null" {
			t.Errorf("null: got %q, want the literal %q", out, "null")
		}
	})
}

// renderDocScalar parses a full document (anchors and aliases live across keys,
// so the single-key helper above cannot express them) and renders one key.
func renderDocScalar(t *testing.T, doc string, key string) string {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &node); err != nil {
		t.Fatalf("unmarshal %q: %v", doc, err)
	}
	m := node.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return scalarValue(m.Content[i+1])
		}
	}
	t.Fatalf("key %q not found in %q", key, doc)
	return ""
}

// #363: an alias used to fall through to the raw node value, and for an
// yaml.AliasNode that is the anchor name, so `k: *anc` rendered as "anc".
func TestScalarValueAlias(t *testing.T) {
	t.Run("an alias renders the aliased value, not the anchor name", func(t *testing.T) {
		if out := renderDocScalar(t, "base: &anc hello\nk: *anc\n", "k"); out != "hello" {
			t.Fatalf("got %q, want %q", out, "hello")
		}
	})

	t.Run("an alias to a sequence renders the aliased sequence", func(t *testing.T) {
		if out := renderDocScalar(t, "base: &anc [a, b]\nk: *anc\n", "k"); out != "a, b" {
			t.Fatalf("got %q, want %q", out, "a, b")
		}
	})

	t.Run("an alias to a mapping renders the joined mapping", func(t *testing.T) {
		if out := renderDocScalar(t, "base: &anc {a: 1}\nk: *anc\n", "k"); out != "a: 1" {
			t.Fatalf("got %q, want %q", out, "a: 1")
		}
	})

	t.Run("a self-referencing anchor terminates", func(t *testing.T) {
		// `a: &x {k: *x}` is valid YAML; without the path cut the recursion
		// would not terminate once aliases resolve into their own anchor.
		if out := renderDocScalar(t, "a: &x {k: *x}\n", "a"); out != "k: " {
			t.Fatalf("got %q, want the cycle cut at %q", out, "k: ")
		}
	})
}
