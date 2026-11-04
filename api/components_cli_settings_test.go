package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
)

// fakeSettings answers reset-settings like the service, and records the resets.
type fakeSettings struct {
	resets []components.Actor
}

func (f *fakeSettings) SettingsOf(_ context.Context, id string) (components.SettingsReset, error) {
	if id == "nope" {
		return components.SettingsReset{}, components.UnknownRefusal(id)
	}
	return components.SettingsReset{ID: id, Changed: 7,
		Sections: []components.SectionSettings{{Section: "transcription", Changed: 7}}}, nil
}

func (f *fakeSettings) ResetSettings(ctx context.Context, id string, a components.Actor) (components.SettingsReset, error) {
	f.resets = append(f.resets, a)
	return f.SettingsOf(ctx, id)
}

// Story 51.13: reset-settings says what is lost and asks first (exit 2 with no terminal
// and no --yes); --yes resets as the OS user, actor source cli.
func TestComponentsCLIResetSettings(t *testing.T) {
	s := &fakeSettings{}
	c, out, errOut := cli(&fakeComponents{}, false, "")
	c.settings = s
	if code := runCLI(c, "reset-settings", "transcription"); code != exitComponentsNeedYes || len(s.resets) != 0 {
		t.Fatalf("no --yes: exit %d, resets %v: %s", code, s.resets, errOut)
	}
	if !strings.Contains(out.String(), "Your changes to 7 settings will be lost.") {
		t.Fatalf("the question: %q", out)
	}
	c, _, _ = cli(&fakeComponents{}, true, "n\n")
	c.settings = s
	if code := runCLI(c, "reset-settings", "transcription"); code != exitComponentsFailed || len(s.resets) != 0 {
		t.Fatalf("declined: exit %d", code)
	}
	c, out, _ = cli(&fakeComponents{}, false, "")
	c.settings = s
	if code := runCLI(c, "reset-settings", "transcription", "--yes"); code != 0 ||
		len(s.resets) != 1 || s.resets[0] != (components.Actor{By: "maya", Source: "cli"}) ||
		!strings.Contains(out.String(), "rules.transcription: defaults") {
		t.Fatalf("--yes: exit %d, resets %+v: %s", code, s.resets, out)
	}
	c, _, errOut = cli(&fakeComponents{}, false, "")
	c.settings = s
	if code := runCLI(c, "reset-settings", "nope", "--yes"); code != exitComponentsFailed || !strings.Contains(errOut.String(), "unknown_component") {
		t.Fatalf("unknown: exit %d: %s", code, errOut)
	}
}
