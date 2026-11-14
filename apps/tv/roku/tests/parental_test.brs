' Story 58.14 T04 (the logic half; screens.test.mjs drives the screens): the parental limit as
' the channel keeps it (source/parental.brs), its lines in both languages, and what became of
' the account's own request.
sub Main()
    m.pass = 0
    m.fail = 0
    en = I18nNew("en")
    ar = I18nNew("ar")

    ' GET /api/me/parental's answers
    child = ParentalFrom(ParseJson("{""restricted"":true,""max_tier"":9,""allow_only"":false,""version"":3}"))
    ck_Eq("child restricted", child.restricted, true)
    ck_Eq("child tier", child.maxTier, 9)
    ck_True("child limited", ParentalLimited(child))
    device = ParentalFrom(ParseJson("{""restricted"":false,""max_tier"":13,""device_limit"":13}"))
    ck_True("a device limit is a limit", ParentalLimited(device))
    ck_Eq("no limit", ParentalLimited(ParentalFrom(ParseJson("{""restricted"":false}"))), false)
    ck_Eq("an older home (no answer)", ParentalLimited(ParentalFrom(invalid)), false)
    ck_Eq("no session field", ParentalLimited(invalid), false)

    ' the blocked page's line
    ck_Eq("en account line", ParentalLimitLine(child, en), "Your account is limited to 9+.")
    ck_Eq("en device line", ParentalLimitLine(device, en), "This device is limited to 13+.")
    ck_Eq("en all ages", ParentalLimitLine(ParentalFrom({restricted: true, max_tier: 0}), en), "Your account is limited to All ages.")
    ck_Eq("en nothing with a tier", ParentalLimitLine(ParentalFrom({restricted: true, max_tier: -1}), en), "Your account is limited to what a parent allowed.")
    ck_Eq("no line without a limit", ParentalLimitLine(ParentalFrom(invalid), en), "")
    got = ar.Format("parental.blocked.account", ["9"])
    ck_Eq("ar account line is the shaped template", ParentalLimitLine(child, ar), got)

    ' Account's lines
    lines = ParentalManagedLines(child, en)
    ck_Eq("en managed lines", lines, ["A parent manages this account.", "Content limit: 9+", "A parent can answer from Maktaba on their phone."])
    ck_Eq("device managed lines", ParentalManagedLines(device, en), ["This device is limited to 13+."])
    ck_Eq("no managed lines", ParentalManagedLines(ParentalFrom(invalid), en), [])
    ck_Eq("ar managed lines count", ParentalManagedLines(child, ar).Count(), 3)
    ck_Eq("ar managed first line", ParentalManagedLines(child, ar)[0], ar.Tr("parental.profile.managed"))

    ' the account's own requests, newest first
    list = ParseJson("{""requests"":[{""id"":""r2"",""subject_kind"":""video"",""subject_id"":""v9"",""status"":""approved""},{""id"":""r1"",""subject_kind"":""video"",""subject_id"":""v9"",""status"":""denied""},{""id"":""r0"",""subject_kind"":""series"",""subject_id"":""s1"",""status"":""pending""}]}")
    ck_Eq("the latest for v9", ParentalRequestState(list, "video", "v9"), "approved")
    ck_Eq("a series pending", ParentalRequestState(list, "series", "s1"), "pending")
    ck_Eq("none for another", ParentalRequestState(list, "video", "v1"), "none")
    ck_Eq("an ended request", ParentalRequestState(ParseJson("{""requests"":[{""subject_kind"":""video"",""subject_id"":""v9"",""status"":""expired""}]}"), "video", "v9"), "expired")
    ck_Eq("an older home", ParentalRequestState(invalid, "video", "v9"), "none")
    ck_Result()
end sub
