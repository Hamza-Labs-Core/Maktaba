package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The terminal says what the desktop says: every key it shares with the web
// app has the web's text, in both languages.
func TestSetupTextsMatchTheApp(t *testing.T) {
	web := map[string]map[string]string{}
	for _, lang := range setupLangs {
		b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "i18n", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		web[lang] = m
	}
	for key, v := range setupTexts {
		if v[0] == "" || v[1] == "" {
			t.Errorf("%s lacks a language", key)
		}
		if strings.HasPrefix(key, "terminal.") {
			continue
		}
		for i, lang := range setupLangs {
			want, ok := web[lang][key]
			if !ok {
				t.Errorf("%s is not the app's (web/src/i18n/%s.json): use a terminal. key", key, lang)
				continue
			}
			if v[i] != want {
				t.Errorf("%s (%s) = %q, the app says %q", key, lang, v[i], want)
			}
		}
	}
}

func TestSetupLang(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		flag string
		env  map[string]string
		want string
	}{
		{"", nil, "en"},
		{"", map[string]string{"LANG": "ar_SA.UTF-8"}, "ar"},
		{"", map[string]string{"LANG": "ar_SA.UTF-8", "LC_ALL": "en_US.UTF-8"}, "en"},
		{"", map[string]string{"LC_MESSAGES": "ar_EG"}, "ar"},
		{"ar", map[string]string{"LANG": "en_GB.UTF-8"}, "ar"},
		{"en", map[string]string{"LANG": "ar"}, "en"},
	} {
		if got, err := setupLang(c.flag, env(c.env)); err != nil || got != c.want {
			t.Errorf("%q %v: %q %v", c.flag, c.env, got, err)
		}
	}
	if _, err := setupLang("de", env(nil)); err == nil {
		t.Error("an unknown language was taken")
	}
	if got := textsFor("en").get("terminal.done", "url", "http://x"); got != "Maktaba is set up. Open http://x (or this computer's address on your network)." {
		t.Errorf("placeholders: %q", got)
	}
}
