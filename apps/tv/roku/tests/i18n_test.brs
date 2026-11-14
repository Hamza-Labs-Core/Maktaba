' Story 58.15: the channel's catalogs read on the device (source/i18n.brs), from the files
' tools/shape_strings.py generates.
sub Main()
    m.pass = 0
    m.fail = 0
    en = I18nNew("en")
    ar = I18nNew("ar")
    t_Eq("en is ltr", en.rtl, false)
    t_Eq("ar is rtl", ar.rtl, true)
    t_Str("en static", en.Tr("roku.notAvailable"), "That video isn't available.")
    ' the Arabic static string is the build step's visual string: it starts (on the left) with
    ' the full stop that ends the sentence
    s = ar.Tr("roku.notAvailable")
    t_Str("ar static ends on the left", Left(s, 1), ".")
    t_Eq("ar static is shaped", Asc(Right(s, 1)) >= &hFE70, true)
    ' templates
    t_Str("en format", en.Format("video.resumeFrom", ["12:34"]), "Resume from 12:34")
    t_Str("en format number", en.Format("launcher.version", [3]), "Version 3")
    t_Str("en format two", en.Format("compat.homeVersion", ["Home", "1.2.3"]), "Home runs Maktaba 1.2.3.")
    got = ar.Format("video.resumeFrom", ["12:34"])
    t_Str("ar format: the time stays left to right, on the left", Left(got, 5), "12:34")
    t_Str("ar format equals VisualLines", got, VisualLines("المتابعة من 12:34", 32, 1, "rtl")[0])
    ' a long template wraps to its label, each line reordered
    long = ar.Format("compat.updateHome.admin", ["المنزل الرئيسي في الطابق الثاني"])
    t_Eq("ar format wraps", long.Split(Chr(10)).Count() >= 2, true)
    ' missing keys
    t_Str("missing Tr", en.Tr("no.such.key"), "no.such.key")
    t_Str("missing Format", ar.Format("no.such.key", []), "no.such.key")
    ' Tr on a template and Format on a static string still answer
    t_Str("Tr of a template", en.Tr("launcher.version"), "Version %1$s")
    t_Str("Format of a static string", en.Format("nav.home", []), "Home")
    print "RESULT pass="; m.pass; " fail="; m.fail
end sub

sub t_Eq(name as String, got as Dynamic, want as Dynamic)
    if got = want then
        m.pass = m.pass + 1
    else
        m.fail = m.fail + 1
        print "FAIL "; name; ": got "; got; ", want "; want
    end if
end sub

sub t_Str(name as String, got as String, want as String)
    t_Eq(name, got, want)
end sub
