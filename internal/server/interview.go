package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
	"github.com/google/uuid"
)

const maxInterviewQuestions = 8

type InterviewState struct {
	SessionID     string                `json:"session_id"`
	Status        string                `json:"status"`
	Answered      int                   `json:"answered"`
	CoveredTopics []core.InterviewTopic `json:"covered_topics"`
	Draft         string                `json:"draft,omitempty"`
	Error         string                `json:"error,omitempty"`
}

type interviewQuestionRequest struct {
	Topic       core.InterviewTopic       `json:"topic"`
	Header      string                    `json:"header"`
	Question    string                    `json:"question"`
	Options     []adapter.UserInputOption `json:"options"`
	MultiSelect bool                      `json:"multi_select,omitempty"`
}

type interviewRecord struct {
	state   InterviewState
	covered map[core.InterviewTopic]bool
	pending bool
	retries int
}

func (c *interviewCoordinator) recover(sessionID string) (InterviewState, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record := c.records[sessionID]
	if record == nil {
		return InterviewState{SessionID: sessionID, Status: "failed", Error: "Interview state expired. Start a new interview."}, "", false
	}
	if record.state.Draft != "" {
		return cloneInterviewState(record.state), "", false
	}
	if record.retries >= 1 {
		record.state.Status = "failed"
		record.state.Error = "The interviewer stopped twice without using the required interview tools. Retry with another model or interviewer."
		return cloneInterviewState(record.state), "", false
	}
	record.retries++
	record.state.Status = "recovering"
	missing := missingInterviewTopics(record.covered)
	prompt := "Continue the USER.md interview now. Use only podiom_ask_profile_question and podiom_submit_user_profile."
	if len(missing) > 0 {
		prompt += " Required topics still missing: " + strings.Join(missing, ", ") + "."
	} else {
		prompt += " All required topics are covered; submit the structured draft now."
	}
	return cloneInterviewState(record.state), prompt, true
}

type interviewCoordinator struct {
	mu      sync.Mutex
	records map[string]*interviewRecord
}

func newInterviewCoordinator() *interviewCoordinator {
	return &interviewCoordinator{records: map[string]*interviewRecord{}}
}

func (c *interviewCoordinator) start(sessionID string) InterviewState {
	c.mu.Lock()
	defer c.mu.Unlock()
	record := &interviewRecord{
		state:   InterviewState{SessionID: sessionID, Status: "interviewing"},
		covered: map[core.InterviewTopic]bool{},
	}
	c.records[sessionID] = record
	return record.state
}

func (c *interviewCoordinator) get(sessionID string) (InterviewState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record := c.records[sessionID]
	if record == nil {
		return InterviewState{}, false
	}
	return cloneInterviewState(record.state), true
}

func (c *interviewCoordinator) remove(sessionID string) {
	c.mu.Lock()
	delete(c.records, sessionID)
	c.mu.Unlock()
}

func (c *interviewCoordinator) beginQuestion(sessionID string, question interviewQuestionRequest) (InterviewState, error) {
	if err := validateInterviewQuestion(question); err != nil {
		return InterviewState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record := c.records[sessionID]
	if record == nil {
		return InterviewState{}, errors.New("interview state not found")
	}
	if record.state.Draft != "" {
		return InterviewState{}, errors.New("interview draft was already submitted")
	}
	if record.pending {
		return InterviewState{}, errors.New("an interview question is already pending")
	}
	if record.state.Answered >= maxInterviewQuestions {
		return InterviewState{}, fmt.Errorf("interview already reached the %d-question limit", maxInterviewQuestions)
	}
	if record.state.Answered < len(core.RequiredInterviewTopics) && record.covered[question.Topic] {
		return InterviewState{}, fmt.Errorf("the first five questions must cover distinct topics; %q is already covered", question.Topic)
	}
	record.pending = true
	record.state.Status = "awaiting_answer"
	return cloneInterviewState(record.state), nil
}

func (c *interviewCoordinator) finishQuestion(sessionID string, topic core.InterviewTopic, answered bool) (InterviewState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record := c.records[sessionID]
	if record == nil {
		return InterviewState{}, errors.New("interview state not found")
	}
	record.pending = false
	if answered {
		record.state.Answered++
		record.covered[topic] = true
		record.state.CoveredTopics = orderedCoveredTopics(record.covered)
	}
	record.state.Status = "interviewing"
	return cloneInterviewState(record.state), nil
}

func (c *interviewCoordinator) submit(sessionID string, draft core.UserProfileDraft) (InterviewState, error) {
	c.mu.Lock()
	record := c.records[sessionID]
	if record == nil {
		c.mu.Unlock()
		return InterviewState{}, errors.New("interview state not found")
	}
	if record.pending {
		c.mu.Unlock()
		return InterviewState{}, errors.New("answer the pending question before submitting")
	}
	missing := missingInterviewTopics(record.covered)
	if len(missing) > 0 {
		c.mu.Unlock()
		return InterviewState{}, fmt.Errorf("interview is missing required topics: %s", strings.Join(missing, ", "))
	}
	c.mu.Unlock()

	markdown, err := core.RenderUserProfileDraft(draft)
	if err != nil {
		return InterviewState{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	record = c.records[sessionID]
	if record == nil {
		return InterviewState{}, errors.New("interview state not found")
	}
	record.state.Status = "draft"
	record.state.Draft = markdown
	record.state.Error = ""
	return cloneInterviewState(record.state), nil
}

func validateInterviewQuestion(question interviewQuestionRequest) error {
	if !core.ValidInterviewTopic(question.Topic) {
		return fmt.Errorf("unknown interview topic %q", question.Topic)
	}
	question.Header = strings.TrimSpace(question.Header)
	question.Question = strings.TrimSpace(question.Question)
	if question.Header == "" || len(question.Header) > 40 {
		return errors.New("question header must contain 1 to 40 characters")
	}
	if question.Question == "" || len(question.Question) > 320 {
		return errors.New("question must contain 1 to 320 characters")
	}
	if len(question.Options) < 3 || len(question.Options) > 5 {
		return errors.New("question must contain 3 to 5 options")
	}
	seen := map[string]bool{}
	for _, option := range question.Options {
		label := strings.TrimSpace(option.Label)
		description := strings.TrimSpace(option.Description)
		if label == "" || description == "" {
			return errors.New("every option needs a label and description")
		}
		if len(label) > 80 || len(description) > 240 {
			return errors.New("option labels must be at most 80 characters and descriptions at most 240 characters")
		}
		key := strings.ToLower(label)
		if seen[key] {
			return fmt.Errorf("duplicate option label %q", label)
		}
		seen[key] = true
	}
	return nil
}

func orderedCoveredTopics(covered map[core.InterviewTopic]bool) []core.InterviewTopic {
	out := make([]core.InterviewTopic, 0, len(covered))
	for _, topic := range core.RequiredInterviewTopics {
		if covered[topic] {
			out = append(out, topic)
		}
	}
	return out
}

func missingInterviewTopics(covered map[core.InterviewTopic]bool) []string {
	var out []string
	for _, topic := range core.RequiredInterviewTopics {
		if !covered[topic] {
			out = append(out, string(topic))
		}
	}
	return out
}

func cloneInterviewState(state InterviewState) InterviewState {
	state.CoveredTopics = append([]core.InterviewTopic(nil), state.CoveredTopics...)
	return state
}

func (s *Server) handleInterview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/interviews/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.Error(w, "interview session and action are required", http.StatusBadRequest)
		return
	}
	sessionID, action := parts[0], parts[1]
	sess, err := s.core.GetSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if sess.Origin != store.OriginInterview {
		http.Error(w, "session is not a USER.md interview", http.StatusForbidden)
		return
	}
	switch action {
	case "questions":
		s.handleInterviewQuestion(w, r, sess)
	case "draft":
		s.handleInterviewDraft(w, r, sess)
	default:
		http.Error(w, "unknown interview action", http.StatusNotFound)
	}
}

func (s *Server) handleInterviewQuestion(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var question interviewQuestionRequest
	if err := json.NewDecoder(r.Body).Decode(&question); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	question.Header = strings.TrimSpace(question.Header)
	question.Question = strings.TrimSpace(question.Question)
	for i := range question.Options {
		question.Options[i].Label = strings.TrimSpace(question.Options[i].Label)
		question.Options[i].Description = strings.TrimSpace(question.Options[i].Description)
	}
	state, err := s.interviews.beginQuestion(sess.ID, question)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.turns.recordInterviewState(sess.ID, &state)
	turnID, ok := s.turns.turnIDForSession(sess.ID)
	if !ok {
		_, _ = s.interviews.finishQuestion(sess.ID, question.Topic, false)
		http.Error(w, "interview turn is not active", http.StatusConflict)
		return
	}
	req := adapter.UserInputRequest{
		ID:       "interview-" + uuid.NewString(),
		TurnID:   turnID,
		Provider: sess.Provider,
		Questions: []adapter.UserInputQuestion{{
			ID:          "answer",
			Header:      strings.TrimSpace(question.Header),
			Question:    strings.TrimSpace(question.Question),
			Options:     question.Options,
			MultiSelect: question.MultiSelect,
			IsOther:     true,
		}},
	}
	decision, err := s.input.RequestUserInput(r.Context(), req, defaultHTTPPermissionTimeout)
	answered := err == nil && len(decision.Answers["answer"]) > 0
	state, finishErr := s.interviews.finishQuestion(sess.ID, question.Topic, answered)
	if finishErr == nil {
		s.turns.recordInterviewState(sess.ID, &state)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, errUserInputTimeout) {
			http.Error(w, err.Error(), http.StatusRequestTimeout)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if !answered {
		http.Error(w, "the question was not answered", http.StatusBadRequest)
		return
	}
	writeJSON(w, decision, nil)
}

func (s *Server) handleInterviewDraft(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var draft core.UserProfileDraft
	if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state, err := s.interviews.submit(sess.ID, draft)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.turns.recordInterviewState(sess.ID, &state)
	writeJSON(w, state, nil)
}
