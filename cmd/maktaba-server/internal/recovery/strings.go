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
	"recovery.held":                {"The update to {failed} stopped: it could not change the library database, and its own Down scripts could not take it back ({why}). Nothing was put back by itself. Maktaba stays stopped until you choose one of these:", "توقف التحديث إلى {failed}: لم يتمكن من تعديل قاعدة بيانات المكتبة، ولم تستطع نصوص التراجع الخاصة به إرجاعها ({why}). لم يُرجَع شيء تلقائيًا. وتبقى مكتبة متوقفة حتى تختار أحد هذين:"},
	"recovery.held.restore":        {"Put back the backup taken before the update ({backup}): {from} runs again, and the changes made since that backup are lost", "أعِد النسخة الاحتياطية المأخوذة قبل التحديث ({backup}): فيعمل {from} مجددًا، وتضيع التغييرات التي جرت بعد تلك النسخة"},
	"recovery.held.newer":          {"Or install a release newer than {failed} that fixes this update: it goes on from where the update stopped.", "أو ثبّت إصدارًا أحدث من {failed} يصلح هذا التحديث: فيُكمل من حيث توقف."},
	"recovery.stuck.running":       {"The backup {backup} is being put back now.", "تُعاد النسخة الاحتياطية {backup} الآن."},
	"recovery.stuck.untouched":     {"The backup {backup} could not be put back ({why}). Nothing was changed. Maktaba stays stopped until you choose one of these:", "تعذّرت إعادة النسخة الاحتياطية {backup} ({why}). لم يتغيّر شيء. وتبقى مكتبة متوقفة حتى تختار أحد هذه:"},
	"recovery.stuck.noCopy":        {"The backup {backup} was not put back: no copy of the library as it is now could be kept ({why}). Nothing was changed. Free some space on the backups' disk, or delete backups you no longer need, and try again; or choose one of these:", "لم تُعَد النسخة الاحتياطية {backup}: تعذّر الاحتفاظ بنسخة من المكتبة كما هي الآن ({why}). لم يتغيّر شيء. حرّر مساحة على قرص النسخ الاحتياطية، أو احذف نسخًا لم تعد تحتاجها، ثم أعِد المحاولة؛ أو اختر أحد هذه:"},
	"recovery.stuck.withoutCopy":   {"Put it back without keeping a copy: the library as it is now is lost for good", "أعِدها دون الاحتفاظ بنسخة: تضيع المكتبة كما هي الآن نهائيًا"},
	"recovery.restore.noCopy":      {"There is no room to keep a copy of the library as it is now. {reason} Restore without keeping a copy? The library as it is now is then lost for good.", "لا توجد مساحة للاحتفاظ بنسخة من المكتبة كما هي الآن. {reason} أتريد الاستعادة دون الاحتفاظ بنسخة؟ تضيع عندها المكتبة كما هي الآن نهائيًا."},
	"recovery.stuck.replaced":      {"The backup {backup} could not be put back whole ({why}). The library database may hold a part of it, so only a backup puts it right. Maktaba stays stopped until you choose one of these:", "تعذّرت إعادة النسخة الاحتياطية {backup} كاملة ({why}). قد تحوي قاعدة بيانات المكتبة جزءًا منها، فلا يصلحها إلا نسخة احتياطية. وتبقى مكتبة متوقفة حتى تختار أحد هذين:"},
	"recovery.stuck.gone":          {"The backup {backup} could not be put back ({why}). The library database is gone, so only a backup brings it back. Maktaba stays stopped until you choose one of these:", "تعذّرت إعادة النسخة الاحتياطية {backup} ({why}). لم تعد قاعدة بيانات المكتبة موجودة، فلا يعيدها إلا نسخة احتياطية. وتبقى مكتبة متوقفة حتى تختار أحد هذين:"},
	"recovery.stuck.retry":         {"Try the same backup again ({backup})", "أعِد المحاولة بالنسخة نفسها ({backup})"},
	"recovery.stuck.other":         {"Put another backup back", "أعِد نسخة احتياطية أخرى"},
	"recovery.stuck.giveUp":        {"Give this restore up: {release} runs on the library database as it was before it", "تخلَّ عن هذه الإعادة: يعمل {release} على قاعدة بيانات المكتبة كما كانت قبلها"},
	"recovery.stuck.giveUpHeld":    {"Give this restore up: the update that stopped waits for you again", "تخلَّ عن هذه الإعادة: ينتظر التحديث الذي توقف اختيارك من جديد"},
	"recovery.stuck.stop":          {"Stop it and give this restore up", "أوقفها وتخلَّ عن هذه الإعادة"},
	"recovery.stuck.newer":         {"Or install a newer release: it goes on from the library database as it was before the restore.", "أو ثبّت إصدارًا أحدث: فيُكمل من قاعدة بيانات المكتبة كما كانت قبل الإعادة."},
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
	"status.group.plugin_runners":  {"Plugin runners", "مشغّلات الإضافات"},
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
