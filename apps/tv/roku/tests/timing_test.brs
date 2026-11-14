' Story 58.15 T04: what shaping one screen of text costs. 20 long descriptions (229 characters
' of Arabic with Latin, digits, a time and brackets) go through VisualLines as a screen's labels
' do, after one call that builds the tables (the channel does that once). The 20 are timed at
' their best of 5 runs, a reference loop the same way (100,000 steps of the kinds the shaping
' code runs: a for each, string comparisons, array reads, pushes and additions), and both printed:
'
'   TIMING shape=<ms> loop=<ms> units=<shape / loop>
'
' On brs-engine, tests/timing/t04.test.mjs holds units under its ceiling: how fast the
' interpreter runs changes from one machine to the next, the ratio of two workloads on the same
' interpreter much less. In the device-test package (tools/build.mjs --device-tests) the same
' line comes from the Roku Express: the real-device checklist (docs/stores/roku.md, row 10)
' reads shape against the 50 ms budget, and loop says how that device compares with brs-engine.
sub Main()
    m.pass = 0
    m.fail = 0
    d = "في الحلقة 3 من الموسم 2 (S2 E5)، يعود المحقق إلى المدينة بعد غياب طويل ليكتشف أن كل شيء قد تغيّر: الأصدقاء، الأعداء، وحتى البيت القديم. تبدأ القصة الساعة 12:34 مساءً، وIntro الحلقة يحمل مفاجأة لم يتوقعها أحد. مدة الحلقة 45 دقيقة."
    descs = []
    for i = 1 to 20
        descs.Push(d + " " + i.ToStr())
    end for
    first = VisualLines(d, 60, 4, "auto")
    ck_Eq("a description fills four lines", first.Count(), 4)
    shape = 999999
    refMs = 999999
    kinds = ["A", "L", "R", "O"]
    ts = CreateObject("roTimespan")
    for rep = 1 to 5
        ts.Mark()
        for each s in descs
            VisualLines(s, 60, 4, "auto")
        end for
        ms = ts.TotalMilliseconds()
        if ms < shape then shape = ms
        ts.Mark()
        x = 0
        refOut = []
        for i = 1 to 25000
            for each k in kinds
                if k = "A" then
                    refOut.Push(kinds[3])
                else
                    x = x + 1
                end if
            end for
        end for
        ms = ts.TotalMilliseconds()
        if ms < refMs then refMs = ms
    end for
    ck_Eq("the reference loop ran", x + refOut.Count(), 100000)
    units = "n/a"
    if refMs > 0 then units = Str(shape / refMs).Trim()
    print "TIMING shape="; shape.ToStr(); " loop="; refMs.ToStr(); " units="; units; " (20 descriptions of "; Len(d).ToStr(); " characters)"
    ck_Result()
end sub
