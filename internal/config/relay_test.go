package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRelayEnrollmentMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.json")
	got, err := LoadRelayEnrollment(path)
	if err != nil {
		t.Fatalf("missing file: got error %v, want nil", err)
	}
	if got != (RelayEnrollment{}) {
		t.Fatalf("missing file: got %+v, want zero value", got)
	}
}

func TestLoadRelayEnrollmentEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.json")
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRelayEnrollment(path)
	if err != nil {
		t.Fatalf("empty file: got error %v, want nil", err)
	}
	if got != (RelayEnrollment{}) {
		t.Fatalf("empty file: got %+v, want zero value", got)
	}
}

func TestLoadRelayEnrollmentMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRelayEnrollment(path)
	if err == nil {
		t.Fatal("malformed JSON: got nil error, want non-nil")
	}
}

func TestLoadRelayEnrollmentIncompleteFields(t *testing.T) {
	for _, raw := range []string{
		`{"instance_id": "abc"}`,
		`{"credential": "xyz"}`,
		`{"instance_id": "", "credential": "xyz"}`,
		`{"instance_id": "abc", "credential": ""}`,
	} {
		path := filepath.Join(t.TempDir(), "relay.json")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadRelayEnrollment(path)
		if err == nil {
			t.Errorf("incomplete JSON %q: got nil error, want non-nil", raw)
		}
	}
}

func TestLoadRelayEnrollmentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relay.json")
	want := RelayEnrollment{
		InstanceID: "inst-abc123",
		Credential: "cred-xyz789",
	}
	if err := SaveRelayEnrollment(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadRelayEnrollment(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip: got %+v, want %+v", got, want)
	}
}

func TestSaveRelayEnrollmentRejectsMissingFields(t *testing.T) {
	dir := t.TempDir()
	for _, e := range []RelayEnrollment{
		{InstanceID: "abc"},
		{Credential: "xyz"},
		{},
	} {
		path := filepath.Join(dir, "relay.json")
		err := SaveRelayEnrollment(path, e)
		if err == nil {
			t.Errorf("SaveRelayEnrollment(%+v): got nil error, want non-nil", e)
		}
	}
}

func TestSaveRelayEnrollmentFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.json")
	e := RelayEnrollment{InstanceID: "inst-1", Credential: "cred-1"}
	if err := SaveRelayEnrollment(path, e); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file permissions = %o, want 0600", mode)
	}
}
