package server

import (
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/core"
)

func testInterviewQuestion(topic core.InterviewTopic) interviewQuestionRequest {
	return interviewQuestionRequest{
		Topic:    topic,
		Header:   "Preference",
		Question: "Which option fits best?",
		Options: []adapter.UserInputOption{
			{Label: "One", Description: "First concrete choice."},
			{Label: "Two", Description: "Second concrete choice."},
			{Label: "Three", Description: "Third concrete choice."},
		},
	}
}

func validProfileDraft() core.UserProfileDraft {
	return core.UserProfileDraft{
		IdentityContext:   []core.UserProfileFact{{Label: "Name", Value: "Marcus"}},
		Communication:     []core.UserProfileFact{{Label: "Tone", Value: "Direct"}},
		OutputPreferences: []core.UserProfileFact{{Label: "Structure", Value: "Lead with outcomes"}},
		TechnicalContext:  []core.UserProfileFact{{Label: "Depth", Value: "Expert"}},
		WorkingTogether:   []core.UserProfileFact{{Label: "Feedback", Value: "Challenge assumptions"}},
	}
}

func TestInterviewCoordinatorEnforcesCoverageAndQuestionLimit(t *testing.T) {
	coordinator := newInterviewCoordinator()
	coordinator.start("session-1")

	first := testInterviewQuestion(core.InterviewTopicIdentityContext)
	if _, err := coordinator.beginQuestion("session-1", first); err != nil {
		t.Fatalf("begin first question: %v", err)
	}
	if _, err := coordinator.finishQuestion("session-1", first.Topic, true); err != nil {
		t.Fatalf("finish first question: %v", err)
	}
	if _, err := coordinator.beginQuestion("session-1", first); err == nil {
		t.Fatal("expected duplicate topic among first five to fail")
	}

	for _, topic := range core.RequiredInterviewTopics[1:] {
		question := testInterviewQuestion(topic)
		if _, err := coordinator.beginQuestion("session-1", question); err != nil {
			t.Fatalf("begin %s: %v", topic, err)
		}
		if _, err := coordinator.finishQuestion("session-1", topic, true); err != nil {
			t.Fatalf("finish %s: %v", topic, err)
		}
	}
	state, err := coordinator.submit("session-1", validProfileDraft())
	if err != nil {
		t.Fatalf("submit covered interview: %v", err)
	}
	if state.Status != "draft" || !strings.Contains(state.Draft, "**Name:** Marcus") {
		t.Fatalf("unexpected submitted state: %+v", state)
	}

	coordinator.start("session-2")
	for _, topic := range core.RequiredInterviewTopics {
		question := testInterviewQuestion(topic)
		_, _ = coordinator.beginQuestion("session-2", question)
		_, _ = coordinator.finishQuestion("session-2", topic, true)
	}
	for i := 0; i < 3; i++ {
		question := testInterviewQuestion(core.InterviewTopicCommunication)
		if _, err := coordinator.beginQuestion("session-2", question); err != nil {
			t.Fatalf("begin follow-up %d: %v", i+1, err)
		}
		_, _ = coordinator.finishQuestion("session-2", question.Topic, true)
	}
	if _, err := coordinator.beginQuestion("session-2", testInterviewQuestion(core.InterviewTopicCommunication)); err == nil {
		t.Fatal("expected ninth question to fail")
	}
}

func TestInterviewCoordinatorRejectsMalformedAndPrematureSubmission(t *testing.T) {
	coordinator := newInterviewCoordinator()
	coordinator.start("session-1")
	malformed := testInterviewQuestion(core.InterviewTopicIdentityContext)
	malformed.Options = malformed.Options[:2]
	if _, err := coordinator.beginQuestion("session-1", malformed); err == nil {
		t.Fatal("expected question with two options to fail")
	}
	if _, err := coordinator.submit("session-1", validProfileDraft()); err == nil || !strings.Contains(err.Error(), "missing required topics") {
		t.Fatalf("expected premature submit failure, got %v", err)
	}
}

func TestInterviewCoordinatorAllowsOneRecovery(t *testing.T) {
	coordinator := newInterviewCoordinator()
	coordinator.start("session-1")
	state, prompt, retry := coordinator.recover("session-1")
	if !retry || state.Status != "recovering" || !strings.Contains(prompt, "identity_context") {
		t.Fatalf("unexpected first recovery: state=%+v retry=%v prompt=%q", state, retry, prompt)
	}
	state, _, retry = coordinator.recover("session-1")
	if retry || state.Status != "failed" || state.Error == "" {
		t.Fatalf("unexpected second recovery: state=%+v retry=%v", state, retry)
	}
}
