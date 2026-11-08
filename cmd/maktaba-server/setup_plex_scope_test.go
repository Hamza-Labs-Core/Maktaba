package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// The setup's Plex step offers Plex Live TV only: Maktaba replaces Plex as the media
// server, so it no longer brings in Plex's libraries or watch history.
func TestSetupPlexOffersLiveTVOnly(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	was := os.Stdout
	os.Stdout = w
	var actions []string
	run := func(step string, v any) (any, error) {
		actions = append(actions, step+":"+v.(map[string]string)["action"])
		return nil, nil
	}
	err = setupPlexCLI(context.Background(), newPrompterFrom(strings.NewReader("n\n")), run, nil)
	os.Stdout = was
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if err != nil || len(actions) != 1 || actions[0] != "plex:skip" {
		t.Fatalf("declined: err %v, steps %v (want only plex:skip)", err, actions)
	}
	if !strings.Contains(string(out), "Plex Live TV") {
		t.Errorf("the question does not name Plex Live TV: %q", out)
	}
	for _, en := range []string{string(out), webSetupTexts["setup.plex.intro"][0]} {
		if strings.Contains(en, "watch history") || strings.Contains(en, "libraries") {
			t.Errorf("still offers Plex's libraries: %q", en)
		}
	}
	intro := webSetupTexts["setup.plex.intro"]
	if !strings.Contains(intro[0], "Plex Live TV") || !strings.Contains(intro[1], "Plex") || intro[1] == intro[0] {
		t.Errorf("setup.plex.intro (en, ar): %q", intro)
	}
}
