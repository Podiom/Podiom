package main

import (
	"encoding/json"
	"testing"
)

func TestStampInstallerCreatesMapForNilBody(t *testing.T) {
	got := stampInstaller(nil, "", "")
	if got == nil {
		t.Fatal("stampInstaller(nil, ...) returned nil")
	}
	if len(got) != 0 {
		t.Fatalf("stampInstaller(nil, empty values) = %v, want empty map", got)
	}
}

func TestStampInstallerIgnoresBlankValues(t *testing.T) {
	body := map[string]json.RawMessage{
		"existing": json.RawMessage(`"keep"`),
	}
	got := stampInstaller(body, " \t ", "\n")

	if _, ok := got["session_id"]; ok {
		t.Fatalf("blank sessionID was stamped: %s", got["session_id"])
	}
	if _, ok := got["installed_by"]; ok {
		t.Fatalf("blank agentName was stamped: %s", got["installed_by"])
	}
	if string(got["existing"]) != `"keep"` {
		t.Fatalf("unrelated field changed: %s", got["existing"])
	}
}

func TestStampInstallerAddsProvenanceAndPreservesOtherFields(t *testing.T) {
	body := map[string]json.RawMessage{
		"existing": json.RawMessage(`{"enabled":true}`),
	}
	got := stampInstaller(body, "session-123", "Atlas")

	for key, want := range map[string]string{
		"session_id":  "session-123",
		"installed_by": "Atlas",
	} {
		var value string
		if err := json.Unmarshal(got[key], &value); err != nil {
			t.Fatalf("unmarshal %s %s: %v", key, got[key], err)
		}
		if value != want {
			t.Errorf("%s = %q, want %q", key, value, want)
		}
	}
	if string(got["existing"]) != `{"enabled":true}` {
		t.Fatalf("unrelated field changed: %s", got["existing"])
	}
}
