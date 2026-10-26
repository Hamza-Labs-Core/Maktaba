package main

import (
	"strings"
	"testing"
)

// [llm] url is logged without a login or a query that may carry a key.
func TestLLMLogURLDropsCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:11434":                        "http://127.0.0.1:11434",
		"https://bob:s3cret@llm.example.net:8443/v1":    "https://…@llm.example.net:8443/v1",
		"https://llm.example.net/v1?api_key=s3cret&x=1": "https://llm.example.net/v1?…",
	} {
		got := llmLogURL(in)
		if got != want || strings.Contains(got, "s3cret") {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
