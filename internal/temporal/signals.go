package temporal

import (
	"encoding/json"

	"temporal-llm-orchestrator/internal/domain"
)

const ReviewDecisionSignalName = "reviewDecision"

type ReviewDecisionSignal struct {
	Decision    domain.ReviewDecisionType `json:"decision"`
	Corrections json.RawMessage           `json:"corrections,omitempty"`
	Reviewer    string                    `json:"reviewer,omitempty"`
	Reason      string                    `json:"reason,omitempty"`
}
