package pluginmanifest

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	plugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

// The rules across fields (story 54.01 §2), run after the schema passed, in this order in
// every validator:
//
//	R1 reserved ids            R4 capabilities           R7 strings in both i18n files
//	R2 platforms               R5 stages, requests,      R8 the settings schema file
//	R3 a lower bound in           triggers               R9 schedule settings
//	   the maktaba range       R6 schedules on catalog
//	                              stages
//
// R7–R9 need the package's files and are skipped when files is nil.
func rules(m map[string]any, files Files, trusted bool) Errors {
	var errs Errors
	id := m["id"].(string)
	firstParty := strings.HasPrefix(id, "tv.maktaba.") && !strings.HasPrefix(id, "tv.maktaba.community.")
	if trusted && !firstParty || !trusted && reservedID(id) {
		errs = append(errs, Error{Code: CodeReservedID, Path: "id"})
	}
	platforms := m["platforms"].([]any)
	if len(platforms) > 1 {
		for _, p := range platforms {
			if p == "any" {
				errs = append(errs, Error{Code: CodeBadPlatform, Path: "platforms", Detail: "any stands alone"})
				break
			}
		}
	}
	if !hasLowerBound(m["maktaba"].(string)) {
		errs = append(errs, Error{Code: CodeBadRange, Path: "maktaba", Detail: "no lower bound"})
	}
	caps := m["capabilities"].(map[string]any)
	seen := map[string]bool{}
	for _, list := range []string{"required", "optional"} {
		for i, c := range caps[list].([]any) {
			name := c.(string)
			path := "capabilities/" + list + "/" + strconv.Itoa(i)
			if !knownCapability(name) {
				errs = append(errs, Error{Code: CodeBadCapability, Path: path, Detail: name})
				continue
			}
			if seen[name] {
				errs = append(errs, Error{Code: CodeDuplicateCapability, Path: path, Detail: name})
			}
			seen[name] = true
		}
	}
	stages := m["stage"].([]any)
	subjects := map[string]string{}
	for i, s := range stages {
		st := s.(map[string]any)
		name := st["name"].(string)
		if _, dup := subjects[name]; dup {
			errs = append(errs, Error{Code: CodeDuplicateStage, Path: "stage/" + strconv.Itoa(i) + "/name", Detail: name})
		}
		subjects[name] = st["subject"].(string)
		// R5a: the dup_group subject is Maktaba's own (54.22 §1). A core stage's name is not
		// reserved: a third-party stage's queue is p_<hash>_<name>, never a core stage's
		if !trusted && subjects[name] == "dup_group" {
			errs = append(errs, Error{Code: CodeTrustedField, Path: "stage/" + strconv.Itoa(i) + "/name", Detail: name})
		}
	}
	for i, s := range stages {
		st := s.(map[string]any)
		base := "stage/" + strconv.Itoa(i)
		for j, r := range st["may_request"].([]any) {
			// a first-party stage requests other core stages by their exact names: the
			// assembled graph checks them (54.25 §1)
			if _, ok := subjects[r.(string)]; !ok && !trusted {
				errs = append(errs, Error{Code: CodeBadRequest, Path: base + "/may_request/" + strconv.Itoa(j), Detail: r.(string)})
			}
		}
		for j, t := range st["trigger"].([]any) {
			ev := t.(map[string]any)["event"].(string)
			path := base + "/trigger/" + strconv.Itoa(j) + "/event"
			e, ok := eventByName[ev]
			switch {
			case !ok || !e.Triggerable():
				errs = append(errs, Error{Code: CodeUnknownEvent, Path: path, Detail: ev})
			case e.Subject != st["subject"].(string):
				errs = append(errs, Error{Code: CodeSubjectMismatch, Path: path, Detail: ev})
			}
		}
	}
	for i, s := range m["schedule"].([]any) {
		stage := s.(map[string]any)["stage"].(string)
		if subjects[stage] != "catalog" {
			errs = append(errs, Error{Code: CodeBadSchedule, Path: "schedule/" + strconv.Itoa(i) + "/stage", Detail: stage})
		}
	}
	if files == nil || len(errs) > 0 {
		return errs
	}
	return append(errs, packageRules(m, files)...)
}

func packageRules(m map[string]any, files Files) Errors {
	var errs Errors
	strs := m["strings"].(map[string]any)
	for _, lang := range []string{"en", "ar"} {
		path := "i18n/" + lang + ".json"
		raw, ok := files(path)
		if !ok {
			errs = append(errs, Error{Code: CodeMissingFile, Path: "strings", Detail: path})
			continue
		}
		var words map[string]any
		if json.Unmarshal(raw, &words) != nil {
			errs = append(errs, Error{Code: CodeMissingString, Path: "strings", Detail: lang})
			continue
		}
		for _, f := range []struct {
			key string
			max int
		}{{"name", MaxNameCodePoints}, {"summary", MaxSummaryCodePoints}} {
			w, ok := words[strs[f.key].(string)].(string)
			switch {
			case !ok || strings.TrimSpace(w) == "":
				errs = append(errs, Error{Code: CodeMissingString, Path: "strings/" + f.key, Detail: lang})
			case utf8.RuneCountInString(w) > f.max:
				errs = append(errs, Error{Code: CodeStringTooLong, Path: "strings/" + f.key, Detail: lang})
			}
		}
	}
	var props map[string]any
	if set, ok := m["settings"].(map[string]any); ok {
		path := set["schema"].(string)
		raw, ok := files(path)
		if !ok {
			errs = append(errs, Error{Code: CodeMissingFile, Path: "settings/schema", Detail: path})
		} else {
			var doc map[string]any
			if json.Unmarshal(raw, &doc) != nil {
				errs = append(errs, Error{Code: CodeBadSettings, Path: "settings/schema", Detail: path})
			} else {
				props, _ = doc["properties"].(map[string]any)
			}
		}
	}
	for i, s := range m["schedule"].([]any) {
		field := s.(map[string]any)["every_minutes_setting"].(string)
		path := "schedule/" + strconv.Itoa(i) + "/every_minutes_setting"
		if _, ok := m["settings"]; !ok {
			errs = append(errs, Error{Code: CodeBadSchedule, Path: path, Detail: "no settings"})
			continue
		}
		p, ok := props[field].(map[string]any)
		if !ok {
			errs = append(errs, Error{Code: CodeBadSchedule, Path: path, Detail: field})
			continue
		}
		min, isNum := p["minimum"].(float64)
		if p["type"] != "integer" || !isNum || min < 15 {
			errs = append(errs, Error{Code: CodeBadSchedule, Path: path, Detail: field})
		}
	}
	return errs
}

// reservedID: tv.maktaba.* (except tv.maktaba.community.*), dev.maktaba.*, and any label
// "maktaba" or "official" (outside the community prefix itself).
func reservedID(id string) bool {
	labels := strings.Split(id, ".")
	if strings.HasPrefix(id, "tv.maktaba.community.") {
		labels = labels[3:]
	} else if strings.HasPrefix(id, "tv.maktaba.") || strings.HasPrefix(id, "dev.maktaba.") {
		return true
	}
	for _, l := range labels {
		if l == "maktaba" || l == "official" {
			return true
		}
	}
	return false
}

func knownCapability(name string) bool {
	base, param, hasParam := strings.Cut(name, ":")
	c, ok := capByName[base]
	if !ok {
		return false
	}
	if c.Param == "" {
		return !hasParam
	}
	return hasParam && ValidID(param)
}

// normaliseRange writes a range as comparators joined by ", " with no other spaces.
func normaliseRange(r string) string {
	parts := strings.Split(r, ",")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, " ", "")
	}
	return strings.Join(parts, ", ")
}

func hasLowerBound(r string) bool {
	for _, p := range strings.Split(r, ", ") {
		if strings.HasPrefix(p, ">") || strings.HasPrefix(p, "=") {
			return true
		}
	}
	return false
}

var (
	reservedOnce   sync.Once
	reservedStages map[string]bool
)

// ReservedStage reports whether name is a stage of Maktaba's own graph
// (shared/plugins/reserved_stages.txt), which only first-party plugins may define.
func ReservedStage(name string) bool {
	reservedOnce.Do(func() {
		reservedStages = map[string]bool{}
		for _, line := range strings.Split(string(plugins.ReservedStages), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				reservedStages[line] = true
			}
		}
	})
	return reservedStages[name]
}
