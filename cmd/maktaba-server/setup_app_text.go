package main

import (
	"fmt"
	"strings"
)

// The terminal setup's words, in English and Arabic (Epic 39 story 39.6). Keys
// that start with "setup." or "login." are the desktop app's own
// (web/src/i18n/{en,ar}.json), with the same text, so both setups say the same
// thing: setup_app_text_test.go keeps them equal and makes sure every code the
// app translates (setup.check.note.<code>, setup.check.run.<role>,
// setup.error.<code>) is here too. "terminal." keys are the terminal's own.
var setupTexts = map[string][2]string{ // key → {en, ar}
	"login.username":          {"Username", "اسم المستخدم"},
	"login.password":          {"Password", "كلمة المرور"},
	"setup.machine.linux":     {"this computer", "هذا الحاسوب"},
	"setup.check.title":       {"What will run", "ما الذي سيعمل"},
	"setup.check.dataDir":     {"Data folder", "مجلد البيانات"},
	"setup.admin.title":       {"Your admin account", "حساب المدير"},
	"setup.admin.intro":       {"You sign in to Maktaba with this account. Keep the password somewhere safe.", "تسجّل الدخول إلى مكتبة بهذا الحساب. احفظ كلمة المرور في مكان آمن."},
	"setup.admin.confirm":     {"Password again", "كلمة المرور مرة أخرى"},
	"setup.admin.mismatch":    {"The two passwords are not the same.", "كلمتا المرور غير متطابقتين."},
	"setup.libraries.title":   {"Your library folders", "مجلدات مكتبتك"},
	"setup.libraries.intro":   {"Which folders hold your lectures? Maktaba reads them; it never moves or changes your files.", "أي المجلدات تحوي محاضراتك؟ تقرؤها مكتبة ولا تنقل ملفاتك ولا تغيّرها."},
	"setup.libraries.mapNote": {"If other machines see the same files under another path, write one \"their path => the path here\" per line.", "إذا رأت أجهزة أخرى الملفات نفسها بمسار آخر، فاكتب في كل سطر \"مسارها => المسار هنا\"."},
	"setup.libraries.mapBad":  {"Write each line as \"their path => the path here\".", "اكتب كل سطر هكذا: \"مسارها => المسار هنا\"."},
	"setup.models.title":      {"Transcription models", "نماذج التفريغ"},
	"setup.models.intro":      {"Maktaba transcribes on {machine}. It needs the speech models (a few GB).", "تفرّغ مكتبة المحاضرات على {machine}، وتحتاج إلى نماذج الكلام (بضعة غيغابايتات)."},
	"setup.models.hfNote":     {"The second-listen model (Cohere) needs a Hugging Face account: accept the model's terms, then make a read token.", "نموذج الاستماع الثاني (Cohere) يحتاج إلى حساب في Hugging Face: وافق على شروط النموذج ثم أنشئ رمز قراءة."},
	"setup.models.nasPath":    {"Folder with the models", "المجلد الذي فيه النماذج"},
	"setup.plex.title":        {"Plex (optional)", "Plex (اختياري)"},
	"setup.plex.intro":        {"Sign in to Plex to bring in your Plex libraries and watch history. You can do this later in Settings.", "سجّل الدخول إلى Plex لجلب مكتبات Plex وسجل المشاهدة. يمكنك فعل ذلك لاحقًا من الإعدادات."},
	"setup.plex.enterCode":    {"Enter this code at {url}:", "أدخل هذا الرمز في {url}:"},
	"setup.plex.waiting":      {"Waiting for Plex…", "بانتظار Plex…"},
	"setup.install.title":     {"Getting Maktaba ready", "تجهيز مكتبة"},
	"setup.install.venv":      {"Installing the transcription and search tools. This downloads about 2 GB and takes a few minutes.", "جارٍ تثبيت أدوات التفريغ والبحث. يُنزَّل نحو 2 غيغابايت ويستغرق بضع دقائق."},
	"setup.install.models":    {"Downloading the speech models…", "جارٍ تنزيل نماذج الكلام…"},
	"setup.install.llm":       {"Downloading the language model that checks tags and finds metadata (about 5 GB). If this stops, Maktaba finishes the download in the background.", "جارٍ تنزيل نموذج اللغة الذي يراجع الوسوم ويبحث عن البيانات الوصفية (نحو 5 غيغابايت). إن توقّف التنزيل أكملته مكتبة في الخلفية."},
	"setup.install.finishing": {"Creating the library database…", "جارٍ إنشاء قاعدة بيانات المكتبة…"},

	// every code a step or check reports (the screens' codedText)
	"setup.error.hf_token_invalid":          {"Hugging Face did not accept the token.", "لم يقبل Hugging Face الرمز."},
	"setup.error.hf_gated":                  {"Accept the model's terms on huggingface.co with this account first.", "وافق أولًا على شروط النموذج في huggingface.co بهذا الحساب."},
	"setup.error.nas_missing":               {"{path} has no copy of the models (the home publishes them in maktaba/models on the share).", "لا توجد نسخة من النماذج في {path} (ينشرها المنزل في maktaba/models على المجلد المشترك)."},
	"setup.error.disk_full":                 {"The disk is full.", "القرص ممتلئ."},
	"setup.error.pin_expired":               {"The Plex code expired. Try again.", "انتهت صلاحية رمز Plex. حاول مرة أخرى."},
	"setup.error.venv_failed":               {"Installing the tools failed.", "فشل تثبيت الأدوات."},
	"setup.error.server_missing":            {"This copy of Maktaba cannot run the server.", "هذه النسخة من مكتبة لا تستطيع تشغيل الخادم."},
	"setup.error.bad_path_map":              {"Line {line} of the path map needs both paths.", "السطر {line} من تحويل المسارات يحتاج إلى المسارين."},
	"setup.error.network":                   {"Maktaba could not reach the internet.", "لم تستطع مكتبة الوصول إلى الإنترنت."},
	"setup.error.venv_missing":              {"The transcription tools are not installed yet.", "أدوات التفريغ لم تُثبَّت بعد."},
	"setup.error.download_failed":           {"Downloading the models failed.", "فشل تنزيل النماذج."},
	"setup.error.interrupted":               {"It stopped part way; it goes on from there next time.", "توقف في منتصف الطريق، ويكمل من حيث توقف في المرة القادمة."},
	"setup.error.migrate_failed":            {"Preparing the database failed.", "فشل تجهيز قاعدة البيانات."},
	"setup.error.admin_failed":              {"The admin account could not be created.", "تعذّر إنشاء حساب المدير."},
	"setup.error.admin_missing":             {"Create the admin account first.", "أنشئ حساب المدير أولًا."},
	"setup.error.database_failed":           {"The library database could not be created.", "تعذّر إنشاء قاعدة بيانات المكتبة."},
	"setup.error.database_unsupported":      {"This install's database cannot be set up here.", "لا يمكن إعداد قاعدة بيانات هذا التثبيت هنا."},
	"setup.check.run.postgres":              {"The library database (private to Maktaba)", "قاعدة بيانات المكتبة (خاصة بمكتبة)"},
	"setup.check.run.api":                   {"The server the app and your devices talk to", "الخادم الذي يتصل به التطبيق وأجهزتك"},
	"setup.check.run.streaming":             {"Playback and Live TV", "التشغيل والبث التلفزيوني المباشر"},
	"setup.check.run.pipeline":              {"Indexing, transcription and search", "الفهرسة والتفريغ والبحث"},
	"setup.check.run.pipelineOne":           {"Indexing, transcription (1 worker) and search", "الفهرسة والتفريغ (عامل واحد) والبحث"},
	"setup.check.run.pipelineWorkers":       {"Indexing, transcription ({workers} workers) and search", "الفهرسة والتفريغ ({workers} عمّال) والبحث"},
	"setup.check.run.onGpu":                 {", transcribing on {gpu}", "، والتفريغ على {gpu}"},
	"setup.check.note.platform_intel_mac":   {"This Mac has an Intel processor: the Maktaba app runs the server on Apple Silicon only.", "معالج هذا الماك من Intel: تطبيق مكتبة يشغّل الخادم على Apple Silicon فقط."},
	"setup.check.note.platform_unsupported": {"Maktaba does not run its server on {os}/{arch}.", "لا تشغّل مكتبة خادمها على {os}/{arch}."},
	"setup.check.note.disk_low":             {"Only {free_bytes} free on the disk of {dir}: Maktaba needs {need_bytes} to start.", "المساحة الحرة على قرص {dir} هي {free_bytes} فقط، وتحتاج مكتبة إلى {need_bytes} لتبدأ."},
	"setup.check.note.disk_tight":           {"Less than {comfortable_bytes} free: the database, the models and the caches grow with the library.", "المساحة الحرة أقل من {comfortable_bytes}: قاعدة البيانات والنماذج والذاكرة المؤقتة تكبر مع المكتبة."},
	"setup.check.note.ram_low":              {"The memory of {machine} is {have_bytes}: Maktaba needs at least {need_bytes}.", "ذاكرة {machine} {have_bytes}، وتحتاج مكتبة إلى {need_bytes} على الأقل."},
	"setup.check.note.ram_tight":            {"Less than {need_bytes} of memory: indexing and transcription will be slow.", "الذاكرة أقل من {need_bytes}: ستكون الفهرسة والتفريغ بطيئين."},
	"setup.check.note.transcribe_cpu":       {"No NVIDIA graphics card: {machine} transcribes on its processor, many times slower than on a graphics card.", "لا توجد بطاقة رسوميات من NVIDIA: يفرّغ {machine} على معالجه، وهذا أبطأ بأضعاف من بطاقة الرسوميات."},
	"setup.check.note.glibc_missing":        {"This Linux has no GNU C library (glibc), which the bundled Postgres, ffmpeg and Python need (Alpine and other musl systems cannot run them).", "لا توجد في نظام لينكس هذا مكتبة GNU C (glibc) التي تحتاجها نسخ Postgres وffmpeg وPython المرفقة (لا تستطيع Alpine وغيرها من أنظمة musl تشغيلها)."},
	"setup.check.note.glibc_old":            {"This Linux has glibc {have}; Maktaba needs {need} or newer (Ubuntu 22.04, Debian 12, Fedora 36, RHEL 10 or later).", "في نظام لينكس هذا glibc {have}، وتحتاج مكتبة إلى {need} أو أحدث (Ubuntu 22.04 أو Debian 12 أو Fedora 36 أو RHEL 10 أو أحدث)."},
	"setup.check.note.user_manager_missing": {"The server runs as your systemd user service, which {machine} cannot start: {reason}.", "يعمل الخادم كخدمة systemd خاصة بك، ولا يستطيع {machine} تشغيلها: {reason}."},
	"setup.check.note.service_system_wide":  {"A system-wide Maktaba service is installed ({unit}): it would be a second server on the same ports. Its administrator removes it first (sudo apt remove maktaba-server, or sudo systemctl disable --now maktaba-server).", "خدمة مكتبة مثبّتة على مستوى النظام ({unit}): ستكون خادمًا ثانيًا على المنافذ نفسها. يزيلها مسؤول النظام أولًا (sudo apt remove maktaba-server أو sudo systemctl disable --now maktaba-server)."},
	"setup.check.note.service_taken":        {"Another installation of Maktaba already owns the service {unit} (it runs {runs}). Remove it first (maktaba-server uninstall from that installation).", "تثبيت آخر لمكتبة يملك الخدمة {unit} (يشغّل {runs}). أزِله أولًا (maktaba-server uninstall من ذلك التثبيت)."},
	"setup.check.note.linger_off":           {"Your services start only when you log in. Setup asks to let the server start at boot (linger); that may need an administrator's password.", "لا تبدأ خدماتك إلا عند تسجيل دخولك. يطلب الإعداد السماح للخادم بالبدء عند الإقلاع (linger)، وقد يحتاج ذلك إلى كلمة مرور المسؤول."},
	"setup.error.username_length":           {"A username is 1 to {max} characters.", "اسم المستخدم من حرف واحد إلى {max} حرفًا."},
	"setup.error.username_space":            {"A username cannot start or end with a space.", "لا يجوز أن يبدأ اسم المستخدم أو ينتهي بمسافة."},
	"setup.error.username_control":          {"A username cannot hold control characters.", "لا يجوز أن يحوي اسم المستخدم محارف تحكّم."},
	"setup.error.password_short":            {"Use at least {min} characters.", "استخدم {min} أحرف على الأقل."},
	"setup.error.password_long":             {"Use at most {max} characters.", "استخدم {max} حرفًا على الأكثر."},
	"setup.error.password_newline":          {"A password cannot hold a line break.", "لا يجوز أن تحوي كلمة المرور سطرًا جديدًا."},
	"setup.error.root_missing":              {"{path} is not a folder on {machine}.", "{path} ليس مجلدًا على {machine}."},
	"setup.error.root_unreadable":           {"Maktaba cannot read {path}.", "لا تستطيع مكتبة قراءة {path}."},
	"setup.error.root_relative":             {"Write the whole path of {path}, from the drive or from /.", "اكتب المسار الكامل لـ {path}، من القرص أو من /."},
	"setup.error.roots_none":                {"Give at least one library folder.", "أدخل مجلد مكتبة واحدًا على الأقل."},
	"setup.error.path_map_relative":         {"Line {line} of the path map: write the whole path of {path}.", "السطر {line} من تحويل المسارات: اكتب المسار الكامل لـ {path}."},
	"setup.error.hf_token_empty":            {"Enter a Hugging Face read token.", "أدخل رمز قراءة من Hugging Face."},
	"setup.error.nas_path_empty":            {"Give the folder on the NAS that holds the models.", "أدخل المجلد الذي فيه النماذج على الـNAS."},

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
