# Linux kernel TCP flaw (CVE-2026-98432): update your Linux kernel

*Published 2026-11-01. [العربية أدناه](#بالعربية)*

## Who is affected

Anyone running a Maktaba home, processing node or the cloud service on **Linux**, including
Docker on Linux. A Maktaba home on a Mac is not affected, and neither are the phone, tablet,
TV and computer apps.

## What it is

CVE-2026-98432 is a flaw in the Linux kernel itself, not in Maktaba. Someone who can reach the
machine over the network can trigger it while a TCP connection is being set up, without
signing in. It can crash the machine or corrupt kernel memory. It is rated critical.

The attack its finders describe goes through a *multipath TCP* listener. Programs built with
Go 1.24 or later, Maktaba's servers among them, listen that way by default.

## What we changed

From the next release on, Maktaba's servers (the home, streaming, the launcher and the cloud
service) no longer listen with multipath TCP. That closes the path described, but **the flaw is
in the kernel, so the fix is a kernel update.**

## What to do

1. Install your distribution's kernel update and **restart the machine**. Fixed upstream
   kernels: 5.10.272, 5.15.223, 6.1.190, 6.6.159, 6.12.113, 6.18.56, 7.2.10 and 7.3 or later.
   Distributions number their kernels their own way: check your distribution's security page.
2. Until you can update, turn multipath TCP off:

   ```bash
   echo 'net.mptcp.enabled = 0' | sudo tee /etc/sysctl.d/90-no-mptcp.conf
   sudo sysctl --system
   ```

3. Update Maktaba to the next release when it is out.

---

## بالعربية

# خلل في نواة لينكس عند اتصالات TCP ‏(CVE-2026-98432): حدّث نواة لينكس

*نُشر في 2026-11-01.*

## من المتأثر

كل من يشغّل منزل مكتبة أو عقدة معالجة أو الخدمة السحابية على **لينكس**، ومن ذلك Docker على
لينكس. منزل مكتبة على جهاز ماك غير متأثر، وكذلك تطبيقات الهاتف واللوحي والتلفاز والحاسوب.

## ما هو

‏CVE-2026-98432 خلل في نواة لينكس نفسها لا في مكتبة. يستطيع من يصل إلى الجهاز عبر الشبكة أن
يستغلّه أثناء إنشاء اتصال TCP دون تسجيل دخول، فيُسقط الجهاز أو يُفسد ذاكرة النواة. وقد صُنّف
خطيرًا جدًا.

الهجوم الذي وصفه مكتشفوه يمرّ عبر مستمع *TCP متعدد المسارات*. والبرامج المبنية بلغة Go
الإصدار 1.24 أو أحدث، ومنها خوادم مكتبة، تستمع بهذه الطريقة افتراضيًا.

## ما غيّرناه

ابتداءً من الإصدار القادم، لم تعد خوادم مكتبة (المنزل والبث والمشغّل والخدمة السحابية) تستمع
بطريقة TCP متعدد المسارات. هذا يغلق الطريق الموصوف، لكن **الخلل في النواة، فإصلاحه بتحديث
النواة.**

## ما الذي تفعله

1. ثبّت تحديث النواة من توزيعتك ثم **أعد تشغيل الجهاز**. أنوية لينكس المُصلحة: ‏5.10.272
   و5.15.223 و6.1.190 و6.6.159 و6.12.113 و6.18.56 و7.2.10 و7.3 أو أحدث. للتوزيعات أرقامها
   الخاصة: راجع صفحة الأمان في توزيعتك.
2. إلى أن تحدّث، أوقف TCP متعدد المسارات:

   ```bash
   echo 'net.mptcp.enabled = 0' | sudo tee /etc/sysctl.d/90-no-mptcp.conf
   sudo sysctl --system
   ```

3. حدّث مكتبة إلى الإصدار القادم حين يصدر.
