' The channel's strings in its own language (Epic 58 story 58.15).
'
' The channel has its own language switch (Account: English / العربية), independent of the
' Roku's locale, so it reads its catalogs itself rather than through tr():
'
'   i18n = I18nNew("ar")              ' "en" or "ar"; pkg:/locale/<en_US|ar_SA>/strings.json
'   i18n.rtl                          ' true in Arabic: give it to LayoutNew
'   i18n.Tr("roku.notAvailable")      ' a static string, already visual (shaped at build time
'                                     ' by tools/shape_strings.py, lines joined with "\n"),
'                                     ' Arabic inside an English string too
'   i18n.Format("video.resumeFrom", ["12:34"])
'                                     ' a template filled in with raw values, then shaped
'                                     ' here (VisualLines, right to left in Arabic, left to
'                                     ' right in English when a value holds Arabic) to its
'                                     ' label's size
'
' A missing key gives the key itself and a log line, never a crash. Arabic's multi-line strings
' carry their own line breaks: their Label needs wrap="true", and must not wrap them again.

function I18nNew(lang as String) as Object
    if lang = "ar" then locale = "ar_SA" else locale = "en_US"
    data = invalid
    raw = ReadAsciiFile("pkg:/locale/" + locale + "/strings.json")
    if raw <> "" then data = ParseJson(raw)
    if data = invalid then
        print "[i18n] no catalog for "; locale
        data = {lang: lang, rtl: (lang = "ar"), strings: {}, templates: {}}
    end if
    return {
        lang: data.lang
        rtl: data.rtl
        strings: data.strings
        templates: data.templates
        Tr: i18n_Tr
        Format: i18n_Format
    }
end function

function i18n_Tr(key as String) as String
    s = m.strings[key]
    if s = invalid then
        if m.templates[key] <> invalid then return m.Format(key, [])
        print "[i18n] missing string: "; key
        return key
    end if
    return s
end function

' The template's %1$s, %2$s, %1$d... replaced by args (strings or numbers; the catalogs use
' positional placeholders only, which tools/i18n/check_tv_catalogs.py matches across
' languages); then shaped and reordered to the label's size: always in Arabic, and in English
' when a value holds Arabic. Values go in raw, never through VisualText first: a value already
' in presentation forms keeps the whole line in logical order (the forms are outside the
' shaping table, so VisualLines leaves the line as it is).
function i18n_Format(key as String, args as Object) as String
    t = m.templates[key]
    if t = invalid then
        if m.strings[key] <> invalid then return m.strings[key]
        print "[i18n] missing string: "; key
        return key
    end if
    text = t.text
    for i = args.Count() - 1 to 0 step -1
        v = args[i]
        if type(v) = "roString" or type(v) = "String" then
            value = v
        else
            value = v.ToStr()
        end if
        n = (i + 1).ToStr()
        text = text.Replace("%" + n + "$s", value)
        text = text.Replace("%" + n + "$d", value)
    end for
    if m.rtl then return VisualLines(text, t.widthChars, t.lines, "rtl").Join(Chr(10))
    ' English: a line with an Arabic value (a home's or a person's name) is shaped left to
    ' right; a plain one is left to the Label's own wrapping
    if vt_Prepare(text).rtl then return VisualLines(text, t.widthChars, t.lines, "ltr").Join(Chr(10))
    return text
end function
