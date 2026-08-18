package notify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// relayChannelIDs are the Android notification channels the push relay names, one per
// Podiom importance. The relay derives the channel from importance and puts the id in the
// FCM payload, so the Android app has to create exactly these.
//
// A mismatch is silent: Android files the notification under its default channel and the
// user's per-importance settings quietly stop applying. That is why this is asserted
// rather than left to the two repositories agreeing by memory.
var relayChannelIDs = map[string]string{
	"passive":   "podiom_passive",
	"normal":    "podiom_default",
	"important": "podiom_important",
	"critical":  "podiom_critical",
}

// TestAndroidCreatesEveryChannelTheRelayNames reads the Android source and checks the
// channel ids line up with what the relay sends.
func TestAndroidCreatesEveryChannelTheRelayNames(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "android", "app", "src", "main",
		"java", "com", "podiom", "app", "MainActivity.java"))
	if err != nil {
		t.Fatalf("read MainActivity: %v", err)
	}
	source := string(body)

	for importance, channel := range relayChannelIDs {
		if !strings.Contains(source, `"`+channel+`"`) {
			t.Errorf("the Android app does not create the channel %q, which the relay sends for "+
				"importance %q; notifications would be filed under Android's default channel and "+
				"the user's per-importance settings would stop applying", channel, importance)
		}
	}

	// The reverse direction: a channel the app creates but the relay never names is dead
	// weight the user still sees in their system settings.
	declared := regexp.MustCompile(`"(podiom_[a-z_]+)"`).FindAllStringSubmatch(source, -1)
	named := map[string]bool{}
	for _, channel := range relayChannelIDs {
		named[channel] = true
	}
	for _, match := range declared {
		if !named[match[1]] {
			t.Errorf("the Android app creates the channel %q, which the relay never sends", match[1])
		}
	}
}

// TestEveryImportanceHasAChannel checks each importance the registry can produce maps to
// a channel the relay knows, so a new importance cannot ship without one.
func TestEveryImportanceHasAChannel(t *testing.T) {
	for _, info := range All() {
		if _, ok := relayChannelIDs[string(info.Importance)]; !ok {
			t.Errorf("%q has importance %q, which the relay has no channel for",
				info.Type, info.Importance)
		}
	}
}

// TestActionSetsAreTheAgreedVocabulary pins the action sets Podiom sends.
//
// The relay does not validate this value — whatever arrives becomes the APNs category
// verbatim — so an unrecognised one reaches iOS and simply produces no buttons, with
// nothing anywhere reporting a problem. The set is fixed here and the iOS app registers
// against it.
func TestActionSetsAreTheAgreedVocabulary(t *testing.T) {
	agreed := map[string]bool{
		"session_permission": true,
		"access_request":     true,
		"goal_action_item":   true,
		"goal_completion":    true,
		"question":           true,
	}

	for _, actionSet := range ActionSets() {
		if !agreed[actionSet] {
			t.Errorf("ActionSets() offers %q, which is not in the agreed vocabulary", actionSet)
		}
	}
	if len(ActionSets()) != len(agreed) {
		t.Errorf("ActionSets() has %d entries, want %d", len(ActionSets()), len(agreed))
	}

	// Every registry entry's action set is either empty or one of the agreed names.
	for _, info := range All() {
		if info.ActionSet == "" {
			continue
		}
		if !agreed[info.ActionSet] {
			t.Errorf("%q names the action set %q, which the apps do not register",
				info.Type, info.ActionSet)
		}
	}
}

// TestActionableTypesNameAnActionSet checks a type that offers buttons says which native
// group they belong to. Without it iOS shows no buttons at all, whatever actions the
// payload carries.
func TestActionableTypesNameAnActionSet(t *testing.T) {
	for _, info := range All() {
		if !info.Actionable() {
			continue
		}
		if info.ActionSet == "" {
			t.Errorf("%q offers actions but names no action set, so iOS would render no buttons",
				info.Type)
		}
	}
}
