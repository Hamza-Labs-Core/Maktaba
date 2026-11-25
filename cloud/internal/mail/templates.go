package mail

import (
	"html"
	"net/url"
	"strings"
)

// The mails' languages.
const (
	LangEN = "en"
	LangAR = "ar"
)

// Lang is tag's language when it is one the mails are written in ("ar-EG" is ar), else "".
func Lang(tag string) string {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	if base == LangEN || base == LangAR {
		return base
	}
	return ""
}

// Pick is the first of tags the mails are written in, else English.
func Pick(tags ...string) string {
	for _, t := range tags {
		if l := Lang(t); l != "" {
			return l
		}
	}
	return LangEN
}

// Templates builds the account mails. WebURL is the cloud web app's
// origin; the links land on its pages, which post the token to the API.
type Templates struct {
	WebURL string
}

// content is one mail's words in one language.
type content struct {
	subject string
	before  []string
	// the link: its button's label and its path on the web app ("" is none)
	label, path string
	after       []string
}

// l2 is English and Arabic of one text.
type l2 struct{ en, ar string }

func (t l2) in(lang string) string {
	if lang == LangAR {
		return t.ar
	}
	return t.en
}

var footer = l2{
	"You get this mail because of a Maktaba Cloud account with this address.",
	"تصلك هذه الرسالة لأن لهذا العنوان حسابًا في سحابة مكتبة.",
}

var signupFooter = l2{
	"You get this mail because this address was used on Maktaba Cloud.",
	"تصلك هذه الرسالة لأن هذا العنوان استُخدم في سحابة مكتبة.",
}

func (t Templates) url(path string) string { return strings.TrimRight(t.WebURL, "/") + path }

func (t Templates) message(lang, to, kind string, foot l2, c content) Message {
	m := Message{To: to, Kind: kind, Subject: c.subject, Lang: lang}
	parts := append([]string(nil), c.before...)
	link := ""
	if c.path != "" {
		link = t.url(c.path)
		parts = append(parts, c.label+":\n"+link)
	}
	parts = append(parts, c.after...)
	m.Body = strings.Join(append(parts, "-- \n"+foot.in(lang)), "\n\n") + "\n"

	var b strings.Builder
	for _, p := range c.before {
		b.WriteString(para(p))
	}
	if link != "" {
		u := html.EscapeString(link)
		b.WriteString(`<p style="margin:24px 0"><a href="` + u + `" style="display:inline-block;padding:12px 20px;border-radius:8px;` +
			`background:#1f2937;color:#ffffff;text-decoration:none;font-weight:600">` + html.EscapeString(c.label) + `</a></p>` +
			`<p dir="ltr" style="margin:0 0 16px;font-size:13px;color:#6b7280;word-break:break-all;text-align:start">` + u + `</p>`)
	}
	for _, p := range c.after {
		b.WriteString(para(p))
	}
	m.HTML = page(lang, c.subject, b.String(), foot.in(lang))
	return m
}

func para(s string) string {
	return `<p style="margin:0 0 16px">` + strings.ReplaceAll(html.EscapeString(s), "\n", "<br>") + `</p>`
}

// page is the mail's one layout: text and a link, nothing loaded from anywhere.
func page(lang, title, inner, foot string) string {
	dir := "ltr"
	if lang == LangAR {
		dir = "rtl"
	}
	return `<!doctype html><html lang="` + lang + `" dir="` + dir + `"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark">` +
		`<title>` + html.EscapeString(title) + `</title></head>` +
		`<body dir="` + dir + `" style="margin:0;padding:24px;font-family:-apple-system,'Segoe UI',Tahoma,Arial,sans-serif;font-size:16px;line-height:1.6">` +
		`<div dir="` + dir + `" style="max-width:560px;margin:0 auto;text-align:start">` +
		`<h1 style="font-size:20px;margin:0 0 16px">` + html.EscapeString(title) + `</h1>` + inner +
		`<p style="margin:32px 0 0;font-size:13px;color:#6b7280">` + html.EscapeString(foot) + `</p></div></body></html>`
}

// VerifyEmail is sent at registration and on resend.
func (t Templates) VerifyEmail(lang, to, token string) Message {
	path := "/verify-email?token=" + url.QueryEscape(token)
	c := content{subject: "Confirm your Maktaba Cloud email", path: path, label: "Confirm your email",
		before: []string{"Confirm your email to finish setting up your Maktaba Cloud account."},
		after:  []string{"The link works for 24 hours. If you did not sign up, ignore this mail."}}
	if lang == LangAR {
		c = content{subject: "أكّد بريدك في سحابة مكتبة", path: path, label: "أكّد بريدك",
			before: []string{"أكّد بريدك الإلكتروني لإكمال إعداد حسابك في سحابة مكتبة."},
			after:  []string{"يعمل الرابط مدة 24 ساعة. إن لم تكن أنت من سجّل، فتجاهل هذه الرسالة."}}
	}
	return t.message(lang, to, KindVerifyEmail, signupFooter, c)
}

// PasswordReset is sent by "forgot password".
func (t Templates) PasswordReset(lang, to, token string) Message {
	path := "/reset-password?token=" + url.QueryEscape(token)
	c := content{subject: "Reset your Maktaba Cloud password", path: path, label: "Choose a new password",
		before: []string{"Someone asked to reset the password of your Maktaba Cloud account."},
		after:  []string{"The link works once, for 1 hour. If it was not you, ignore this mail: your password stays as it is."}}
	if lang == LangAR {
		c = content{subject: "إعادة تعيين كلمة مرورك في سحابة مكتبة", path: path, label: "اختر كلمة مرور جديدة",
			before: []string{"طلب أحدهم إعادة تعيين كلمة مرور حسابك في سحابة مكتبة."},
			after:  []string{"يعمل الرابط مرة واحدة، ومدة ساعة واحدة. إن لم تكن أنت، فتجاهل هذه الرسالة: تبقى كلمة مرورك كما هي."}}
	}
	return t.message(lang, to, KindPasswordReset, footer, c)
}

// AlreadyRegistered goes to an existing account when someone tries to
// register its address, so registration does not reveal which
// addresses have accounts.
func (t Templates) AlreadyRegistered(lang, to string) Message {
	c := content{subject: "Someone tried to sign up with your email", path: "/sign-in", label: "Sign in",
		before: []string{"Someone tried to create a Maktaba Cloud account with this address, which already has one."},
		after:  []string{"Forgot your password? Choose a new one: " + t.url("/forgot-password"), "If it was not you, there is nothing to do."}}
	if lang == LangAR {
		c = content{subject: "حاول أحدهم التسجيل ببريدك", path: "/sign-in", label: "تسجيل الدخول",
			before: []string{"حاول أحدهم إنشاء حساب في سحابة مكتبة بهذا العنوان، وله حساب من قبل."},
			after:  []string{"نسيت كلمة مرورك؟ اختر كلمة جديدة: " + t.url("/forgot-password"), "إن لم تكن أنت، فلا شيء عليك فعله."}}
	}
	return t.message(lang, to, KindAlreadyRegistered, footer, c)
}

// Test is the operator's test mail (`maktaba-cloud mail test`).
func (t Templates) Test(lang, to string) Message {
	c := content{subject: "Maktaba Cloud: a test mail", path: "/sign-in", label: "Open Maktaba Cloud",
		before: []string{"This is a test. Maktaba Cloud's mail reaches this address."}}
	if lang == LangAR {
		c = content{subject: "سحابة مكتبة: رسالة تجريبية", path: "/sign-in", label: "افتح سحابة مكتبة",
			before: []string{"هذه رسالة تجريبية. بريد سحابة مكتبة يصل إلى هذا العنوان."}}
	}
	return t.message(lang, to, KindTest, signupFooter, c)
}

type notice struct{ subject, body l2 }

var billingPage = l2{"Open the billing page", "افتح صفحة الفوترة"}

// notices are the account notices, each with a link to the billing page.
var notices = map[string]notice{
	"payment_failed": {
		l2{"Your Maktaba Cloud payment failed", "لم تتم دفعتك لسحابة مكتبة"},
		l2{"We could not take your payment for Maktaba Cloud. Update your payment method on the billing page.",
			"لم نستطع تحصيل دفعتك لسحابة مكتبة. حدّث طريقة الدفع من صفحة الفوترة."},
	},
	"payg_near_cap": {
		l2{"80% of your pay-as-you-go limit is used", "استُهلك 80% من حد الدفع حسب الاستخدام"},
		l2{"You've used 80% of your pay-as-you-go limit this month.", "استهلكت 80% من حد الدفع حسب الاستخدام لهذا الشهر."},
	},
	"payg_cap_reached": {
		l2{"Your pay-as-you-go limit is reached", "بلغت حد الدفع حسب الاستخدام"},
		l2{"You've reached your pay-as-you-go limit. Remote play continues at Basic quality until next month.",
			"بلغت حد الدفع حسب الاستخدام. يستمر التشغيل عن بُعد بجودة الخطة الأساسية حتى الشهر القادم."},
	},
	"broadcast_allowance_80": {
		l2{"80% of this month's viewing is used", "استُهلك 80% من مشاهدة هذا الشهر"},
		l2{"Your channels have used 80% of this month's viewer-hours.", "استهلكت قنواتك 80% من ساعات المشاهدة لهذا الشهر."},
	},
	"broadcast_allowance_100": {
		l2{"This month's viewing allowance is used", "استُهلكت حصة المشاهدة لهذا الشهر"},
		l2{"Your channels have used this month's viewer-hours. New viewers now follow your plan: pay as you go if it is on, then lower quality, then a wait. People already watching continue.",
			"استهلكت قنواتك ساعات المشاهدة لهذا الشهر. يتبع المشاهدون الجدد خطتك: الدفع حسب الاستخدام إن كان مفعّلًا، ثم جودة أقل، ثم الانتظار. ويستمر من يشاهدون الآن."},
	},
	"broadcast_cap_80": {
		l2{"80% of your viewing limit is used", "استُهلك 80% من حد المشاهدة"},
		l2{"You've used 80% of this month's pay-as-you-go limit for your channels.", "استهلكت 80% من حد الدفع حسب الاستخدام لقنواتك هذا الشهر."},
	},
	"broadcast_cap_100": {
		l2{"Your viewing limit is reached", "بلغت حد المشاهدة"},
		l2{"You've reached this month's pay-as-you-go limit. New viewers watch in lower quality until next month, or until you raise your limit.",
			"بلغت حد الدفع حسب الاستخدام لهذا الشهر. يشاهد المشاهدون الجدد بجودة أقل حتى الشهر القادم، أو حتى ترفع الحد."},
	},
}

// NoticeKinds are the notices' kinds.
func NoticeKinds() []string {
	out := make([]string, 0, len(notices))
	for k := range notices {
		out = append(out, k)
	}
	return out
}

// Notice is an account notice (a payment, a limit), or false for a kind there is none of.
func (t Templates) Notice(lang, to, kind string) (Message, bool) {
	n, ok := notices[kind]
	if !ok {
		return Message{}, false
	}
	return t.message(lang, to, kind, footer, content{subject: n.subject.in(lang), before: []string{n.body.in(lang)},
		path: "/billing", label: billingPage.in(lang)}), true
}

// Both is a mail written in both languages at once, for a recipient whose language is not
// known (a copyright notice's sender has no account): en above, ar below.
func Both(to, kind, subject, en, ar string) Message {
	half := func(lang, dir, s string) string {
		return `<div lang="` + lang + `" dir="` + dir + `" style="text-align:start">` + para(s) + `</div>`
	}
	return Message{To: to, Kind: kind, Subject: subject, Body: en + "\n\n———\n\n" + ar,
		HTML: page(LangEN, subject, half(LangEN, "ltr", en)+`<hr style="border:0;border-top:1px solid #d1d5db;margin:24px 0">`+half(LangAR, "rtl", ar),
			"Maktaba Cloud · سحابة مكتبة")}
}
