package notify

import (
	"reflect"
	"testing"
)

func TestCategoryTitle(t *testing.T) {
	tests := []struct {
		category Category
		want     string
	}{
		{CategoryAgent, "Agent interaction"},
		{CategoryGoals, "Goals"},
		{CategorySchedules, "Schedules"},
		{CategoryTasks, "Tasks"},
		{CategorySystem, "System"},
		{Category("custom"), "custom"},
	}
	for _, tt := range tests {
		if got := tt.category.Title(); got != tt.want {
			t.Errorf("Category(%q).Title() = %q, want %q", tt.category, got, tt.want)
		}
	}
}

func TestAllChannels(t *testing.T) {
	want := []string{ChannelWebPush, ChannelNativePush}
	if got := AllChannels(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllChannels() = %v, want %v", got, want)
	}
}
