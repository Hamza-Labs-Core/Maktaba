package channels

import (
	"encoding/json"
	"errors"
	"strings"
)

// ErrUnknownMode is returned by ValidateMode for a mode outside the
// closed vocabulary (defense-in-depth ahead of the DB CHECK).
var ErrUnknownMode = errors.New("unknown channel mode")

// validModes is the closed mode vocabulary (mirrors the slot-0081 CHECK
// and the Python scheduler's planner registry, Plan 27.2).
var validModes = map[string]struct{}{
	ModeShuffle:  {},
	ModeMarathon: {},
	ModeSchedule: {},
	ModeSmartMix: {},
}

// validTransitions is the closed transition vocabulary (slot-0081 CHECK).
var validTransitions = map[string]struct{}{
	TransitionCut:       {},
	TransitionCrossfade: {},
}

// validMarathonOrder is the accepted marathon episode ordering.
var validMarathonOrder = map[string]struct{}{
	"":         {}, // unset → planner default (aired)
	"aired":    {},
	"dvd":      {},
	"release":  {},
	"filename": {},
}

// ValidateMode reports whether mode is in the closed vocabulary.
func ValidateMode(mode string) error {
	if _, ok := validModes[mode]; !ok {
		return ErrUnknownMode
	}
	return nil
}

// ValidateTransition reports whether transition is in the closed
// vocabulary. Empty is allowed (the DB default fills 'cut').
func ValidateTransition(t string) error {
	if t == "" {
		return nil
	}
	if _, ok := validTransitions[t]; !ok {
		return errors.New("transition must be 'cut' or 'crossfade'")
	}
	return nil
}

// ValidateModeConfig checks the mode_config JSONB against the per-mode
// schema (D1). The four modes carry disjoint config; the contract here
// is the documented source of truth mirrored by the Python scheduler
// (Plan 27.2 §0 D1). An empty/nil config is valid for every mode — the
// planner applies its documented defaults.
func ValidateModeConfig(mode string, cfg json.RawMessage) error {
	if err := ValidateMode(mode); err != nil {
		return err
	}
	m, err := decodeObject(cfg)
	if err != nil {
		return err
	}
	if m == nil {
		return nil // empty config → planner defaults
	}
	switch mode {
	case ModeShuffle:
		return validateShuffle(m)
	case ModeMarathon:
		return validateMarathon(m)
	case ModeSchedule:
		return validateSchedule(m)
	case ModeSmartMix:
		return validateSmartMix(m)
	}
	return ErrUnknownMode
}

// decodeObject parses cfg into a generic object. Nil/empty → (nil, nil).
// A non-object JSON value (array, scalar) is an error — mode_config is
// always an object.
func decodeObject(cfg json.RawMessage) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(cfg))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		return nil, errors.New("mode_config must be a JSON object")
	}
	return m, nil
}

// validateShuffle: optional `reshuffle_period` (string token) and an
// optional inline `filter` (smart_query shape — structural only here;
// the scheduler resolves it).
func validateShuffle(m map[string]any) error {
	if v, ok := m["reshuffle_period"]; ok {
		if _, isStr := v.(string); !isStr {
			return errors.New("shuffle.reshuffle_period must be a string")
		}
	}
	if v, ok := m["filter"]; ok {
		if _, isObj := v.(map[string]any); !isObj {
			return errors.New("shuffle.filter must be an object")
		}
	}
	return nil
}

// validateMarathon: requires a `series_id` (string) OR a `source`
// (object); validates `order` against the vocabulary; `loop` must be a
// bool when present.
func validateMarathon(m map[string]any) error {
	_, hasSeries := m["series_id"]
	_, hasSource := m["source"]
	if !hasSeries && !hasSource {
		return errors.New("marathon requires series_id or source")
	}
	if hasSeries {
		if _, ok := m["series_id"].(string); !ok {
			return errors.New("marathon.series_id must be a string")
		}
	}
	if order, ok := m["order"]; ok {
		s, isStr := order.(string)
		if !isStr {
			return errors.New("marathon.order must be a string")
		}
		if _, valid := validMarathonOrder[s]; !valid {
			return errors.New("marathon.order must be one of aired, dvd, release, filename")
		}
	}
	if loop, ok := m["loop"]; ok {
		if _, isBool := loop.(bool); !isBool {
			return errors.New("marathon.loop must be a boolean")
		}
	}
	return nil
}

// validateSchedule: requires a non-empty `slots` array; each slot must be
// an object carrying at least a `start` and `end` time-of-day string.
func validateSchedule(m map[string]any) error {
	raw, ok := m["slots"]
	if !ok {
		return errors.New("schedule requires a non-empty slots array")
	}
	slots, isArr := raw.([]any)
	if !isArr || len(slots) == 0 {
		return errors.New("schedule.slots must be a non-empty array")
	}
	for i, s := range slots {
		obj, isObj := s.(map[string]any)
		if !isObj {
			return errors.New("schedule.slots entries must be objects")
		}
		if _, ok := obj["start"].(string); !ok {
			return errFieldf("schedule.slots[%d].start must be a time string", i)
		}
		if _, ok := obj["end"].(string); !ok {
			return errFieldf("schedule.slots[%d].end must be a time string", i)
		}
	}
	return nil
}

// validateSmartMix: optional `daypart_profile` (string), optional
// `diversity` (number in [0,1]), optional `weights` (object).
func validateSmartMix(m map[string]any) error {
	if v, ok := m["daypart_profile"]; ok {
		if _, isStr := v.(string); !isStr {
			return errors.New("smart_mix.daypart_profile must be a string")
		}
	}
	if v, ok := m["weights"]; ok {
		if _, isObj := v.(map[string]any); !isObj {
			return errors.New("smart_mix.weights must be an object")
		}
	}
	if v, ok := m["diversity"]; ok {
		f, isNum := v.(float64)
		if !isNum {
			return errors.New("smart_mix.diversity must be a number")
		}
		if f < 0 || f > 1 {
			return errors.New("smart_mix.diversity must be in [0,1]")
		}
	}
	return nil
}

func errFieldf(format string, i int) error {
	return errors.New(strings.Replace(format, "%d", itoa(i), 1))
}
