package moderation

import (
	"encoding/json"
	"time"

	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// InstructionsType is the control post's typ for a home's takedown and restore instructions. It
// is signed with the broadcast signing key (the key of the revocation list) under its own typ, so
// neither the edge nor a home reads one as the other.
const InstructionsType = "takedowns"

// InstructionsTTL is how long a signed batch is current: the home asks every minute.
const InstructionsTTL = time.Hour

// Instruction is one takedown or restore a home applies (§2): the item (by its ref, or the
// programme airing at At on the channel) or the source, with the notice it answers.
type Instruction struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // takedown or restore
	NoticeID string `json:"notice_id,omitempty"`
	// Restores is the takedown a restore undoes.
	Restores string `json:"restores,omitempty"`
	Scope    string `json:"scope"` // item or source
	// Channel is the channel's cloud id (the home's publish answer).
	Channel    string     `json:"channel,omitempty"`
	At         *time.Time `json:"at,omitempty"`
	ItemRef    string     `json:"item_ref,omitempty"`
	SourceKind string     `json:"source_kind,omitempty"`
	SourceRef  string     `json:"source_ref,omitempty"`
}

// SignInstructions signs a home's pending instructions as of now.
func SignInstructions(k bc.Key, serverID string, list []Instruction, now time.Time) (string, error) {
	if list == nil {
		list = []Instruction{}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return "", err
	}
	return bc.SignControl(k, InstructionsType, map[string]any{"schema": 1, "home": serverID, "instructions": items}, now, InstructionsTTL)
}

// Ack is a home's answer to an instruction.
type Ack struct {
	// Result is applied, not_found (the item is gone: the takedown applies to nothing) or failed.
	Result string `json:"result"`
	Detail string `json:"detail"`
	// ItemRef and Programme are what the home resolved from the time (a takedown by time).
	ItemRef   string `json:"item_ref"`
	Programme string `json:"programme"`
	// AiringUntil is the end of the item's slot when it is on air now (nil: not airing), as the
	// home says: shown to staff, never acted on (the slate keeps its own end).
	AiringUntil *time.Time `json:"airing_until"`
}

// ValidAck checks an ack's result.
func ValidAck(a Ack) bool {
	return a.Result == "applied" || a.Result == "not_found" || a.Result == "failed"
}
