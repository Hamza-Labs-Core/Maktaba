package com.hamzalabs.maktaba.desktop

import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.unit.LayoutDirection

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
        "signin.restoreFailed" to "The home did not answer: {0}",
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

        "cloud.title" to "Through Maktaba Cloud",
        "cloud.intro" to "Away from home, or a home on another network: sign in with your Maktaba Cloud account and open a home linked to it. Each home decides what you can do there.",
        "cloud.email" to "Email",
        "cloud.signIn" to "Sign in to Maktaba Cloud",
        "cloud.advanced" to "Advanced",
        "cloud.address" to "Maktaba Cloud address",
        "cloud.addressHint" to "Change it only to reach a test or your own Maktaba Cloud. Empty: api.maktaba.app.",
        "cloud.addressInvalid" to "That is not an address.",
        "cloud.wrong" to "The email or the password is wrong.",
        "cloud.tooMany" to "Too many tries. Wait a minute and try again.",
        "cloud.signedInAs" to "Signed in to Maktaba Cloud as {0}",
        "cloud.yourHomes" to "Your homes",
        "cloud.noHomes" to "No home is linked to this Maktaba Cloud account yet.",
        "cloud.online" to "Online",
        "cloud.offline" to "Offline",
        "cloud.notLinked" to "Not linked to your account",
        "cloud.howToLink" to "To open a home that is not linked: open the home in a browser, sign in there, and link your Maktaba Cloud account on its Account page (Linked accounts). Owning a home in Maktaba Cloud is not an account in it.",
        "cloud.refresh" to "Refresh",
        "cloud.signOut" to "Sign out of Maktaba Cloud",
        "cloud.unreachable" to "The home did not answer, at its own address or through the cloud.",
        "cloud.refused" to "The home refused the sign-in: the link may have been removed. Link again on the home's Account page.",
        "cloud.locked" to "Your account on that home is locked. Ask its admin.",
        "cloud.changePassword" to "That home wants you to change your password first: sign in there with it.",
        "cloud.signInHere" to "Sign in through Maktaba Cloud",
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
        "signin.restoreFailed" to "لم يُجب المنزل: {0}",
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

        "cloud.title" to "عبر سحابة مكتبة",
        "cloud.intro" to "بعيدًا عن المنزل، أو منزل على شبكة أخرى: سجّل الدخول بحساب سحابة مكتبة وافتح منزلًا مرتبطًا به. وكل منزل يقرّر ما يمكنك فعله فيه.",
        "cloud.email" to "البريد الإلكتروني",
        "cloud.signIn" to "تسجيل الدخول إلى سحابة مكتبة",
        "cloud.advanced" to "متقدم",
        "cloud.address" to "عنوان سحابة مكتبة",
        "cloud.addressHint" to "لا تغيّره إلا للوصول إلى سحابة مكتبة تجريبية أو خاصة بك. فارغ: api.maktaba.app.",
        "cloud.addressInvalid" to "هذا ليس عنوانًا.",
        "cloud.wrong" to "البريد الإلكتروني أو كلمة المرور غير صحيحة.",
        "cloud.tooMany" to "محاولات كثيرة. انتظر دقيقة ثم حاول مرة أخرى.",
        "cloud.signedInAs" to "مسجّل الدخول إلى سحابة مكتبة باسم {0}",
        "cloud.yourHomes" to "منازلك",
        "cloud.noHomes" to "لا منزل مرتبط بحساب سحابة مكتبة هذا بعد.",
        "cloud.online" to "متصل",
        "cloud.offline" to "غير متصل",
        "cloud.notLinked" to "غير مرتبط بحسابك",
        "cloud.howToLink" to "لفتح منزل غير مرتبط: افتح المنزل في متصفح، وسجّل الدخول فيه، واربط حساب سحابة مكتبة من صفحة الحساب فيه (الحسابات المرتبطة). وامتلاك منزل في سحابة مكتبة ليس حسابًا فيه.",
        "cloud.refresh" to "تحديث",
        "cloud.signOut" to "تسجيل الخروج من سحابة مكتبة",
        "cloud.unreachable" to "لم يُجب المنزل، لا على عنوانه ولا عبر السحابة.",
        "cloud.refused" to "رفض المنزل تسجيل الدخول: ربما أُلغي الربط. اربط مرة أخرى من صفحة الحساب في المنزل.",
        "cloud.locked" to "حسابك في ذلك المنزل مقفل. اسأل مسؤوله.",
        "cloud.changePassword" to "يطلب ذلك المنزل أن تغيّر كلمة مرورك أولًا: سجّل الدخول فيه بها.",
        "cloud.signInHere" to "تسجيل الدخول عبر سحابة مكتبة",
    )
}
