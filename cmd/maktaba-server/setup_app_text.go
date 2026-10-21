package main

import (
	"fmt"
	"maps"
	"strings"
)

// The terminal setup's words, in English and Arabic (Epic 39 story 39.6), key
// → {en, ar}. The desktop app's own (setup.*, login.*: web/src/i18n) are
// webSetupTexts, generated from the app's files so both setups say the same
// thing; the terminal's own are terminalTexts. setup_app_text_test.go fails
// when the app's texts change or the code uses one more:
// `go test -run TestSetupTextsMatchTheApp -update` rewrites setup_texts_web.go.
var setupTexts = func() map[string][2]string {
	m := maps.Clone(webSetupTexts)
	maps.Copy(m, terminalTexts)
	return m
}()

// terminalTexts are the lines only the terminal says ("terminal." keys).
var terminalTexts = map[string][2]string{
	"terminal.intro":         {"Maktaba setup: this computer runs the server (no window needed).", "إعداد مكتبة: هذا الحاسوب يشغّل الخادم (لا حاجة إلى نافذة)."},
	"terminal.already":       {"This computer is set up already ({url}).", "هذا الحاسوب مُعدّ مسبقًا ({url})."},
	"terminal.serviceAgain":  {"Install and start its service again", "تثبيت خدمته وتشغيلها من جديد"},
	"terminal.note":          {"Note: {text}", "ملاحظة: {text}"},
	"terminal.blocked":       {"Maktaba cannot run its server on this computer:", "لا تستطيع مكتبة تشغيل خادمها على هذا الحاسوب:"},
	"terminal.blockedEnd":    {"this computer cannot run the server (see above)", "هذا الحاسوب لا يستطيع تشغيل الخادم (انظر أعلاه)"},
	"terminal.passwordEmpty": {"The password is empty.", "كلمة المرور فارغة."},
	"terminal.roots":         {"Library folders (separate them with commas)", "مجلدات المكتبة (افصل بينها بفواصل)"},
	"terminal.map":           {"NAS path map (separate entries with commas; empty: none)", "تحويل مسارات الـNAS (افصل بين الأسطر بفواصل؛ فارغ: لا شيء)"},
	"terminal.hfOrShare":     {"Hugging Face token (empty: use the copy on the share)", "رمز Hugging Face (فارغ: استخدام النسخة الموجودة على الـNAS)"},
	"terminal.plexAsk":       {"Sign in to Plex now", "تسجيل الدخول إلى Plex الآن"},
	"terminal.plexLater":     {"Plex is not linked now: link it later in Settings.", "لم يُربط Plex الآن: اربطه لاحقًا من الإعدادات."},
	"terminal.plexLinked":    {"Plex is linked.", "تم ربط Plex."},
	"terminal.plexSkip":      {"Press Ctrl+C to skip this and link Plex later in Settings.", "اضغط Ctrl+C لتخطّي هذا وربط Plex لاحقًا من الإعدادات."},
	"terminal.plexRetry":     {"Trying again…", "جارٍ المحاولة مرة أخرى…"},
	"terminal.plexExpired":   {"The Plex code expired: link Plex later in Settings.", "انتهت صلاحية رمز Plex: اربط Plex لاحقًا من الإعدادات."},
	"terminal.answersRead":   {"The answers file could not be read: {err}", "تعذّرت قراءة ملف الإجابات: {err}"},
	"terminal.answersStep":   {"The answers file names {step}, which is not a setup step (the steps are {steps}).", "يذكر ملف الإجابات {step}، وهي ليست خطوة من خطوات الإعداد (الخطوات هي {steps})."},
	"terminal.serviceFailed": {"The service could not be installed and started: {err}", "تعذّر تثبيت الخدمة وتشغيلها: {err}"},
	"terminal.copying":       {"Copying the server into {dir}", "جارٍ نسخ الخادم إلى {dir}"},
	"terminal.llmLater":      {"The language model was not downloaded now ({err}); the server downloads it in the background.", "لم يُنزَّل نموذج اللغة الآن ({err})؛ سيُنزّله الخادم في الخلفية."},
	"terminal.started":       {"Started the service {unit}.", "بدأت الخدمة {unit}."},
	"terminal.linger":        {"It starts at boot (linger).", "تبدأ عند إقلاع الحاسوب (linger)."},
	"terminal.noLinger":      {"It runs while you are logged in. To start it at boot, an administrator can run: loginctl enable-linger {user} ({err})", "تعمل ما دمت مسجّل الدخول. ليبدأ عند الإقلاع، يستطيع المسؤول تشغيل: loginctl enable-linger {user} ({err})"},
	"terminal.done":          {"Maktaba is set up. Open {url} (or this computer's address on your network).", "اكتمل إعداد مكتبة. افتح {url} (أو عنوان هذا الحاسوب على شبكتك)."},
	"terminal.cancelled":     {"Setup was cancelled; run it again to go on.", "أُلغي الإعداد؛ شغّله من جديد لتكمل."},
	"terminal.root":          {"This setup installs Maktaba for one user, as a user service: run it as that user, without sudo (an administrator's password is asked only to start it at boot).", "هذا الإعداد يثبّت مكتبة لمستخدم واحد كخدمة مستخدم: شغّله بهذا المستخدم دون sudo (تُطلب كلمة مرور المسؤول فقط ليبدأ عند الإقلاع)."},
	"terminal.yes":           {"y", "ن"},
	"terminal.no":            {"n", "ل"},
}

// setupLangs are the languages of the terminal setup, in setupTexts' order.
var setupLangs = []string{"en", "ar"}

// setupLang picks the terminal setup's language: --lang, else the locale
// (LC_ALL, LC_MESSAGES, LANG, the first one set), else English.
func setupLang(flag string, getenv func(string) string) (string, error) {
	if flag != "" {
		for _, l := range setupLangs {
			if flag == l {
				return l, nil
			}
		}
		return "", fmt.Errorf("--lang %q: one of %s", flag, strings.Join(setupLangs, ", "))
	}
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := getenv(k); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "ar") {
				return "ar", nil
			}
			return "en", nil
		}
	}
	return "en", nil
}

// texts are setupTexts in one language.
type texts struct{ ar bool }

func textsFor(lang string) texts { return texts{ar: lang == "ar"} }

// get is key's text with its {name} placeholders filled from pairs (name,
// value, ...). An unknown key is a bug: it shows as itself.
func (t texts) get(key string, pairs ...string) string {
	v, ok := setupTexts[key]
	if !ok {
		return key
	}
	s := v[0]
	if t.ar {
		s = v[1]
	}
	if len(pairs) > 0 {
		var r []string
		for i := 0; i+1 < len(pairs); i += 2 {
			r = append(r, "{"+pairs[i]+"}", pairs[i+1])
		}
		s = strings.NewReplacer(r...).Replace(s)
	}
	return s
}

// coded is what the screens' codedText shows: prefix.code with the params (a
// *_bytes number as a size) and the platform's words, else the step's English.
func (t texts) coded(prefix, code string, params map[string]any, english string) string {
	key := prefix + "." + code
	if _, ok := setupTexts[key]; !ok {
		return english
	}
	pairs := []string{"machine", t.get("setup.machine.linux")}
	for k, v := range params {
		switch n := v.(type) {
		case float64:
			if strings.HasSuffix(k, "_bytes") {
				pairs = append(pairs, k, formatBytes(n))
			} else {
				pairs = append(pairs, k, fmt.Sprint(n))
			}
		case nil:
		default:
			pairs = append(pairs, k, fmt.Sprint(n))
		}
	}
	return t.get(key, pairs...)
}

// stepError is a step's failure in the user's language: setup.error.<code>
// with its params, else the step's own message.
func (t texts) stepError(f *stepFailure) string {
	return t.coded("setup.error", f.Code, f.Params, f.Message)
}

// runText is one line of what will run, as the screens word it (runText in
// SetupWizard.tsx): the pipeline by its number of workers and its GPU.
func (t texts) runText(role, what string, params map[string]any) string {
	if role != "pipeline" {
		return t.coded("setup.check.run", role, params, what)
	}
	workers, _ := params["workers"].(float64)
	key := "pipeline"
	switch {
	case workers > 1:
		key = "pipelineWorkers"
	case workers == 1:
		key = "pipelineOne"
	}
	text := t.coded("setup.check.run", key, params, what)
	if gpu, ok := params["gpu"].(string); ok && gpu != "" && text != what {
		text += t.coded("setup.check.run", "onGpu", params, "")
	}
	return text
}

// formatBytes is the screens' formatBytes: "3.2 GB" (powers of 1024).
func formatBytes(n float64) string {
	if n < 0 {
		return "?"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%g %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}
