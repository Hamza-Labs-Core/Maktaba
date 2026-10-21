package main

import (
	"fmt"
	"strings"
)

// The terminal setup's words, in English and Arabic (Epic 39 story 39.6). Keys
// that start with "setup." or "login." are the desktop app's own
// (web/src/i18n/{en,ar}.json), with the same text, so both setups say the same
// thing; setup_app_text_test.go keeps them equal. "terminal." keys are the
// terminal's own. A step's error is shown by its code (setup.error.<code>),
// else by the step's message.
var setupTexts = map[string][2]string{ // key → {en, ar}
	"login.username":                   {"Username", "اسم المستخدم"},
	"login.password":                   {"Password", "كلمة المرور"},
	"setup.check.title":                {"What will run", "ما الذي سيعمل"},
	"setup.check.dataDir":              {"Data folder", "مجلد البيانات"},
	"setup.admin.title":                {"Your admin account", "حساب المدير"},
	"setup.admin.intro":                {"You sign in to Maktaba with this account. Keep the password somewhere safe.", "تسجّل الدخول إلى مكتبة بهذا الحساب. احفظ كلمة المرور في مكان آمن."},
	"setup.admin.confirm":              {"Password again", "كلمة المرور مرة أخرى"},
	"setup.admin.mismatch":             {"The two passwords are not the same.", "كلمتا المرور غير متطابقتين."},
	"setup.libraries.title":            {"Your library folders", "مجلدات مكتبتك"},
	"setup.libraries.intro":            {"Which folders hold your lectures? Maktaba reads them; it never moves or changes your files.", "أي المجلدات تحوي محاضراتك؟ تقرؤها مكتبة ولا تنقل ملفاتك ولا تغيّرها."},
	"setup.libraries.mapNote":          {"If other machines see the same files under another path, write one \"their path => the path here\" per line.", "إذا رأت أجهزة أخرى الملفات نفسها بمسار آخر، فاكتب في كل سطر \"مسارها => المسار هنا\"."},
	"setup.libraries.mapBad":           {"Write each line as \"their path => the path here\".", "اكتب كل سطر هكذا: \"مسارها => المسار هنا\"."},
	"setup.models.title":               {"Transcription models", "نماذج التفريغ"},
	"setup.models.intro":               {"Maktaba transcribes on {machine}. It needs the speech models (a few GB).", "تفرّغ مكتبة المحاضرات على {machine}، وتحتاج إلى نماذج الكلام (بضعة غيغابايتات)."},
	"setup.models.hfNote":              {"The second-listen model (Cohere) needs a Hugging Face account: accept the model's terms, then make a read token.", "نموذج الاستماع الثاني (Cohere) يحتاج إلى حساب في Hugging Face: وافق على شروط النموذج ثم أنشئ رمز قراءة."},
	"setup.models.nasPath":             {"Folder with the models", "المجلد الذي فيه النماذج"},
	"setup.plex.title":                 {"Plex (optional)", "Plex (اختياري)"},
	"setup.plex.intro":                 {"Sign in to Plex to bring in your Plex libraries and watch history. You can do this later in Settings.", "سجّل الدخول إلى Plex لجلب مكتبات Plex وسجل المشاهدة. يمكنك فعل ذلك لاحقًا من الإعدادات."},
	"setup.plex.enterCode":             {"Enter this code at {url}:", "أدخل هذا الرمز في {url}:"},
	"setup.plex.waiting":               {"Waiting for Plex…", "بانتظار Plex…"},
	"setup.install.title":              {"Getting Maktaba ready", "تجهيز مكتبة"},
	"setup.install.venv":               {"Installing the transcription and search tools. This downloads about 2 GB and takes a few minutes.", "جارٍ تثبيت أدوات التفريغ والبحث. يُنزَّل نحو 2 غيغابايت ويستغرق بضع دقائق."},
	"setup.install.models":             {"Downloading the speech models…", "جارٍ تنزيل نماذج الكلام…"},
	"setup.install.llm":                {"Downloading the language model that checks tags and finds metadata (about 5 GB). If this stops, Maktaba finishes the download in the background.", "جارٍ تنزيل نموذج اللغة الذي يراجع الوسوم ويبحث عن البيانات الوصفية (نحو 5 غيغابايت). إن توقّف التنزيل أكملته مكتبة في الخلفية."},
	"setup.install.finishing":          {"Creating the library database…", "جارٍ إنشاء قاعدة بيانات المكتبة…"},
	"setup.machine.linux":              {"this computer", "هذا الحاسوب"},
	"setup.error.hf_token_invalid":     {"Hugging Face did not accept the token.", "لم يقبل Hugging Face الرمز."},
	"setup.error.hf_gated":             {"Accept the model's terms on huggingface.co with this account first.", "وافق أولًا على شروط النموذج في huggingface.co بهذا الحساب."},
	"setup.error.nas_missing":          {"That folder has no models.", "لا توجد نماذج في هذا المجلد."},
	"setup.error.disk_full":            {"The disk is full.", "القرص ممتلئ."},
	"setup.error.pin_expired":          {"The Plex code expired. Try again.", "انتهت صلاحية رمز Plex. حاول مرة أخرى."},
	"setup.error.venv_failed":          {"Installing the tools failed.", "فشل تثبيت الأدوات."},
	"setup.error.server_missing":       {"This copy of Maktaba cannot run the server.", "هذه النسخة من مكتبة لا تستطيع تشغيل الخادم."},
	"setup.error.bad_path_map":         {"Each line of the path map needs both paths.", "كل سطر في تحويل المسارات يحتاج إلى المسارين."},
	"setup.error.network":              {"Maktaba could not reach the internet.", "لم تستطع مكتبة الوصول إلى الإنترنت."},
	"setup.error.venv_missing":         {"The transcription tools are not installed yet.", "أدوات التفريغ لم تُثبَّت بعد."},
	"setup.error.download_failed":      {"Downloading the models failed.", "فشل تنزيل النماذج."},
	"setup.error.interrupted":          {"It stopped part way; it goes on from there next time.", "توقف في منتصف الطريق، ويكمل من حيث توقف في المرة القادمة."},
	"setup.error.migrate_failed":       {"Preparing the database failed.", "فشل تجهيز قاعدة البيانات."},
	"setup.error.admin_failed":         {"The admin account could not be created.", "تعذّر إنشاء حساب المدير."},
	"setup.error.admin_missing":        {"Create the admin account first.", "أنشئ حساب المدير أولًا."},
	"setup.error.database_failed":      {"The library database could not be created.", "تعذّر إنشاء قاعدة بيانات المكتبة."},
	"setup.error.database_unsupported": {"This install's database cannot be set up here.", "لا يمكن إعداد قاعدة بيانات هذا التثبيت هنا."},

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
	"terminal.plexLinked":    {"Plex is linked.", "تم ربط Plex."},
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

// stepError is a step's failure in the user's language: by its code when the
// app has a text for it, else the step's own message.
func (t texts) stepError(f *stepFailure) string {
	if _, ok := setupTexts["setup.error."+f.Code]; ok {
		return t.get("setup.error." + f.Code)
	}
	return f.Message
}
