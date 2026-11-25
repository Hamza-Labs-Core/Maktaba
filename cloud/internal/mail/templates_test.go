package mail

import (
	"context"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const arabic = "ابتثجحخدذرزسشصضطظعغفقكلمنهوي"

var links = regexp.MustCompile(`https?://[^\s"<>]+`)

func TestTemplatesInEnglishAndArabic(t *testing.T) {
	tpl := Templates{WebURL: "https://app.example.test/"}
	const tok = "a+b/c=&<x>"
	q := url.QueryEscape(tok)
	mails := map[string]func(lang string) (Message, string){
		KindVerifyEmail: func(l string) (Message, string) {
			return tpl.VerifyEmail(l, "a@example.test", tok), "/verify-email?token=" + q
		},
		KindPasswordReset: func(l string) (Message, string) {
			return tpl.PasswordReset(l, "a@example.test", tok), "/reset-password?token=" + q
		},
		KindAlreadyRegistered: func(l string) (Message, string) { return tpl.AlreadyRegistered(l, "a@example.test"), "/sign-in" },
		KindTest:              func(l string) (Message, string) { return tpl.Test(l, "a@example.test"), "/sign-in" },
	}
	if len(NoticeKinds()) < 7 {
		t.Fatalf("notices: %v", NoticeKinds())
	}
	for _, k := range NoticeKinds() {
		mails[k] = func(l string) (Message, string) {
			m, ok := tpl.Notice(l, "a@example.test", k)
			if !ok {
				t.Fatalf("no notice %s", k)
			}
			return m, "/billing"
		}
	}
	for kind, build := range mails {
		en, path := build(LangEN)
		ar, _ := build(LangAR)
		for lang, m := range map[string]Message{LangEN: en, LangAR: ar} {
			if m.Kind != kind || m.To != "a@example.test" || m.Lang != lang || m.Subject == "" || strings.ContainsAny(m.Subject, "\r\n") {
				t.Errorf("%s %s: %+v", kind, lang, m)
			}
			if !strings.Contains(m.Body, "https://app.example.test"+path+"\n") {
				t.Errorf("%s %s: no link in the text: %q", kind, lang, m.Body)
			}
			if !strings.Contains(m.HTML, `href="`+html.EscapeString("https://app.example.test"+path)+`"`) {
				t.Errorf("%s %s: no link in the HTML", kind, lang)
			}
			// nothing is loaded from anywhere, and every link is the web app's
			for _, bad := range []string{"<img", "<script", "<link", "url(", "@import", "http://"} {
				if strings.Contains(m.HTML, bad) {
					t.Errorf("%s %s: the HTML holds %s", kind, lang, bad)
				}
			}
			for _, l := range append(links.FindAllString(m.HTML, -1), links.FindAllString(m.Body, -1)...) {
				if !strings.HasPrefix(l, "https://app.example.test/") || strings.HasPrefix(l, "https://app.example.test//") {
					t.Errorf("%s %s: links to %s", kind, lang, l)
				}
			}
		}
		if !strings.Contains(en.HTML, `<html lang="en" dir="ltr">`) || !strings.Contains(ar.HTML, `<html lang="ar" dir="rtl">`) ||
			!strings.Contains(ar.HTML, `<body dir="rtl"`) {
			t.Errorf("%s: direction", kind)
		}
		if !strings.ContainsAny(ar.Subject, arabic) || !strings.ContainsAny(ar.Body, arabic) || strings.ContainsAny(en.Subject+en.Body, arabic) {
			t.Errorf("%s: languages: %q / %q", kind, en.Subject, ar.Subject)
		}
		if len(strings.Split(en.Body, "\n\n")) != len(strings.Split(ar.Body, "\n\n")) {
			t.Errorf("%s: the two languages say different things", kind)
		}
	}
	if _, ok := tpl.Notice(LangEN, "a@example.test", "nope"); ok {
		t.Error("a notice of no kind")
	}
}

// The moderation mails: both languages in one, each in its own direction, and escaped.
func TestBothLanguages(t *testing.T) {
	m := Both("a@example.test", "moderation_x", "S", "One <b>\ntwo", "واحد")
	if m.Body != "One <b>\ntwo\n\n———\n\nواحد" {
		t.Errorf("%q", m.Body)
	}
	if !strings.Contains(m.HTML, `<div lang="ar" dir="rtl"`) || !strings.Contains(m.HTML, "One &lt;b&gt;<br>two") {
		t.Errorf("%s", m.HTML)
	}
}

func TestLang(t *testing.T) {
	for in, want := range map[string]string{"ar": "ar", "AR-eg": "ar", "en-GB": "en", "fr": "", "": "", "arz": ""} {
		if got := Lang(in); got != want {
			t.Errorf("Lang(%q) = %q", in, got)
		}
	}
	if Pick("", "ar") != "ar" || Pick("fr", "") != "en" || Pick("ar", "en") != "ar" {
		t.Error("Pick")
	}
}

func TestGuard(t *testing.T) {
	rec := &Recorder{}
	g := Guard{Next: rec, Allow: []string{"@Maktaba.tv", "delivered@resend.dev"}, Prefix: "[QA]"}
	for to, ok := range map[string]bool{"a@maktaba.tv": true, "A@MAKTABA.TV": true, "delivered@resend.dev": true,
		"other@resend.dev": false, "a@sub.maktaba.tv": false, "a@evilmaktaba.tv": false, "a@maktaba.tv.evil.test": false,
		"ci-e2e-1@example.com": false, "nobody": false, "a@maktaba.tv@example.com": false} {
		err := g.Send(context.Background(), Message{To: to, Subject: "S"})
		if (err == nil) != ok || (!ok && !errors.Is(err, ErrSuppressed)) {
			t.Errorf("%s: %v", to, err)
		}
	}
	if len(rec.Sent) != 3 || rec.Sent[0].Subject != "[QA] S" {
		t.Errorf("%+v", rec.Sent)
	}
	if err := (Guard{Next: rec}).Send(context.Background(), Message{To: "anyone@example.test", Subject: "S"}); err != nil {
		t.Error(err)
	}
	if rec.Sent[3].Subject != "S" {
		t.Errorf("%+v", rec.Sent[3])
	}
	for e, ok := range map[string]bool{"a@example.test": true, "@example.test": true, "example.test": false, "@": false, "": false, "X <a@b.test>": false} {
		if (ParseAllow(e) == nil) != ok {
			t.Errorf("ParseAllow(%q)", e)
		}
	}
}
