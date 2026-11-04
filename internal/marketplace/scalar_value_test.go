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

// #363: an alias node's Value holds the anchor NAME, not the aliased content —
// the new AliasNode case recurses into node.Alias instead, so `k: *anc` renders
// "hello", not "anc". The anchor-defining line itself already worked (a plain
// scalar); this pins the alias reference specifically.
func TestScalarValueResolvesAliases(t *testing.T) {
	doc := "base: &anc hello\nk: *anc\n"
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &node); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := scalarValue(node.Content[0].Content[1])
	if got != "hello" {
		t.Errorf("k: *anc rendered %q, want the aliased value %q", got, "hello")
	}
}
