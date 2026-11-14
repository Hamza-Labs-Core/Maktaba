' Roku's visual text (Epic 58 story 58.15).
'
' Roku draws a Label's text left to right, one glyph per character: it neither joins Arabic
' letters nor reorders right-to-left text. These functions turn a string from the home (a
' title, a description, a name, a subtitle cue, what is typed on the MiniKeyboard) into what
' Roku must draw: Arabic letters in their joined presentation forms, the line in display order.
'
'   VisualText(s, maxWidthChars)               one line; cut with an ellipsis past the width
'                                              (0: no limit); direction from the first strong
'                                              character, as dir="auto" on the web
'   VisualTextDir(s, maxWidthChars, base)      the same with base "auto", "rtl" or "ltr"
'   VisualLines(s, widthChars, maxLines, base) wrapped to the width before reordering, each
'                                              line in display order; the last line ends with an
'                                              ellipsis when text is left over (0: no limit)
'   VisualIsRtl(s)                             true when the first strong character is Arabic
'                                              (the Label's alignment)
'
' The steps, each the same as the reference (apps/tv/roku/tools/visual.py, python-bidi and
' arabic-reshaper), which shared/i18n/visual-vectors.json holds this code to:
'   1. tab, CR and LF become spaces; invisible format characters are removed;
'   2. a string with a character outside the shaping table (Persian or Urdu letters, Hebrew,
'      ZWJ, ZWNJ, embedding controls) stays in logical order, logged, never a crash;
'   3. tashkeel is removed (Roku has no mark positioning);
'   4. the width cut, in characters, with an ellipsis that lands at the visual end;
'   5. Arabic joining and the lam-alef ligatures;
'   6. UAX #9 for one line: weak types (W1-W7), bracket pairs (N0), neutrals (N1, N2),
'      levels (I1, I2), trailing whitespace (L1), reordering (L2), mirrored glyphs (L4).
'      There are no explicit embeddings: their controls are gated in step 2.
' The data (classes, forms, mirrors, brackets) is generated in visualtext_tables.brs.
'
' Shape long text once per screen and keep the result: story 58.15's budget is 50 ms a screen.

function VisualText(s as String, maxWidthChars as Integer) as String
    return VisualTextDir(s, maxWidthChars, "auto")
end function

function VisualTextDir(s as String, maxWidthChars as Integer, base as String) as String
    tb = vt_Tables()
    if base <> "rtl" and not tb.nonAscii.IsMatch(s) then return vt_AsciiCut(s, maxWidthChars)
    t = vt_Prepare(s, false)
    if t.gated then
        vt_LogLogical(t.gatedCode)
        if t.harakat then t = vt_Prepare(s, true)
        return vt_JoinAll(vt_Ellipsize(t, maxWidthChars).chars)
    end if
    t = vt_Ellipsize(t, maxWidthChars)
    return vt_Order(vt_Shape(t, [[0, t.chars.Count()]], false), base)
end function

function VisualLines(s as String, widthChars as Integer, maxLines as Integer, base as String) as Object
    tb = vt_Tables()
    if base <> "rtl" and not tb.nonAscii.IsMatch(s) then return vt_AsciiLines(s, widthChars, maxLines)
    t = vt_Prepare(s, false)
    out = []
    if t.gated then
        vt_LogLogical(t.gatedCode)
        if t.harakat then t = vt_Prepare(s, true)
        for each line in vt_FitLines(vt_Wrap(t, widthChars), widthChars, maxLines)
            out.Push(vt_LineText(t, line))
        end for
        return out
    end if
    direction = base
    if direction = "auto" then
        if vt_FirstStrongRtl(t.cls) then direction = "rtl" else direction = "ltr"
    end if
    for each line in vt_FitLines(vt_Wrap(t, widthChars), widthChars, maxLines)
        out.Push(vt_Order(vt_Shape(t, line.words, line.ellipsis), direction))
    end for
    return out
end function

function VisualIsRtl(s as String) as Boolean
    if not vt_Tables().nonAscii.IsMatch(s) then return false
    return vt_FirstStrongRtl(vt_Prepare(s, false).cls)
end function

' ---- the steps ----------------------------------------------------------------------------

' The tables, built once per component (each SceneGraph component has its own global AA).
function vt_Tables() as Object
    g = GetGlobalAA()
    if g.vtTables = invalid then
        br = vt_TableBrackets()
        closeOf = {}
        for i = 0 to br.open.Count() - 1
            closeOf[br.open[i].ToStr()] = br.close[i]
        end for
        closing = {}
        for each c in br.close
            closing[c.ToStr()] = true
        end for
        ranges = vt_TableRanges()
        g.vtTables = {
            latin: vt_TableLatin().Split("")
            arabic: vt_TableArabic().Split("")
            ranges: ranges
            rangeCodes: ranges.codes.Split("")
            forms: vt_TableForms()
            lamAlef: vt_TableLamAlef()
            mirror: vt_TableMirror()
            closeOf: closeOf
            closing: closing
            nonAscii: CreateObject("roRegex", "[^ -~]", "")
        }
    end if
    return g.vtTables
end function

' The class code of a character (visualtext_tables.brs lists the codes).
function vt_Class(c as Integer, tb as Object) as String
    if c < 256 then return tb.latin[c]
    if c >= &h600 and c <= &h6FF then return tb.arabic[c - &h600]
    r = tb.ranges
    lo = 0
    hi = r.starts.Count() - 1
    while lo <= hi
        half = (lo + hi) \ 2
        if c < r.starts[half] then
            hi = half - 1
        else if c > r.ends[half] then
            lo = half + 1
        else
            return tb.rangeCodes[half]
        end if
    end while
    return "L"
end function

' The string as characters, codes and classes: tab, CR and LF as spaces, BN removed, and tashkeel
' removed (Roku has no mark positioning) unless keepHarakat. A UTF-16 surrogate pair
' (brs-engine's strings) is kept together as one character, as on Roku. Flags say which later
' steps the string needs: harakat (tashkeel seen), arabic (letters to shape), rtl (anything
' right-to-left), brackets (a paired bracket), gated (a character outside the shaping table:
' the caller prepares the string again with keepHarakat, as it is shown in logical order).
'
' The hot loops below make no function call per character: a call costs as much as five to ten
' statements on brs-engine and on the device. Arabic letters are tested first: they are most of
' what reaches this code (printable ASCII takes vt_AsciiCut and vt_AsciiLines).
function vt_Prepare(s as String, keepHarakat as Boolean) as Object
    tb = vt_Tables()
    latin = tb.latin
    arabic = tb.arabic
    closeOf = tb.closeOf
    closing = tb.closing
    units = s.Split("")
    chars = []
    codes = []
    cls = []
    gated = false
    gatedCode = 0
    harakat = false
    hasArabic = false
    rtl = false
    brackets = false
    n = units.Count()
    i = 0
    while i < n
        ch = units[i]
        c = Asc(ch)
        if c < 256 then
            k = latin[c]
        else if c >= &h600 and c <= &h6FF then
            k = arabic[c - &h600]
        else
            if c >= &hD800 and c <= &hDBFF and i + 1 < n then
                lowUnit = Asc(units[i + 1])
                if lowUnit >= &hDC00 and lowUnit <= &hDFFF then
                    ch = ch + units[i + 1]
                    c = &h10000 + (c - &hD800) * 1024 + (lowUnit - &hDC00)
                    i = i + 1
                end if
            end if
            k = vt_Class(c, tb)
        end if
        if k = "A" then
            chars.Push(ch)
            codes.Push(c)
            cls.Push(k)
            hasArabic = true
        else if k = "P" then
            chars.Push(" ")
            codes.Push(32)
            cls.Push("W")
        else if k = "H" then
            harakat = true
            if keepHarakat then
                chars.Push(ch)
                codes.Push(c)
                cls.Push(k)
            end if
        else if k <> "B" then
            chars.Push(ch)
            codes.Push(c)
            cls.Push(k)
            if k = "R" or k = "N" or k = "2" or k = "3" then
                rtl = true
            else if k = "O" then
                if c = 40 or c = 41 or c = 91 or c = 93 or c = 123 or c = 125 then
                    brackets = true
                else if c > 255 then
                    key = c.ToStr()
                    if closeOf.DoesExist(key) or closing.DoesExist(key) then brackets = true
                end if
            else if k = "X" and not gated then
                gated = true
                gatedCode = c
            end if
        end if
        i = i + 1
    end while
    if hasArabic then rtl = true
    return {chars: chars, codes: codes, cls: cls, gated: gated, gatedCode: gatedCode, harakat: harakat, arabic: hasArabic, rtl: rtl, brackets: brackets}
end function

' A piece of a prepared string, with its flags (kept: a flag only says a step may be needed).
function vt_Piece(t as Object, chars as Object, codes as Object, cls as Object) as Object
    return {chars: chars, codes: codes, cls: cls, gated: t.gated, gatedCode: t.gatedCode, harakat: t.harakat, arabic: t.arabic, rtl: t.rtl, brackets: t.brackets}
end function

sub vt_LogLogical(c as Integer)
    print "[visualtext] shown in logical order: U+"; UCase(StrI(c, 16)); " is outside the shaping table"
end sub

function vt_Slice(t as Object, first as Integer, count as Integer) as Object
    chars = []
    codes = []
    cls = []
    tc = t.chars
    tk = t.codes
    tl = t.cls
    for i = first to first + count - 1
        chars.Push(tc[i])
        codes.Push(tk[i])
        cls.Push(tl[i])
    end for
    return vt_Piece(t, chars, codes, cls)
end function

' At most width characters: cut, trailing spaces dropped, then an ellipsis (U+2026, neutral).
function vt_Ellipsize(t as Object, width as Integer) as Object
    n = t.chars.Count()
    if width <= 0 or n <= width then return t
    keep = 0
    if width > 1 then
        keep = width - 1
        while keep > 0 and t.codes[keep - 1] = 32
            keep = keep - 1
        end while
    end if
    out = vt_Slice(t, 0, keep)
    out.chars.Push(Chr(&h2026))
    out.codes.Push(&h2026)
    out.cls.Push("O")
    return out
end function

function vt_FirstStrongRtl(cls as Object) as Boolean
    for each k in cls
        if k = "L" or k = "1" then return false
        if k = "R" or k = "A" or k = "2" or k = "3" then return true
    end for
    return false
end function

' Printable ASCII (nothing outside space to tilde) on a line that is not forced right to left
' needs none of the steps: each character is its own glyph and the order is the logical one.
' These two give what the full steps give for it, a word at a time instead of a character.
function vt_AsciiCut(s as String, width as Integer) as String
    n = Len(s)
    if width <= 0 or n <= width then return s
    keep = 0
    if width > 1 then
        keep = width - 1
        while keep > 0 and Mid(s, keep, 1) = " "
            keep = keep - 1
        end while
    end if
    return Left(s, keep) + Chr(&h2026)
end function

function vt_AsciiLines(s as String, width as Integer, maxLines as Integer) as Object
    lines = []
    cur = ""
    curLen = -1 ' no line started
    for each w in s.Tokenize(" ")
        ws = w
        while width > 0 and Len(ws) > width
            if curLen >= 0 then
                lines.Push(cur)
                curLen = -1
            end if
            lines.Push(Left(ws, width))
            ws = Mid(ws, width + 1)
        end while
        wl = Len(ws)
        if wl > 0 then
            if curLen < 0 then
                cur = ws
                curLen = wl
            else if width <= 0 or curLen + 1 + wl <= width then
                cur = cur + " " + ws
                curLen = curLen + 1 + wl
            else
                lines.Push(cur)
                cur = ws
                curLen = wl
            end if
        end if
    end for
    if curLen >= 0 then lines.Push(cur)
    if maxLines <= 0 or lines.Count() <= maxLines then return lines
    out = []
    for i = 0 to maxLines - 2
        out.Push(lines[i])
    end for
    rest = lines[maxLines - 1]
    for i = maxLines to lines.Count() - 1
        rest = rest + " " + lines[i]
    end for
    out.Push(vt_AsciiCut(rest, width))
    return out
end function

' Greedy word wrap in logical order (words split on spaces; a word longer than the width is
' split; no width: one line), as visual.wrap. Each line is a list of [start, end) word ranges
' of t, joined by one space: nothing is copied here (vt_Shape copies each line once).
function vt_Wrap(t as Object, width as Integer) as Object
    codes = t.codes
    n = codes.Count()
    lines = []
    cur = invalid
    curLen = 0
    i = 0
    while i < n
        while i < n and codes[i] = 32
            i = i + 1
        end while
        if i >= n then exit while
        j = i
        while j < n and codes[j] <> 32
            j = j + 1
        end while
        ws = i
        i = j
        while width > 0 and j - ws > width
            if cur <> invalid then
                lines.Push(cur)
                cur = invalid
            end if
            lines.Push([[ws, ws + width]])
            ws = ws + width
        end while
        wl = j - ws
        if wl > 0 then
            if cur = invalid then
                cur = [[ws, j]]
                curLen = wl
            else if width <= 0 or curLen + 1 + wl <= width then
                cur.Push([ws, j])
                curLen = curLen + 1 + wl
            else
                lines.Push(cur)
                cur = [[ws, j]]
                curLen = wl
            end if
        end if
    end while
    if cur <> invalid then lines.Push(cur)
    return lines
end function

' At most maxLines lines, each {words, ellipsis}; the rest goes on the last line, cut with an
' ellipsis.
function vt_FitLines(lines as Object, width as Integer, maxLines as Integer) as Object
    out = []
    if maxLines <= 0 or lines.Count() <= maxLines then
        for each words in lines
            out.Push({words: words, ellipsis: false})
        end for
        return out
    end if
    for i = 0 to maxLines - 2
        out.Push({words: lines[i], ellipsis: false})
    end for
    rest = []
    for i = maxLines - 1 to lines.Count() - 1
        rest.Append(lines[i])
    end for
    out.Push(vt_EllipsizeWords(rest, width))
    return out
end function

' vt_Ellipsize on a line of words: at most width characters, the space before the cut dropped,
' then the ellipsis.
function vt_EllipsizeWords(words as Object, width as Integer) as Object
    total = words.Count() - 1
    for each w in words
        total = total + w[1] - w[0]
    end for
    if width <= 0 or total <= width then return {words: words, ellipsis: false}
    keep = 0
    if width > 1 then keep = width - 1
    kept = []
    used = 0
    for each w in words
        if kept.Count() > 0 then
            if used + 1 >= keep then exit for ' the space would end the cut: dropped
            used = used + 1
        end if
        l = w[1] - w[0]
        if used + l >= keep then
            if keep > used then kept.Push([w[0], w[0] + keep - used])
            exit for
        end if
        kept.Push(w)
        used = used + l
    end for
    return {words: kept, ellipsis: true}
end function

' A line in logical order, as it is shown when the string is gated.
function vt_LineText(t as Object, line as Object) as String
    tc = t.chars
    out = []
    for each w in line.words
        if out.Count() > 0 then out.Push(" ")
        for k = w[0] to w[1] - 1
            out.Push(tc[k])
        end for
    end for
    if line.ellipsis then out.Push(Chr(&h2026))
    return out.Join("")
end function

' One line copied out of t (its [start, end) word ranges joined by one space, then the
' ellipsis), with Arabic joining and the lam-alef ligatures in logical order (arabic-reshaper's
' algorithm). A word is shaped on its own: what is around it (a space, the cut) joins nothing.
' Forms: -1 not a letter of the table, 0 isolated, 1 initial, 2 medial, 3 final.
function vt_Shape(t as Object, words as Object, ellipsis as Boolean) as Object
    tc = t.chars
    tk = t.codes
    tl = t.cls
    chars = []
    codes = []
    cls = []
    first = true
    if not t.arabic then
        for each w in words
            if first then
                first = false
            else
                chars.Push(" ")
                codes.Push(32)
                cls.Push("W")
            end if
            for i = w[0] to w[1] - 1
                chars.Push(tc[i])
                codes.Push(tk[i])
                cls.Push(tl[i])
            end for
        end for
    else
        tb = vt_Tables()
        forms = tb.forms
        lamAlef = tb.lamAlef
        for each w in words
            if first then
                first = false
            else
                chars.Push(" ")
                codes.Push(32)
                cls.Push("W")
            end if
            ' one pass: a character's form is final once the next one is seen, so each is
            ' copied out one step behind (pend), a lam with the alef after it as their ligature
            p = -1 ' the form of the character before
            pf = invalid ' its table entry
            pend = -1 ' the character before, not yet copied out
            for i = w[0] to w[1] - 1
                c = tk[i]
                f = invalid
                if c >= &h621 and c <= &h64A then f = forms[c - &h600]
                if f = invalid then
                    np = -1
                else if p = -1 then
                    np = 0
                else if f[3] = 0 and f[2] = 0 then
                    np = 0 ' does not join the letter before
                else if pf[1] = 0 and pf[2] = 0 then
                    np = 0 ' the letter before joins nothing after it
                else if p = 3 and pf[2] = 0 then
                    np = 0 ' the letter before is final and has no medial form
                else
                    if p = 0 then p = 1 else p = 2
                    np = 3
                end if
                nxt = i
                if pend >= 0 then
                    if p = -1 then
                        chars.Push(tc[pend])
                        codes.Push(tk[pend])
                        cls.Push(tl[pend])
                    else
                        g = invalid
                        if tk[pend] = &h644 then
                            if c = &h627 or c = &h623 or c = &h625 or c = &h622 then
                                ' the ligature's form: isolated or final by whether the lam
                                ' joins the letter before
                                lig = lamAlef[c.ToStr()]
                                if p = 0 or p = 1 then g = lig[0] else g = lig[1]
                                nxt = -1 ' the alef is in the ligature
                            end if
                        end if
                        if g = invalid then g = pf[p]
                        chars.Push(Chr(g))
                        codes.Push(g)
                        cls.Push("A")
                    end if
                end if
                pend = nxt
                p = np
                pf = f
            end for
            if pend >= 0 then
                if p = -1 then
                    chars.Push(tc[pend])
                    codes.Push(tk[pend])
                    cls.Push(tl[pend])
                else
                    g = pf[p]
                    chars.Push(Chr(g))
                    codes.Push(g)
                    cls.Push("A")
                end if
            end if
        end for
    end if
    if ellipsis then
        chars.Push(Chr(&h2026))
        codes.Push(&h2026)
        cls.Push("O")
    end if
    return vt_Piece(t, chars, codes, cls)
end function

' UAX #9 for one line, then the characters in display order, mirrored on odd levels, without the
' steering marks.
function vt_Order(t as Object, base as String) as String
    tc = t.chars
    tl = t.cls
    n = tc.Count()
    if n = 0 then return ""

    ' P2, P3: the paragraph level
    para = 0
    if base = "rtl" then
        para = 1
    else if base = "auto" and t.rtl then
        if vt_FirstStrongRtl(tl) then para = 1
    end if

    ' left to right with nothing right to left: the order is the logical one (all levels even)
    if para = 0 and not t.rtl then
        out = []
        for i = 0 to n - 1
            if tl[i] <> "1" then out.Push(tc[i])
        end for
        return out.Join("")
    end if

    if para = 1 then e = "R" else e = "L"
    tb = vt_Tables()

    ' W1 (marks take the type before them), W2 (European digits after Arabic letters are
    ' Arabic digits) and W3 (AL is R), in one pass, each on the previous rule's result
    ty = []
    prev = e
    last = e
    seps = false
    terms = false
    nums = false
    marks = false
    wIdx = [] ' where W4 to W7 may change a type: digits, separators, terminators
    wStrong = [] ' the strong type before each (W7)
    for i = 0 to n - 1
        k = tl[i]
        if k = "A" then
            prev = "A"
            last = "A"
            ty.Push("R")
        else
            if k = "1" then
                k = "L"
                marks = true
            else if k = "2" then
                k = "R"
                marks = true
            else if k = "3" then
                k = "A"
                marks = true
            else if k = "M" then
                k = prev
            end if
            prev = k
            if k = "L" or k = "R" then
                last = k
            else if k = "A" then
                last = "A"
                k = "R"
            else if k = "E" then
                wIdx.Push(i)
                wStrong.Push(last)
                if last = "A" then k = "N"
                nums = true
            else if k = "S" or k = "C" then
                wIdx.Push(i)
                wStrong.Push(last)
                seps = true
            else if k = "T" then
                wIdx.Push(i)
                wStrong.Push(last)
                terms = true
            end if
            ty.Push(k)
        end if
    end for
    ' W4 to W7 change only the types at wIdx (or a run of terminators starting at one), so they
    ' visit those, in order, instead of the whole line
    ' W4: one separator between two numbers of the same kind
    if seps then
        for each i in wIdx
            if i > 0 and i < n - 1 then
                k = ty[i]
                if k = "S" then
                    if ty[i - 1] = "E" and ty[i + 1] = "E" then ty[i] = "E"
                else if k = "C" then
                    if ty[i - 1] = "E" and ty[i + 1] = "E" then
                        ty[i] = "E"
                    else if ty[i - 1] = "N" and ty[i + 1] = "N" then
                        ty[i] = "N"
                    end if
                end if
            end if
        end for
    end if
    ' W5: terminators next to European digits
    if terms and nums then
        done = -1 ' the end of the last run of terminators seen
        for each i in wIdx
            if i > done and ty[i] = "T" then
                j = i
                while j < n and ty[j] = "T"
                    j = j + 1
                end while
                if (i > 0 and ty[i - 1] = "E") or (j < n and ty[j] = "E") then
                    for k = i to j - 1
                        ty[k] = "E"
                    end for
                end if
                done = j - 1
            end if
        end for
    end if
    ' W6 (the remaining separators and terminators are neutral), W7 (digits after L are L)
    for x = 0 to wIdx.Count() - 1
        i = wIdx[x]
        k = ty[i]
        if k = "S" or k = "T" or k = "C" then
            ty[i] = "O"
        else if k = "E" and wStrong[x] = "L" then
            ty[i] = "L"
        end if
    end for

    if t.brackets then vt_BracketPairs(t, ty, e, tb)

    ' N1, N2 (a run of neutrals between two of the same direction takes it, else the
    ' embedding's), with I1, I2 (levels) in the same pass
    lv = []
    i = 0
    while i < n
        k = ty[i]
        if k = "R" then
            lv.Push(1)
            i = i + 1
        else if k = "O" or k = "W" then
            j = i
            while j < n and (ty[j] = "O" or ty[j] = "W")
                j = j + 1
            end while
            before = e
            if i > 0 then
                before = ty[i - 1]
                if before <> "L" then before = "R"
            end if
            after = e
            if j < n then
                after = ty[j]
                if after <> "L" then after = "R"
            end if
            d = e
            if before = after then d = before
            if para = 0 then
                if d = "R" then x = 1 else x = 0
            else
                if d = "R" then x = 1 else x = 2
            end if
            for m = i to j - 1
                lv.Push(x)
            end for
            i = j
        else
            if para = 0 and k = "L" then lv.Push(0) else lv.Push(2)
            i = i + 1
        end if
    end while
    ' L1: trailing whitespace at the paragraph level
    i = n - 1
    while i >= 0 and tl[i] = "W"
        lv[i] = para
        i = i - 1
    end while
    ' L2 and L4: levels are 0, 1 and 2 (there are no embeddings), so the display order is each run
    ' at level 1 or above reversed, with each run at level 2 inside it reversed back; a neutral
    ' at level 1 takes its mirrored glyph; the steering marks are not drawn
    mirror = tb.mirror
    tk = t.codes
    out = []
    i = 0
    while i < n
        if lv[i] = 0 then
            if marks then
                k = tl[i]
                if k <> "1" and k <> "2" and k <> "3" then out.Push(tc[i])
            else
                out.Push(tc[i])
            end if
            i = i + 1
        else
            j = i + 1
            while j < n and lv[j] > 0
                j = j + 1
            end while
            q = j - 1
            while q >= i
                if lv[q] = 2 then
                    r = q
                    while r > i and lv[r - 1] = 2
                        r = r - 1
                    end while
                    for x = r to q
                        if marks then
                            k = tl[x]
                            if k <> "1" and k <> "2" and k <> "3" then out.Push(tc[x])
                        else
                            out.Push(tc[x])
                        end if
                    end for
                    q = r - 1
                else
                    k = tl[q]
                    if k = "O" then
                        m = mirror[tk[q].ToStr()]
                        if m = invalid then out.Push(tc[q]) else out.Push(Chr(m))
                    else if not marks or (k <> "1" and k <> "2" and k <> "3") then
                        out.Push(tc[q])
                    end if
                    q = q - 1
                end if
            end while
            i = j
        end if
    end while
    return out.Join("")
end function

' The strong direction a type counts as for N0: "L", "R" (R, EN, AN) or "".
function vt_Strong(k as String) as String
    if k = "L" then return "L"
    if k = "R" or k = "E" or k = "N" then return "R"
    return ""
end function

' N0: paired brackets take the direction of what they enclose (BD16 pairs, at most 63 open).
sub vt_BracketPairs(t as Object, ty as Object, e as String, tb as Object)
    n = ty.Count()
    stack = []
    pairs = []
    for i = 0 to n - 1
        if ty[i] = "O" then
            key = t.codes[i].ToStr()
            closer = tb.closeOf[key]
            if closer <> invalid then
                if stack.Count() = 63 then exit for
                stack.Push({close: closer, at: i})
            else if tb.closing.DoesExist(key) then
                s = stack.Count() - 1
                while s >= 0
                    if stack[s].close = t.codes[i] then
                        pairs.Push({o: stack[s].at, c: i})
                        while stack.Count() > s
                            stack.Pop()
                        end while
                        exit while
                    end if
                    s = s - 1
                end while
            end if
        end if
    end for
    if pairs.Count() = 0 then return
    pairs.SortBy("o")
    for each p in pairs
        o = p.o
        c = p.c
        inside = ""
        for k = o + 1 to c - 1
            d = vt_Strong(ty[k])
            if d = e then
                inside = e
                exit for
            else if d <> "" then
                inside = d
            end if
        end for
        if inside <> "" then
            d = e
            if inside <> e then
                ctx = e
                k = o - 1
                while k >= 0
                    s2 = vt_Strong(ty[k])
                    if s2 <> "" then
                        ctx = s2
                        exit while
                    end if
                    k = k - 1
                end while
                if ctx <> e then d = ctx
            end if
            vt_SetBracket(t, ty, o, d)
            vt_SetBracket(t, ty, c, d)
        end if
    end for
end sub

' A bracket's new type, and the marks that followed it (W1 gave them the bracket's old type).
sub vt_SetBracket(t as Object, ty as Object, at as Integer, d as String)
    ty[at] = d
    k = at + 1
    while k < ty.Count() and t.cls[k] = "M"
        ty[k] = d
        k = k + 1
    end while
end sub

function vt_JoinAll(chars as Object) as String
    return chars.Join("")
end function
