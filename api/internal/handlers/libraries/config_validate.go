// Story 9.1 AC-1 — library `settings` schema validation, enforced
// API-side.
//
// The PATCH handler used to blind-`DeepMergeJSON` arbitrary settings:
// a malformed `stt.backend="invalid"` returned 200. The pipeline
// already ships the canonical validator
// (`pipeline/.../library_mgmt/config.py:validate`) but the Go API
// never called it (and could not — it's Python). This file is a
// faithful Go port of that validator so the API can reject malformed
// settings with a 422 carrying the offending JSON path, and surface
// unknown keys as warnings (the forward-compat clause: unknown keys
// round-trip but are flagged).
//
// The two implementations MUST stay in lockstep — the recognised-key
// sets, nested-key sets, ISO-639-1 rule, stt-backend vocabulary, and
// numeric ranges below mirror config.py line-for-line. Any change to
// one is a change to both.
package libraries

import (
	"regexp"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// recognisedKeys mirrors config.py RECOGNISED_KEYS.
var recognisedKeys = map[string]struct{}{
	"language":                {},
	"multi_audio":             {},
	"stt":                     {},
	"embedding":               {},
	"diarize":                 {},
	"chapter_inference":       {},
	"auto_tag_topics":         {},
	"default_subtitle_lang":   {},
	"ignore_globs":            {},
	"sweep_interval_sec":      {},
	"speaker_match_threshold": {},
	"topic_clusters":          {},
}

// nestedKeys mirrors config.py _NESTED_KEYS.
var nestedKeys = map[string]map[string]struct{}{
	"stt": {
		"backend": {}, "model": {}, "profile": {},
		"initial_prompt": {}, "max_usd_per_month": {},
	},
	"embedding": {"model": {}, "device": {}},
}

// allowedSTTBackends mirrors config.py ALLOWED_STT_BACKENDS.
var allowedSTTBackends = map[string]struct{}{
	"whisper-mlx": {}, "faster-whisper": {}, "openai-api": {},
}

// allowedEmbeddingDevices mirrors config.py _validate_embedding_field.
var allowedEmbeddingDevices = map[string]struct{}{
	"auto": {}, "cpu": {}, "cuda": {}, "mps": {},
}

var iso6391Re = regexp.MustCompile(`^[a-z]{2}$`)

// ValidateLibrarySettings is the Go twin of config.py `validate`. It
// takes the decoded settings object and returns field-level 422 errors
// (type/vocabulary/shape violations) plus warnings for unknown keys
// (which still round-trip — forward-compat). A nil/empty map is valid.
func ValidateLibrarySettings(settings map[string]any) (errs []httperror.FieldError, warnings []string) {
	for key, value := range settings {
		if _, ok := recognisedKeys[key]; !ok {
			warnings = append(warnings, "unknown key "+key)
			continue
		}
		validateSettingsKey(key, value, &errs, &warnings)
	}
	return errs, warnings
}

func addErr(errs *[]httperror.FieldError, path, msg string) {
	*errs = append(*errs, httperror.FieldError{Field: path, Message: msg})
}

// isJSONInt reports whether v is a JSON number with no fractional part.
// encoding/json decodes every number into float64, so an "integer"
// constraint is "float64 with zero fraction". JSON has no bool/int
// subtype confusion (unlike Python) so there is no bool guard needed
// here — a JSON bool decodes to Go bool, never float64.
func isJSONInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	if f != float64(int64(f)) {
		return 0, false
	}
	return int(int64(f)), true
}

func validateSettingsKey(key string, value any, errs *[]httperror.FieldError, warnings *[]string) {
	switch key {
	case "language":
		if value == "auto" {
			return
		}
		s, ok := value.(string)
		if !ok || !iso6391Re.MatchString(s) {
			addErr(errs, "settings/language", "must be 'auto' or an ISO-639-1 code")
		}
	case "default_subtitle_lang":
		s, ok := value.(string)
		if !ok || !iso6391Re.MatchString(s) {
			addErr(errs, "settings/"+key, "must be an ISO-639-1 code")
		}
	case "multi_audio", "diarize", "chapter_inference", "auto_tag_topics":
		if _, ok := value.(bool); !ok {
			addErr(errs, "settings/"+key, "must be boolean")
		}
	case "sweep_interval_sec":
		n, ok := isJSONInt(value)
		if !ok || n < 0 {
			addErr(errs, "settings/"+key, "must be a non-negative integer")
		}
	case "ignore_globs":
		arr, ok := value.([]any)
		if !ok {
			addErr(errs, "settings/"+key, "must be a list of strings")
			return
		}
		for _, v := range arr {
			if _, ok := v.(string); !ok {
				addErr(errs, "settings/"+key, "must be a list of strings")
				return
			}
		}
	case "speaker_match_threshold":
		f, ok := value.(float64)
		if !ok {
			addErr(errs, "settings/"+key, "must be a number")
			return
		}
		if f < 0.0 || f > 1.0 {
			addErr(errs, "settings/"+key, "must be in [0, 1]")
		}
	case "topic_clusters":
		if value == nil {
			return
		}
		n, ok := isJSONInt(value)
		if !ok || n < 1 {
			addErr(errs, "settings/"+key, "must be a positive integer or null")
		}
	case "stt":
		validateNested("stt", value, errs, warnings, validateSTTField)
	case "embedding":
		validateNested("embedding", value, errs, warnings, validateEmbeddingField)
	}
}

func validateNested(
	key string,
	value any,
	errs *[]httperror.FieldError,
	warnings *[]string,
	fieldValidator func(name string, v any, errs *[]httperror.FieldError),
) {
	obj, ok := value.(map[string]any)
	if !ok {
		addErr(errs, "settings/"+key, "must be an object")
		return
	}
	allowed := nestedKeys[key]
	for nk, nv := range obj {
		if _, ok := allowed[nk]; !ok {
			*warnings = append(*warnings, "unknown key "+key+"/"+nk)
			continue
		}
		fieldValidator(nk, nv, errs)
	}
}

func validateSTTField(name string, value any, errs *[]httperror.FieldError) {
	switch name {
	case "backend":
		s, ok := value.(string)
		if !ok {
			addErr(errs, "settings/stt/backend", "must be a string")
			return
		}
		if _, ok := allowedSTTBackends[s]; !ok {
			addErr(errs, "settings/stt/backend", "unknown backend "+s+"; allowed: faster-whisper, openai-api, whisper-mlx")
		}
	case "model", "profile", "initial_prompt":
		if _, ok := value.(string); !ok {
			addErr(errs, "settings/stt/"+name, "must be a string")
		}
	case "max_usd_per_month":
		f, ok := value.(float64)
		if !ok || f < 0 {
			addErr(errs, "settings/stt/"+name, "must be a non-negative number")
		}
	}
}

func validateEmbeddingField(name string, value any, errs *[]httperror.FieldError) {
	switch name {
	case "model":
		if _, ok := value.(string); !ok {
			addErr(errs, "settings/embedding/model", "must be a string")
		}
	case "device":
		s, ok := value.(string)
		if !ok {
			addErr(errs, "settings/embedding/device", "must be one of {auto, cpu, cuda, mps}")
			return
		}
		if _, ok := allowedEmbeddingDevices[s]; !ok {
			addErr(errs, "settings/embedding/device", "must be one of {auto, cpu, cuda, mps}")
		}
	}
}
