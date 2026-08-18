package notify

import (
	"context"
	"fmt"

	"github.com/Podiom/Podiom/internal/store"
)

// PreferenceRow is one togglable notification type as the settings UI sees it.
type PreferenceRow struct {
	Type       string `json:"type"`
	Label      string `json:"label"`
	Importance string `json:"importance"`
	Enabled    bool   `json:"enabled"`
}

// PreferenceGroup is one category's worth of rows.
type PreferenceGroup struct {
	Category string          `json:"category"`
	Title    string          `json:"title"`
	Rows     []PreferenceRow `json:"rows"`
}

// PreferenceUpdate is one requested change.
type PreferenceUpdate struct {
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

// PreferenceGroups returns every notification type grouped for the settings UI,
// with the user's stored choice or the registry default.
//
// The whole model is served from here rather than assembled by the client, so the
// labels, grouping and defaults exist once — adding a notification type makes it
// appear in the web and mobile settings screens with no client change.
//
// A type is reported as enabled when any channel would deliver it. The UI presents
// one switch per type rather than a channel matrix, because "which of my devices"
// is a question about registrations, not about which events matter.
func (e *Engine) PreferenceGroups(ctx context.Context) ([]PreferenceGroup, error) {
	if e == nil {
		return nil, nil
	}
	prefs, err := e.preferences(ctx)
	if err != nil {
		return nil, err
	}
	byCategory := map[Category][]PreferenceRow{}
	for _, info := range All() {
		enabled := false
		for _, channel := range AllChannels() {
			if prefs.enabled(info, channel) {
				enabled = true
				break
			}
		}
		byCategory[info.Category] = append(byCategory[info.Category], PreferenceRow{
			Type:       info.Type,
			Label:      info.Label,
			Importance: string(info.Importance),
			Enabled:    enabled,
		})
	}
	groups := make([]PreferenceGroup, 0, len(byCategory))
	for _, category := range Categories() {
		rows := byCategory[category]
		if len(rows) == 0 {
			continue
		}
		groups = append(groups, PreferenceGroup{
			Category: string(category),
			Title:    category.Title(),
			Rows:     rows,
		})
	}
	return groups, nil
}

// SetPreferences records the user's choices.
//
// One update writes a row for every known channel, including channels this daemon
// is not currently running. That is what makes the single switch honest: a type
// switched off stays off when native push is added later, instead of quietly
// reverting to the registry default on a channel that had no row.
func (e *Engine) SetPreferences(ctx context.Context, updates []PreferenceUpdate) error {
	if e == nil {
		return nil
	}
	// Validated as a whole before anything is written, so a request naming one bad
	// type does not leave the rest half-applied.
	for _, update := range updates {
		if _, ok := Lookup(update.Type); !ok {
			return fmt.Errorf("unknown notification type %q", update.Type)
		}
	}
	for _, update := range updates {
		for _, channel := range AllChannels() {
			if err := e.store.SetNotificationPreference(ctx, store.NotificationPreference{
				Type:    update.Type,
				Channel: channel,
				Enabled: update.Enabled,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
