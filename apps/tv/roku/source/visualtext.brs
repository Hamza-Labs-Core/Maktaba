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
    t = vt_Prepare(s)
    if t.gated then
        vt_LogLogical(t.gatedCode)
        return vt_JoinAll(vt_Ellipsize(t, maxWidthChars).chars)
    end if
    if t.harakat then t = vt_StripHarakat(t)
    t = vt_Ellipsize(t, maxWidthChars)
    return vt_Order(vt_Shape(t), base)
end function

function VisualLines(s as String, widthChars as Integer, maxLines as Integer, base as String) as Object
    t = vt_Prepare(s)
    out = []
    if t.gated then
        vt_LogLogical(t.gatedCode)
        for each line in vt_FitLines(vt_Wrap(t, widthChars), widthChars, maxLines)
            out.Push(vt_JoinAll(line.chars))
        end for
        return out
    end if
    if t.harakat then t = vt_StripHarakat(t)
    direction = base
    if direction = "auto" then
        if vt_FirstStrongRtl(t.cls) then direction = "rtl" else direction = "ltr"
    end if
    for each line in vt_FitLines(vt_Wrap(t, widthChars), widthChars, maxLines)
        out.Push(vt_Order(vt_Shape(line), direction))
    end for
    return out
end function

function VisualIsRtl(s as String) as Boolean
    return vt_FirstStrongRtl(vt_Prepare(s).cls)
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

' The string as characters, codes and classes: tab, CR and LF as spaces, BN removed. A UTF-16
' surrogate pair (brs-engine's strings) is kept together as one character, as on Roku. Flags
' say which later steps the string needs: harakat (H present), arabic (letters to shape), rtl
' (anything right-to-left), brackets (a paired bracket).
'
' The hot loops below make no function call per character: a call costs as much as ten
' statements on brs-engine and on the device.
function vt_Prepare(s as String) as Object
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
        if c >= &hD800 and c <= &hDBFF and i + 1 < n then
            lowUnit = Asc(units[i + 1])
            if lowUnit >= &hDC00 and lowUnit <= &hDFFF then
                ch = ch + units[i + 1]
                c = &h10000 + (c - &hD800) * 1024 + (lowUnit - &hDC00)
                i = i + 1
            end if
        end if
        if c < 256 then
            k = latin[c]
        else if c >= &h600 and c <= &h6FF then
            k = arabic[c - &h600]
        else
            k = vt_Class(c, tb)
        end if
        if k = "P" then
            chars.Push(" ")
            codes.Push(32)
            cls.Push("W")
        else if k <> "B" then
            chars.Push(ch)
            codes.Push(c)
            cls.Push(k)
            if k = "A" then
                hasArabic = true
                rtl = true
            else if k = "R" or k = "N" or k = "2" or k = "3" then
                rtl = true
            else if k = "O" then
                if c = 40 or c = 41 or c = 91 or c = 93 or c = 123 or c = 125 then
                    brackets = true
                else if c > 255 then
                    key = c.ToStr()
                    if closeOf.DoesExist(key) or closing.DoesExist(key) then brackets = true
                end if
            else if k = "H" then
                harakat = true
            else if k = "X" and not gated then
                gated = true
                gatedCode = c
            end if
        end if
        i = i + 1
    end while
    return {chars: chars, codes: codes, cls: cls, gated: gated, gatedCode: gatedCode, harakat: harakat, arabic: hasArabic, rtl: rtl, brackets: brackets}
end function

' A piece of a prepared string, with its flags (kept: a flag only says a step may be needed).
function vt_Piece(t as Object, chars as Object, codes as Object, cls as Object) as Object
    return {chars: chars, codes: codes, cls: cls, gated: t.gated, gatedCode: t.gatedCode, harakat: t.harakat, arabic: t.arabic, rtl: t.rtl, brackets: t.brackets}
end function

sub vt_LogLogical(c as Integer)
    print "[visualtext] shown in logical order: U+"; UCase(StrI(c, 16)); " is outside the shaping table"
end sub

function vt_StripHarakat(t as Object) as Object
    chars = []
    codes = []
    cls = []
    tc = t.chars
    tk = t.codes
    tl = t.cls
    for i = 0 to tc.Count() - 1
        if tl[i] <> "H" then
            chars.Push(tc[i])
            codes.Push(tk[i])
            cls.Push(tl[i])
        end if
    end for
    out = vt_Piece(t, chars, codes, cls)
    out.harakat = false
    return out
end function

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

' Greedy word wrap in logical order (words split on spaces; a word longer than the width is
' split), as visual.wrap. Lines are found as ranges of words, then copied once.
function vt_Wrap(t as Object, width as Integer) as Object
    codes = t.codes
    n = codes.Count()
    lines = [] ' each: a list of [start, end) word ranges
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
            else if curLen + 1 + wl <= width then
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
    out = []
    tc = t.chars
    tl = t.cls
    for each words in lines
        chars = []
        lcodes = []
        cls = []
        first = true
        for each w in words
            if not first then
                chars.Push(" ")
                lcodes.Push(32)
                cls.Push("W")
            end if
            first = false
            for k = w[0] to w[1] - 1
                chars.Push(tc[k])
                lcodes.Push(codes[k])
                cls.Push(tl[k])
            end for
        end for
        out.Push(vt_Piece(t, chars, lcodes, cls))
    end for
    return out
end function

' At most maxLines lines; the rest goes on the last line, cut with an ellipsis.
function vt_FitLines(lines as Object, width as Integer, maxLines as Integer) as Object
    if maxLines <= 0 or lines.Count() <= maxLines then return lines
    out = []
    for i = 0 to maxLines - 2
        out.Push(lines[i])
    end for
    rest = lines[maxLines - 1]
    for i = maxLines to lines.Count() - 1
        more = lines[i]
        rest.chars.Push(" ")
        rest.codes.Push(32)
        rest.cls.Push("W")
        rest.chars.Append(more.chars)
        rest.codes.Append(more.codes)
        rest.cls.Append(more.cls)
    end for
    out.Push(vt_Ellipsize(rest, width))
    return out
end function

' Arabic joining and lam-alef ligatures, in logical order (arabic-reshaper's algorithm).
' Forms: -1 not a letter of the table, 0 isolated, 1 initial, 2 medial, 3 final.
function vt_Shape(t as Object) as Object
    if not t.arabic then return t
    tb = vt_Tables()
    forms = tb.forms
    tc = t.chars
    tk = t.codes
    tl = t.cls
    n = tk.Count()
    form = []
    letter = []
    p = -1 ' the form of the character before
    pf = invalid ' its table entry
    for i = 0 to n - 1
        c = tk[i]
        f = invalid
        if c >= &h621 and c <= &h64A then f = forms[c - &h600]
        letter.Push(f)
        if f = invalid then
            p = -1
        else if p = -1 then
            p = 0
        else if f[3] = 0 and f[2] = 0 then
            p = 0 ' does not join the letter before
        else if pf[1] = 0 and pf[2] = 0 then
            p = 0 ' the letter before joins nothing after it
        else if p = 3 and pf[2] = 0 then
            p = 0 ' the letter before is final and has no medial form
        else
            if p = 0 then form[i - 1] = 1 else form[i - 1] = 2
            p = 3
        end if
        form.Push(p)
        pf = f
    end for

    lamAlef = tb.lamAlef
    chars = []
    codes = []
    cls = []
    i = 0
    while i < n
        c = tk[i]
        lig = invalid
        if c = &h644 and i + 1 < n then
            a = tk[i + 1]
            if a = &h627 or a = &h623 or a = &h625 or a = &h622 then lig = lamAlef[a.ToStr()]
        end if
        if lig <> invalid then
            ' the ligature's form: isolated or final by whether the lam joins the letter before
            if form[i] = 0 or form[i] = 1 then g = lig[0] else g = lig[1]
            chars.Push(Chr(g))
            codes.Push(g)
            cls.Push("A")
            i = i + 2
        else
            fm = form[i]
            if fm = -1 then
                chars.Push(tc[i])
                codes.Push(c)
            else
                g = letter[i][fm]
                chars.Push(Chr(g))
                codes.Push(g)
            end if
            cls.Push(tl[i])
            i = i + 1
        end if
    end while
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
    for i = 0 to n - 1
        k = tl[i]
        if k = "1" then
            k = "L"
        else if k = "2" then
            k = "R"
        else if k = "3" then
            k = "A"
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
            if last = "A" then k = "N"
            nums = true
        else if k = "S" or k = "C" then
            seps = true
        else if k = "T" then
            terms = true
        end if
        ty.Push(k)
    end for
    ' W4: one separator between two numbers of the same kind
    if seps then
        for i = 1 to n - 2
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
        end for
    end if
    ' W5: terminators next to European digits
    if terms and nums then
        i = 0
        while i < n
            if ty[i] = "T" then
                j = i
                while j < n and ty[j] = "T"
                    j = j + 1
                end while
                if (i > 0 and ty[i - 1] = "E") or (j < n and ty[j] = "E") then
                    for k = i to j - 1
                        ty[k] = "E"
                    end for
                end if
                i = j
            else
                i = i + 1
            end if
        end while
    end if
    ' W6 (the remaining separators and terminators are neutral), W7 (digits after L are L)
    if seps or terms or nums then
        last = e
        for i = 0 to n - 1
            k = ty[i]
            if k = "S" or k = "T" or k = "C" then
                ty[i] = "O"
            else if k = "L" or k = "R" then
                last = k
            else if k = "E" then
                if last = "L" then ty[i] = "L"
            end if
        end for
    end if

    if t.brackets then vt_BracketPairs(t, ty, e, tb)

    ' N1, N2 (a run of neutrals between two of the same direction takes it, else the
    ' embedding's), with I1, I2 (levels) in the same pass
    lv = []
    i = 0
    while i < n
        k = ty[i]
        if k = "O" or k = "W" then
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
            if para = 0 then
                if k = "R" then
                    lv.Push(1)
                else if k = "L" then
                    lv.Push(0)
                else
                    lv.Push(2)
                end if
            else
                if k = "R" then lv.Push(1) else lv.Push(2)
            end if
            i = i + 1
        end if
    end while
    ' L1: trailing whitespace at the paragraph level
    i = n - 1
    while i >= 0 and tl[i] = "W"
        lv[i] = para
        i = i - 1
    end while
    ' L2: reverse each run at or above each level, from the highest to the lowest odd level
    order = []
    top = 0
    low = 99
    for i = 0 to n - 1
        order.Push(i)
        x = lv[i]
        if x > top then top = x
        if x mod 2 = 1 and x < low then low = x
    end for
    level = top
    while level >= low
        i = 0
        while i < n
            if lv[order[i]] >= level then
                j = i + 1
                while j < n and lv[order[j]] >= level
                    j = j + 1
                end while
                a = i
                b = j - 1
                while a < b
                    x = order[a]
                    order[a] = order[b]
                    order[b] = x
                    a = a + 1
                    b = b - 1
                end while
                i = j
            else
                i = i + 1
            end if
        end while
        level = level - 1
    end while
    ' L4 (only neutrals have mirrored glyphs) and the output without the steering marks
    mirror = tb.mirror
    tk = t.codes
    out = []
    for each idx in order
        k = tl[idx]
        if k = "O" and lv[idx] mod 2 = 1 then
            m = mirror[tk[idx].ToStr()]
            if m = invalid then out.Push(tc[idx]) else out.Push(Chr(m))
        else if k <> "1" and k <> "2" and k <> "3" then
            out.Push(tc[idx])
        end if
    end for
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
