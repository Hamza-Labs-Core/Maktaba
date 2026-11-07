# An OpenAI API key typed into Settings → Models was kept in the browser

*Published 2026-11-06. [العربية أدناه](#بالعربية)*

## Who is affected

Anyone who typed an OpenAI API key into the web app's **Settings → Models** page and pressed
**Save**. A home that never had a key typed there is not affected, and neither are the phone,
tablet, TV and computer apps, which have no such field.

## What it is

The page saved the key in the browser's own storage (`localStorage`), in plain text, on that
browser only. Maktaba never used it: the server reads its OpenAI key from `OPENAI_API_KEY`.
Anyone who can read that browser's storage, someone else using the same browser profile among
them, could read the key. Nothing was sent to the server or to anyone else.

## What we changed

From the next release on, the page has no key field: it says that the key is set on the
server. Opening the page removes a key an earlier build kept.

## What to do

1. Update Maktaba to the next release when it is out, and open **Settings → Models** once in
   each browser where a key was typed, to remove it.
2. If a key was typed on a browser others use, or you are not sure, revoke it in your OpenAI
   account and make a new one.
3. To use OpenAI for transcription, set `OPENAI_API_KEY` on the server.

---

## بالعربية

# مفتاح OpenAI API المكتوب في الإعدادات ← النماذج كان يُحفظ في المتصفح

*نُشر في 2026-11-06.*

## من المتأثر

كل من كتب مفتاح OpenAI API في صفحة **الإعدادات ← النماذج** في تطبيق الويب وضغط **حفظ**. المنزل
الذي لم يُكتب فيه مفتاح هناك غير متأثر، وكذلك تطبيقات الهاتف واللوحي والتلفاز والحاسوب، فليس فيها
هذا الحقل.

## ما هو

كانت الصفحة تحفظ المفتاح في تخزين المتصفح نفسه (`localStorage`) نصًا صريحًا، في ذلك المتصفح
وحده. ولم تستعمله مكتبة أبدًا: فالخادم يقرأ مفتاح OpenAI من `OPENAI_API_KEY`. وكان يستطيع قراءة
المفتاح كل من يقرأ تخزين ذلك المتصفح، ومنهم من يستعمل ملف المتصفح نفسه. ولم يُرسل شيء إلى الخادم
ولا إلى أحد غيره.

## ما الذي غيّرناه

من الإصدار القادم، لم يعد في الصفحة حقل للمفتاح: صارت تقول إن المفتاح يُضبط على الخادم. وفتح
الصفحة يحذف مفتاحًا حفظه إصدار سابق.

## ما الذي تفعله

1. حدّث مكتبة إلى الإصدار القادم حين يصدر، وافتح **الإعدادات ← النماذج** مرة في كل متصفح كُتب فيه
   مفتاح، ليُحذف.
2. إن كُتب المفتاح في متصفح يستعمله غيرك، أو لم تكن متأكدًا، فألغِه في حسابك على OpenAI وأنشئ
   مفتاحًا جديدًا.
3. لاستعمال OpenAI في التفريغ النصي، اضبط `OPENAI_API_KEY` على الخادم.
