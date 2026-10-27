package com.hamzalabs.maktaba.desktop

import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.unit.LayoutDirection
import com.hamzalabs.maktaba.core.api.ErrorKind

/** The player's languages: English, and Arabic laid out right to left. */
enum class Lang(val code: String, val direction: LayoutDirection) {
    En("en", LayoutDirection.Ltr),
    Ar("ar", LayoutDirection.Rtl);

    companion object {
        /** A stored choice, else the system's language when it is Arabic, else English. */
        fun of(code: String?): Lang = entries.firstOrNull { it.code == code }
            ?: if (java.util.Locale.getDefault().language == "ar") Ar else En
    }
}

/** The words of one language: `t["key"]`, `{0}`, `{1}` filled from the arguments. */
class Texts(val lang: Lang) {
    private val table = if (lang == Lang.Ar) Strings.AR else Strings.EN

    operator fun get(key: String, vararg args: Any?): String {
        var s = table[key] ?: Strings.EN[key] ?: key
        args.forEachIndexed { i, a -> s = s.replace("{$i}", a?.toString() ?: "") }
        return s
    }
}

val LocalTexts = staticCompositionLocalOf { Texts(Lang.En) }

/** What went wrong with the home, in a sentence (the Android apps' `ErrorKind.sentence`). */
fun Texts.sentence(kind: ErrorKind): String = this["error." + kind.name.replaceFirstChar { it.lowercase() }]

object Strings {
    val EN: Map<String, String> = mapOf(
        "app.title" to "Maktaba Player",
        "common.cancel" to "Cancel",
        "common.close" to "Close",
        "common.save" to "Save",
        "common.retry" to "Try again",
        "common.error" to "Something went wrong.",
        "common.loading" to "Loading…",

        "picker.title" to "Choose a home",
        "picker.yours" to "Your homes",
        "picker.network" to "On this network",
        "picker.searching" to "Looking for homes on this network…",
        "picker.noneFound" to "No home announces itself on this network. Type its address below.",
        "picker.add" to "Add a home",
        "picker.address" to "Address or link",
        "picker.addressHint" to "A home's address (192.168.1.5:8088) or a maktaba://connect link",
        "picker.connect" to "Connect",
        "picker.connecting" to "Connecting…",
        "picker.notAHome" to "No Maktaba home answered at {0}.",
        "picker.rename" to "Rename",
        "picker.remove" to "Remove",
        "picker.renameTitle" to "Rename this home",
        "picker.renameHint" to "Shown on this computer only. Leave it empty for the home's own name.",

        "link.title" to "Open this home?",
        "link.body" to "A link asks to open {0} ({1}). Open it only if you trust it.",
        "link.open" to "Open",

        "signin.title" to "Sign in to {0}",
        "signin.withPassword" to "Password",
        "signin.withCode" to "With a code",
        "signin.username" to "Username",
        "signin.password" to "Password",
        "signin.submit" to "Sign in",
        "signin.failed" to "The username or the password is wrong.",
        "signin.codeHint" to "On a device signed in to this home, open Sign in a device (or scan this QR code) and enter the code:",
        "signin.codeRenew" to "A new code appears when this one runs out.",
        "signin.codeUnsupported" to "This home has no sign-in by code. Use your password.",
        "signin.restoreFailed" to "Your kept sign-in could not be checked: {0}",
        "signin.otherHome" to "Choose another home",

        "nav.home" to "Home",
        "nav.libraries" to "Libraries",
        "nav.all" to "All videos",
        "nav.search" to "Search",
        "lib.empty" to "Nothing here yet.",
        "lib.more" to "Load more",
        "lib.searchHint" to "Search titles",

        "menu.account" to "Account",
        "menu.switchHome" to "Switch home",
        "menu.approve" to "Approve a sign-in",
        "menu.bigScreen" to "Big screen",
        "menu.leaveBigScreen" to "Leave big screen",
        "menu.startBig" to "Start in big screen",
        "menu.language" to "Language",
        "menu.updates" to "Check for updates",
        "menu.help" to "Help",
        "menu.signOut" to "Sign out",
        "menu.signedInAs" to "Signed in as {0} on {1}",

        "watch.opening" to "Opening the video…",
        "watch.failed" to "The video could not play: {0}",
        "watch.play" to "Play",
        "watch.pause" to "Pause",
        "watch.back" to "Back 10 seconds",
        "watch.forward" to "Forward 10 seconds",
        "watch.fullscreen" to "Full screen",
        "watch.close" to "Close the video",

        "approve.title" to "Approve a sign-in",
        "approve.hint" to "Type the code the other device shows: eight letters and digits.",
        "approve.code" to "Code",
        "approve.continue" to "Continue",
        "approve.asking" to "{0} on {1} ({2}) asks to sign in as you.",
        "approve.label" to "Its name: {0}",
        "approve.expires" to "The code expires at {0}.",
        "approve.approve" to "Approve",
        "approve.done" to "The device is signed in as you.",
        "approve.notWaiting" to "This code is not waiting for a sign-in. Ask the device for a new code.",
        "approve.used" to "This code was already used.",
        "approve.checkCode" to "Check the code: eight letters and digits.",
        "approve.cloud" to "A sign-in through Maktaba Cloud cannot approve a device. Approve it on the home's own address.",
        "approve.older" to "This home cannot show what is asking. Check that the code is the one on your own device before you approve it.",

        "update.title" to "Updates",
        "update.checking" to "Checking for an update…",
        "update.available" to "Maktaba Player {0} is available (you have {1}).",
        "update.install" to "Download and install",
        "update.downloading" to "Downloading and checking…",
        "update.installing" to "The installer is open. Maktaba Player closes to update.",
        "update.linux" to "The package is downloaded and checked: {0}. Install it with your software installer.",
        "update.upToDate" to "You have the latest version ({0}).",
        "update.failed" to "No update was installed: {0}",
        "update.unsigned" to "This build has no update key: update it by hand.",

        "big.hint" to "Arrow keys move, Enter opens, Escape goes back.",

        "error.notFound" to "We couldn't find what you were looking for.",
        "error.notReady" to "Your home is still preparing this video. Try again in a moment.",
        "error.signedOut" to "Your sign-in has ended. Sign in again to keep watching.",
        "error.noAccess" to "Your account doesn't have access to this.",
        "error.unreachable" to "Couldn't reach your home. Check that it is on and on the network.",
        "error.busy" to "Your home is busy right now. Try again in a moment.",
        "error.server" to "Something went wrong on your home. Try again in a moment.",
    )

    val AR: Map<String, String> = mapOf(
        "app.title" to "مشغّل مكتبة",
        "common.cancel" to "إلغاء",
        "common.close" to "إغلاق",
        "common.save" to "حفظ",
        "common.retry" to "حاول مرة أخرى",
        "common.error" to "حدث خطأ ما.",
        "common.loading" to "جارٍ التحميل…",

        "picker.title" to "اختر منزلًا",
        "picker.yours" to "منازلك",
        "picker.network" to "على هذه الشبكة",
        "picker.searching" to "جارٍ البحث عن منازل على هذه الشبكة…",
        "picker.noneFound" to "لا يعلن أي منزل عن نفسه على هذه الشبكة. اكتب عنوانه أدناه.",
        "picker.add" to "إضافة منزل",
        "picker.address" to "العنوان أو الرابط",
        "picker.addressHint" to "عنوان المنزل (192.168.1.5:8088) أو رابط maktaba://connect",
        "picker.connect" to "اتصال",
        "picker.connecting" to "جارٍ الاتصال…",
        "picker.notAHome" to "لم يُجب أي منزل لمكتبة على {0}.",
        "picker.rename" to "إعادة تسمية",
        "picker.remove" to "إزالة",
        "picker.renameTitle" to "إعادة تسمية هذا المنزل",
        "picker.renameHint" to "يظهر على هذا الحاسوب فقط. اتركه فارغًا لاسم المنزل نفسه.",

        "link.title" to "فتح هذا المنزل؟",
        "link.body" to "يطلب رابط فتح {0} ({1}). لا تفتحه إلا إذا كنت تثق به.",
        "link.open" to "فتح",

        "signin.title" to "تسجيل الدخول إلى {0}",
        "signin.withPassword" to "كلمة المرور",
        "signin.withCode" to "برمز",
        "signin.username" to "اسم المستخدم",
        "signin.password" to "كلمة المرور",
        "signin.submit" to "تسجيل الدخول",
        "signin.failed" to "اسم المستخدم أو كلمة المرور غير صحيحة.",
        "signin.codeHint" to "على جهاز مسجّل الدخول إلى هذا المنزل، افتح «تسجيل دخول جهاز» (أو امسح رمز QR هذا) وأدخل الرمز:",
        "signin.codeRenew" to "يظهر رمز جديد عند انتهاء صلاحية هذا الرمز.",
        "signin.codeUnsupported" to "لا يتيح هذا المنزل تسجيل الدخول برمز. استخدم كلمة المرور.",
        "signin.restoreFailed" to "تعذّر التحقق من تسجيل دخولك المحفوظ: {0}",
        "signin.otherHome" to "اختر منزلًا آخر",

        "nav.home" to "الرئيسية",
        "nav.libraries" to "المكتبات",
        "nav.all" to "كل المقاطع",
        "nav.search" to "بحث",
        "lib.empty" to "لا شيء هنا بعد.",
        "lib.more" to "تحميل المزيد",
        "lib.searchHint" to "ابحث في العناوين",

        "menu.account" to "الحساب",
        "menu.switchHome" to "تبديل المنزل",
        "menu.approve" to "الموافقة على تسجيل دخول",
        "menu.bigScreen" to "الشاشة الكبيرة",
        "menu.leaveBigScreen" to "مغادرة الشاشة الكبيرة",
        "menu.startBig" to "البدء في الشاشة الكبيرة",
        "menu.language" to "اللغة",
        "menu.updates" to "البحث عن تحديثات",
        "menu.help" to "المساعدة",
        "menu.signOut" to "تسجيل الخروج",
        "menu.signedInAs" to "مسجّل الدخول باسم {0} في {1}",

        "watch.opening" to "جارٍ فتح المقطع…",
        "watch.failed" to "تعذّر تشغيل المقطع: {0}",
        "watch.play" to "تشغيل",
        "watch.pause" to "إيقاف مؤقت",
        "watch.back" to "رجوع 10 ثوانٍ",
        "watch.forward" to "تقديم 10 ثوانٍ",
        "watch.fullscreen" to "ملء الشاشة",
        "watch.close" to "إغلاق المقطع",

        "approve.title" to "الموافقة على تسجيل دخول",
        "approve.hint" to "اكتب الرمز الذي يعرضه الجهاز الآخر: ثمانية أحرف وأرقام.",
        "approve.code" to "الرمز",
        "approve.continue" to "متابعة",
        "approve.asking" to "يطلب {0} على {1} ({2}) تسجيل الدخول باسمك.",
        "approve.label" to "اسمه: {0}",
        "approve.expires" to "تنتهي صلاحية الرمز في {0}.",
        "approve.approve" to "موافقة",
        "approve.done" to "سُجّل دخول الجهاز باسمك.",
        "approve.notWaiting" to "هذا الرمز لا ينتظر تسجيل دخول. اطلب رمزًا جديدًا من الجهاز.",
        "approve.used" to "استُخدم هذا الرمز من قبل.",
        "approve.checkCode" to "تحقّق من الرمز: ثمانية أحرف وأرقام.",
        "approve.cloud" to "لا يمكن لتسجيل دخول عبر سحابة مكتبة الموافقة على جهاز. وافق عليه من عنوان المنزل نفسه.",
        "approve.older" to "لا يستطيع هذا المنزل أن يعرض ما يطلب. تأكّد أن الرمز هو الظاهر على جهازك قبل الموافقة.",

        "update.title" to "التحديثات",
        "update.checking" to "جارٍ البحث عن تحديث…",
        "update.available" to "يتوفر مشغّل مكتبة {0} (لديك {1}).",
        "update.install" to "تنزيل وتثبيت",
        "update.downloading" to "جارٍ التنزيل والتحقق…",
        "update.installing" to "فُتح المثبّت. يُغلق مشغّل مكتبة ليتحدّث.",
        "update.linux" to "نُزّلت الحزمة وتُحقّق منها: {0}. ثبّتها بمثبّت البرامج لديك.",
        "update.upToDate" to "لديك أحدث إصدار ({0}).",
        "update.failed" to "لم يُثبَّت أي تحديث: {0}",
        "update.unsigned" to "لا يحمل هذا الإصدار مفتاح تحديث: حدّثه يدويًا.",

        "big.hint" to "الأسهم للتنقل، وEnter للفتح، وEscape للرجوع.",

        "error.notFound" to "لم نعثر على ما تبحث عنه.",
        "error.notReady" to "ما زال المنزل يجهّز هذا المقطع. حاول مجددًا بعد قليل.",
        "error.signedOut" to "انتهت جلسة تسجيل الدخول. سجّل الدخول مجددًا لمتابعة المشاهدة.",
        "error.noAccess" to "لا يملك حسابك صلاحية الوصول إلى هذا.",
        "error.unreachable" to "تعذّر الوصول إلى منزلك. تأكد من أنه يعمل ومتصل بالشبكة.",
        "error.busy" to "منزلك مشغول الآن. حاول مجددًا بعد قليل.",
        "error.server" to "حدث خطأ في منزلك. حاول مجددًا بعد قليل.",
    )
}
