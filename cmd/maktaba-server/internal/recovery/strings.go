package recovery

import "strings"

// Words are the console's strings, English and Arabic (story 53.04 §5, 53.05 §4). Its
// tests fail on a key that lacks either.
var Words = map[string][2]string{
	"recovery.title":               {"Maktaba recovery", "استعادة مكتبة"},
	"recovery.intro":               {"Part of Maktaba isn't working. You can see what happened and fix it here.", "جزء من مكتبة لا يعمل. يمكنك هنا معرفة ما حدث وإصلاحه."},
	"recovery.allWell":             {"Every part of Maktaba is working.", "كل أجزاء مكتبة تعمل."},
	"recovery.signin.mac":          {"Open this page from the Maktaba menu on the Mac that runs the home.", "افتح هذه الصفحة من قائمة مكتبة على جهاز الماك الذي يشغّل الجهاز الرئيسي."},
	"recovery.signin.denied":       {"Only an admin of this home can open the recovery page.", "يمكن لمشرفي هذا الجهاز الرئيسي فقط فتح صفحة الاستعادة."},
	"recovery.signin.later":        {"Or, once the home answers again, sign in as an admin and come back to this page.", "أو سجّل الدخول مشرفًا حين يجيب الجهاز الرئيسي من جديد، ثم عُد إلى هذه الصفحة."},
	"recovery.signin.locked":       {"Too many tries. Wait five minutes and try again.", "محاولات كثيرة. انتظر خمس دقائق ثم حاول مجددًا."},
	"recovery.action.restart":      {"Restart", "إعادة التشغيل"},
	"recovery.action.restartAll":   {"Restart everything", "إعادة تشغيل الكل"},
	"recovery.action.safeMode":     {"Start in safe mode", "التشغيل في الوضع الآمن"},
	"recovery.action.leaveSafe":    {"Leave safe mode", "الخروج من الوضع الآمن"},
	"recovery.action.rollback":     {"Go back to version {version}", "الرجوع إلى الإصدار {version}"},
	"recovery.action.restore":      {"Restore a backup", "استعادة نسخة احتياطية"},
	"recovery.action.diagnostics":  {"Download diagnostics", "تنزيل ملف التشخيص"},
	"recovery.action.confirm":      {"Are you sure?", "هل أنت متأكد؟"},
	"recovery.action.confirmAgain": {"This replaces what is there now. Are you sure?", "سيحلّ هذا محلّ ما هو موجود الآن. هل أنت متأكد؟"},
	"recovery.action.done":         {"Done.", "تم."},
	"recovery.action.failed":       {"That did not work: {reason}", "لم ينجح ذلك: {reason}"},
	"recovery.components":          {"Parts", "الأجزاء"},
	"recovery.logs.title":          {"Recent problems", "المشكلات الأخيرة"},
	"recovery.logs.more":           {"Load 15 more minutes", "تحميل 15 دقيقة أخرى"},
	"recovery.logs.filter":         {"Search the loaded lines", "ابحث في الأسطر المحمّلة"},
	"recovery.logs.none":           {"No warnings or errors in this window.", "لا تحذيرات ولا أخطاء في هذه المدة."},
	"recovery.disk":                {"Disk: {free} free, logs {logs}", "القرص: {free} متاحة، والسجلات {logs}"},
	"recovery.restarts":            {"{n} restarts", "{n} مرات إعادة تشغيل"},
	"recovery.lastExit":            {"last stopped {at} (code {code})", "توقّف آخر مرة {at} (الرمز {code})"},
	"recovery.backups.none":        {"No backups were found on the data disk.", "لم تُعثر على نسخ احتياطية في قرص البيانات."},
	"restarting.openRecovery":      {"Open the recovery page", "افتح صفحة الاستعادة"},
	"safe.strip":                   {"Maktaba is in safe mode. Playback, processing and Live TV are off until it restarts.", "مكتبة في الوضع الآمن. التشغيل والمعالجة والبث المباشر متوقفة حتى إعادة التشغيل."},
	"safe.stay":                    {"Stay in safe mode", "البقاء في الوضع الآمن"},
	"repair.keepsStopping":         {"{component} keeps stopping", "{component} يتوقف باستمرار"},
	"status.group.database":        {"Database", "قاعدة البيانات"},
	"status.group.api":             {"Maktaba service", "خدمة مكتبة"},
	"status.group.streaming":       {"Playback", "التشغيل"},
	"status.group.processing":      {"Processing", "المعالجة"},
	"status.group.search":          {"Search", "البحث"},
	"status.group.logs":            {"Log search", "البحث في السجلات"},
	"status.state.ok":              {"working", "يعمل"},
	"status.state.starting":        {"starting", "يبدأ التشغيل"},
	"status.state.degraded":        {"having trouble", "يواجه مشكلة"},
	"status.state.restarting":      {"restarting", "يُعاد تشغيله"},
	"status.state.down":            {"stopped", "متوقف"},
	"status.state.off":             {"off", "متوقف عمدًا"},
}

// Lang picks the page's language from Accept-Language: ar when the browser prefers it
// over English, else en.
func Lang(accept string) string {
	for _, part := range strings.Split(accept, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "ar"):
			return "ar"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return "en"
}

// T is a key's words in lang, with {name} replaced by vars.
func T(lang, key string, vars ...string) string {
	w, ok := Words[key]
	if !ok {
		return key
	}
	s := w[0]
	if lang == "ar" && w[1] != "" {
		s = w[1]
	}
	for i := 0; i+1 < len(vars); i += 2 {
		s = strings.ReplaceAll(s, "{"+vars[i]+"}", vars[i+1])
	}
	return s
}
