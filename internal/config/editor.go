package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RemoveAgent removes an agent entry from config.yaml when it exists. It edits
// the YAML syntax tree so user comments and unrelated settings survive.
func RemoveAgent(path, name string) error {
	return editFile(path, func(root *yaml.Node) (bool, error) {
		return removeAgent(root, name), nil
	})
}

// UpsertProfile creates or replaces one profile entry in config.yaml. It edits
// the YAML syntax tree so user comments and unrelated settings survive.
func UpsertProfile(path string, profile Profile) error {
	return editFile(path, func(root *yaml.Node) (bool, error) {
		return upsertProfile(root, profile), nil
	})
}

// RemoveProfile removes a profile entry from config.yaml when it exists.
func RemoveProfile(path, name string) error {
	return editFile(path, func(root *yaml.Node) (bool, error) {
		return removeProfile(root, name), nil
	})
}

// SetGlobal upserts the daemon-wide defaults under the top-level `global:`
// mapping in config.yaml. It edits the YAML syntax tree so user comments and
// unrelated settings survive. Only the fields Podiom's Settings page owns are
// touched: provider, profile, model, effort, permission_mode,
// permission_timeout, and fallback. Empty model/effort keys are removed so the
// file falls back to applyDefaults; an empty fallback drops the key entirely.
func SetGlobal(path string, g Global) error {
	return editFile(path, func(root *yaml.Node) (bool, error) {
		return setGlobal(root, g), nil
	})
}

func setGlobal(root *yaml.Node, g Global) bool {
	doc := root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return false
	}
	global := mappingChild(doc, "global")
	if global == nil {
		global = &yaml.Node{Kind: yaml.MappingNode}
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "global"},
			global,
		)
	}

	changed := false
	changed = setScalarChild(global, "provider", string(g.Provider)) || changed
	changed = setScalarChild(global, "profile", g.Profile) || changed
	changed = setScalarChild(global, "model", g.Model) || changed
	changed = setScalarChild(global, "effort", g.Effort) || changed
	changed = setScalarChild(global, "permission_mode", string(g.PermissionMode)) || changed
	changed = setScalarChild(global, "permission_timeout", g.PermissionTimeout) || changed
	changed = setSequenceChild(global, "fallback", g.Fallback) || changed
	// setScalarChild drops empty values, so "off" leaves no key behind and the
	// zero value from applyDefaults stands.
	collapse := ""
	if g.CollapseReasoning {
		collapse = "true"
	}
	changed = setScalarChild(global, "collapse_reasoning", collapse) || changed
	return changed
}

// SetVoice upserts the voice-input settings under the top-level `voice:`
// mapping in config.yaml, editing the YAML syntax tree so user comments and
// unrelated settings survive. An empty key removes it; a voice mapping left
// empty is dropped entirely so a cleared config carries no stray block.
func SetVoice(path string, v Voice) error {
	return editFile(path, func(root *yaml.Node) (bool, error) {
		return setVoice(root, v), nil
	})
}

func setVoice(root *yaml.Node, v Voice) bool {
	doc := root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return false
	}
	voice := mappingChild(doc, "voice")
	if voice == nil {
		if v.OpenAIAPIKey == "" {
			return false
		}
		voice = &yaml.Node{Kind: yaml.MappingNode}
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "voice"},
			voice,
		)
	}

	changed := setScalarChild(voice, "openai_api_key", v.OpenAIAPIKey)
	if len(voice.Content) == 0 {
		changed = removeMappingChild(doc, "voice") || changed
	}
	return changed
}

// removeMappingChild deletes key (and its value) from a mapping node.
func removeMappingChild(mapping *yaml.Node, key string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}

// mappingChild returns the value node for key in a mapping node, or nil.
func mappingChild(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setScalarChild upserts key->value in a mapping. An empty value removes the
// key (so config falls back to applyDefaults). Reports whether it mutated.
func setScalarChild(mapping *yaml.Node, key, value string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		if value == "" {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
		if mapping.Content[i+1].Value == value && mapping.Content[i+1].Kind == yaml.ScalarNode {
			return false
		}
		mapping.Content[i+1].Kind = yaml.ScalarNode
		mapping.Content[i+1].Tag = ""
		mapping.Content[i+1].Style = 0
		mapping.Content[i+1].Value = value
		return true
	}
	if value == "" {
		return false
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
	return true
}

// setSequenceChild upserts key->[]values in a mapping. An empty slice removes
// the key. Reports whether it mutated.
func setSequenceChild(mapping *yaml.Node, key string, values []string) bool {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		if len(values) == 0 {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
		if sequenceEqual(mapping.Content[i+1], values) {
			return false
		}
		mapping.Content[i+1] = seq
		return true
	}
	if len(values) == 0 {
		return false
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		seq,
	)
	return true
}

func sequenceEqual(node *yaml.Node, values []string) bool {
	if node.Kind != yaml.SequenceNode || len(node.Content) != len(values) {
		return false
	}
	for i, v := range values {
		if node.Content[i].Value != v {
			return false
		}
	}
	return true
}

func editFile(path string, edit func(root *yaml.Node) (bool, error)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config for edit: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat config for edit: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parse config for edit %s: %w", path, err)
	}
	changed, err := edit(&root)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		_ = enc.Close()
		return fmt.Errorf("encode config edit: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close config encoder: %w", err)
	}
	return writeFileAtomic(path, out.Bytes(), info.Mode().Perm())
}

func removeAgent(root *yaml.Node, name string) bool {
	doc := root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i]
		if key.Value != "agents" {
			continue
		}
		agents := doc.Content[i+1]
		if agents.Kind != yaml.SequenceNode {
			return false
		}
		changed := false
		kept := agents.Content[:0]
		for _, entry := range agents.Content {
			if agentNodeName(entry) == name {
				changed = true
				continue
			}
			kept = append(kept, entry)
		}
		if changed {
			agents.Content = kept
		}
		return changed
	}
	return false
}

func upsertProfile(root *yaml.Node, profile Profile) bool {
	doc := root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return false
	}
	profiles := mappingChild(doc, "profiles")
	if profiles == nil {
		profiles = &yaml.Node{Kind: yaml.SequenceNode}
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "profiles"},
			profiles,
		)
	}
	if profiles.Kind != yaml.SequenceNode {
		return false
	}
	next := profileNode(profile)
	for i, entry := range profiles.Content {
		if profileNodeName(entry) != profile.Name {
			continue
		}
		if profileNodeEqual(entry, profile) {
			return false
		}
		profiles.Content[i] = next
		return true
	}
	profiles.Content = append(profiles.Content, next)
	return true
}

func removeProfile(root *yaml.Node, name string) bool {
	doc := root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return false
	}
	profiles := mappingChild(doc, "profiles")
	if profiles == nil || profiles.Kind != yaml.SequenceNode {
		return false
	}
	changed := false
	kept := profiles.Content[:0]
	for _, entry := range profiles.Content {
		if profileNodeName(entry) == name {
			changed = true
			continue
		}
		kept = append(kept, entry)
	}
	if changed {
		profiles.Content = kept
	}
	return changed
}

func profileNode(profile Profile) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: profile.Name},
		&yaml.Node{Kind: yaml.ScalarNode, Value: "provider"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: string(profile.Provider)},
	)
	if info, ok := ProviderInfoFor(profile.Provider); ok {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: info.ProfileDirKey},
			&yaml.Node{Kind: yaml.ScalarNode, Value: profile.Dir()},
		)
	}
	return node
}

func profileNodeName(node *yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "name" {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func profileNodeEqual(node *yaml.Node, profile Profile) bool {
	if node.Kind != yaml.MappingNode {
		return false
	}
	values := map[string]string{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		values[node.Content[i].Value] = node.Content[i+1].Value
	}
	if values["name"] != profile.Name || values["provider"] != string(profile.Provider) {
		return false
	}
	info, ok := ProviderInfoFor(profile.Provider)
	if !ok {
		return false
	}
	otherKey := "home_dir"
	if info.ProfileDirKey == "home_dir" {
		otherKey = "config_dir"
	}
	return values[info.ProfileDirKey] == profile.Dir() && values[otherKey] == ""
}

func agentNodeName(node *yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "name" {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
