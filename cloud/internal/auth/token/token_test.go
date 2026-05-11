package token

import (
	"strings"
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	s := NewSigner([]byte("test-secret-must-be-at-least-32-bytes-long"))
	now := time.Now()
	tok, err := s.Issue("user-1", "u@example.com", "pro", now)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(strings.Split(tok, ".")) != 3 {
		t.Errorf("token does not have 3 segments: %q", tok)
	}
	c, err := s.Verify(tok, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if c.Sub != "user-1" || c.Email != "u@example.com" || c.Plan != "pro" {
		t.Errorf("claims mismatch: %+v", c)
	}
}

func TestVerify_Expired(t *testing.T) {
	s := NewSigner([]byte("test-secret-must-be-at-least-32-bytes-long"))
	now := time.Now()
	tok, _ := s.Issue("u", "", "", now)
	if _, err := s.Verify(tok, now.Add(2*AccessTTL)); err != ErrExpired {
		t.Errorf("Verify(expired) = %v, want %v", err, ErrExpired)
	}
}

func TestVerify_BadSignature(t *testing.T) {
	a := NewSigner([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	b := NewSigner([]byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	tok, _ := a.Issue("u", "", "", time.Now())
	if _, err := b.Verify(tok, time.Now()); err != ErrSignature {
		t.Errorf("Verify(wrong key) = %v, want %v", err, ErrSignature)
	}
}
