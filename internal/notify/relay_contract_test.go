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

// actionSetsWithoutACategory are action sets Podiom sends but deliberately does not give
// iOS buttons, with the reason.
//
// Recorded here rather than in a comment so the omission is provable and cannot be
// forgotten: registering the category is what makes this test pass again.
var actionSetsWithoutACategory = map[string]string{
	ActionSetQuestion: "its buttons are the question's own answer options, whose text is only " +
		"known when the question is asked; a category's action titles are fixed at registration, " +
		"so generic labels would be worse than opening Podiom to read the question",
}

// TestIOSRegistersEveryActionSet keeps the iOS categories and Podiom's registry in step.
//
// The APNs category is the only thing that makes buttons appear on iOS, and the relay does
// not know or check what the app registered — so a category Podiom sends but iOS does not
// know produces a notification with no buttons and no error anywhere. This is the only
// place the two halves are compared.
func TestIOSRegistersEveryActionSet(t *testing.T) {
	source := readAppDelegate(t)

	for _, actionSet := range ActionSets() {
		if reason, skipped := actionSetsWithoutACategory[actionSet]; skipped {
			if reason == "" {
				t.Errorf("actionSetsWithoutACategory[%q] needs a reason", actionSet)
			}
			if strings.Contains(source, `identifier: "`+actionSet+`"`) {
				t.Errorf("%q is registered on iOS but listed as deliberately unregistered", actionSet)
			}
			continue
		}
		if !strings.Contains(source, `identifier: "`+actionSet+`"`) {
			t.Errorf("iOS registers no UNNotificationCategory for the action set %q, so those "+
				"notifications arrive with no buttons; register it in AppDelegate.swift or record "+
				"the omission in actionSetsWithoutACategory", actionSet)
		}
	}
}

// TestIOSRegistersEveryActionOfEachSet checks the buttons inside each category cover the
// actions Podiom can offer for it.
//
// A missing action identifier is the subtler half of the same silent failure: the category
// matches, some buttons appear, and one operation is simply unavailable on the phone.
func TestIOSRegistersEveryActionOfEachSet(t *testing.T) {
	source := readAppDelegate(t)

	for _, info := range All() {
		if info.ActionSet == "" {
			continue
		}
		if _, skipped := actionSetsWithoutACategory[info.ActionSet]; skipped {
			continue
		}
		for _, action := range info.Actions {
			// `open` is navigation, and tapping the notification body already does it, so a
			// category need not spend a button on it.
			if action == ActionOpen {
				continue
			}
			if !strings.Contains(source, `identifier: "`+string(action)+`"`) {
				t.Errorf("%q offers the action %q but iOS registers no button for it in the %q "+
					"category, so it cannot be performed from the notification",
					info.Type, action, info.ActionSet)
			}
		}
	}
}

// TestIOSRegistersNoUnknownAction checks the reverse: a button iOS draws that Podiom would
// reject is worse than a missing one, because the user presses it and nothing happens.
func TestIOSRegistersNoUnknownAction(t *testing.T) {
	source := readAppDelegate(t)
	known := map[string]bool{
		string(ActionOpen): true, string(ActionAllow): true, string(ActionDeny): true,
		string(ActionApprove): true, string(ActionDone): true, string(ActionBlocked): true,
		string(ActionReview): true, string(ActionMarkDone): true,
	}
	categories := map[string]bool{}
	for _, actionSet := range ActionSets() {
		categories[actionSet] = true
	}

	for _, match := range regexp.MustCompile(`identifier: "([a-z_]+)"`).FindAllStringSubmatch(source, -1) {
		id := match[1]
		if categories[id] || known[id] {
			continue
		}
		t.Errorf("AppDelegate.swift registers %q, which is neither an action set Podiom sends "+
			"nor an action it accepts; pressing that button would do nothing", id)
	}
}

func readAppDelegate(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "ios", "App", "App", "AppDelegate.swift"))
	if err != nil {
		t.Fatalf("read AppDelegate: %v", err)
	}
	return string(body)
}
