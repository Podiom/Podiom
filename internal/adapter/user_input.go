package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func (q *UserInputQuestion) UnmarshalJSON(data []byte) error {
	type question UserInputQuestion
	var raw struct {
		question
		MultiSelectCamel *bool `json:"multiSelect"`
		MultiSelectSnake *bool `json:"multi_select"`
		IsOtherCamel     *bool `json:"isOther"`
		IsOtherSnake     *bool `json:"is_other"`
		IsSecretCamel    *bool `json:"isSecret"`
		IsSecretSnake    *bool `json:"is_secret"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*q = UserInputQuestion(raw.question)
	if raw.MultiSelectCamel != nil {
		q.MultiSelect = *raw.MultiSelectCamel
	}
	if raw.MultiSelectSnake != nil {
		q.MultiSelect = *raw.MultiSelectSnake
	}
	if raw.IsOtherCamel != nil {
		q.IsOther = *raw.IsOtherCamel
	}
	if raw.IsOtherSnake != nil {
		q.IsOther = *raw.IsOtherSnake
	}
	if raw.IsSecretCamel != nil {
		q.IsSecret = *raw.IsSecretCamel
	}
	if raw.IsSecretSnake != nil {
		q.IsSecret = *raw.IsSecretSnake
	}
	return nil
}

// NormalizeUserInputQuestions trims provider payloads and assigns stable ids
// when a provider omits them. Internal protocol helpers use the same function
// as adapters so answer keys stay consistent across the relay round-trip.
func NormalizeUserInputQuestions(questions []UserInputQuestion) {
	for i := range questions {
		if strings.TrimSpace(questions[i].ID) == "" {
			questions[i].ID = fmt.Sprintf("q%d", i+1)
		}
		questions[i].Header = strings.TrimSpace(questions[i].Header)
		questions[i].Question = strings.TrimSpace(questions[i].Question)
		for j := range questions[i].Options {
			questions[i].Options[j].Label = strings.TrimSpace(questions[i].Options[j].Label)
			questions[i].Options[j].Description = strings.TrimSpace(questions[i].Options[j].Description)
		}
	}
}

func userInputID(prefix string, payload []byte) string {
	sum := sha256.Sum256(payload)
	return prefix + "-" + hex.EncodeToString(sum[:8])
}
