package notify

import (
	"reflect"
	"testing"

	"github.com/Podiom/Podiom/internal/store"
)

func TestQuestionActions(t *testing.T) {
	info := Info{Actions: []ActionID{ActionOpen}}
	nav := []Action{{ID: ActionOpen, Label: "Open"}}
	option := func(label string) store.AgentQuestionOption { return store.AgentQuestionOption{Label: label} }
	plain := func(options ...store.AgentQuestionOption) store.AgentQuestionItem {
		return store.AgentQuestionItem{Question: "Choose", Options: options}
	}

	tests := []struct {
		name  string
		items []store.AgentQuestionItem
		want  []Action
	}{
		{name: "zero items", want: nav},
		{name: "multiple items", items: []store.AgentQuestionItem{plain(option("A")), plain(option("B"))}, want: nav},
		{name: "secret", items: []store.AgentQuestionItem{{IsSecret: true, Options: []store.AgentQuestionOption{option("A")}}}, want: nav},
		{name: "multi select", items: []store.AgentQuestionItem{{MultiSelect: true, Options: []store.AgentQuestionOption{option("A")}}}, want: nav},
		{name: "other", items: []store.AgentQuestionItem{{IsOther: true, Options: []store.AgentQuestionOption{option("A")}}}, want: nav},
		{name: "no options", items: []store.AgentQuestionItem{plain()}, want: nav},
		{name: "too many options", items: []store.AgentQuestionItem{plain(option("A"), option("B"), option("C"), option("D"))}, want: nav},
		{name: "plain options", items: []store.AgentQuestionItem{plain(option("Alpha"), option("Beta"), option("Gamma"))}, want: []Action{{ID: ActionOpen, Label: "Open"}, {ID: "answer:0", Label: "Alpha"}, {ID: "answer:1", Label: "Beta"}, {ID: "answer:2", Label: "Gamma"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := questionActions(info, tt.items); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("questionActions() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
