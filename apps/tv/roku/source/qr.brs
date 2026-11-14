' A small QR code encoder (ISO/IEC 18004) for the code screen (story 58.12), ported from the web
' app's web/src/lib/qr.ts: byte mode, error correction level M, the smallest version that holds
' the text, the mask with the lowest penalty. Roku has no QR drawing of its own; the other TV
' apps draw the QR of the sign-in's verify_url on the device too, so the channel needs no image
' from the home or the cloud. Versions 1 to 10 only (up to 213 bytes): a verify URL is about 40.
'
' QrEncode(text) returns the modules, rows of booleans (true = dark) without the quiet zone, or
' invalid when the text is too long. tests/qr_test.brs holds it to qr.ts module by module.

function qr_EccPerBlock() as Object
    return [-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26]
end function

function qr_Blocks() as Object
    return [-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5]
end function

function qr_Xor(a as Integer, b as Integer) as Integer
    return (a or b) - (a and b)
end function

' The modules a version's data and error correction fill.
function qr_RawDataModules(ver as Integer) as Integer
    result = (16 * ver + 128) * ver + 64
    if ver >= 2 then
        align = Int(ver / 7) + 2
        result = result - ((25 * align - 10) * align - 55)
        if ver >= 7 then result = result - 36
    end if
    return result
end function

' The character count's bits in byte mode.
function qr_CountBits(ver as Integer) as Integer
    if ver <= 9 then return 8
    return 16
end function

function qr_DataCodewords(ver as Integer) as Integer
    return Int(qr_RawDataModules(ver) / 8) - qr_EccPerBlock()[ver] * qr_Blocks()[ver]
end function

' --- Reed-Solomon over GF(2^8), polynomial 0x11D ---

function qr_GfMul(x as Integer, y as Integer) as Integer
    z = 0
    for i = 7 to 0 step -1
        z = qr_Xor(z << 1, (z >> 7) * &h11D)
        z = qr_Xor(z, ((y >> i) and 1) * x)
    end for
    return z and &hFF
end function

function qr_RsDivisor(degree as Integer) as Object
    result = []
    for i = 0 to degree - 1
        result.Push(0)
    end for
    result[degree - 1] = 1
    root = 1
    for i = 0 to degree - 1
        for j = 0 to degree - 1
            result[j] = qr_GfMul(result[j], root)
            if j + 1 < degree then result[j] = qr_Xor(result[j], result[j + 1])
        end for
        root = qr_GfMul(root, 2)
    end for
    return result
end function

function qr_RsRemainder(data as Object, divisor as Object) as Object
    result = []
    for each d in divisor
        result.Push(0)
    end for
    for each b in data
        factor = qr_Xor(b, result.Shift())
        result.Push(0)
        for i = 0 to divisor.Count() - 1
            result[i] = qr_Xor(result[i], qr_GfMul(divisor[i], factor))
        end for
    end for
    return result
end function

' The data codewords split in blocks, each with its error correction, interleaved.
function qr_WithErrorCorrection(data as Object, ver as Integer) as Object
    numBlocks = qr_Blocks()[ver]
    eccLen = qr_EccPerBlock()[ver]
    raw = Int(qr_RawDataModules(ver) / 8)
    numShort = numBlocks - (raw mod numBlocks)
    shortLen = Int(raw / numBlocks)
    divisor = qr_RsDivisor(eccLen)
    blocks = []
    k = 0
    for i = 0 to numBlocks - 1
        n = shortLen - eccLen
        if i >= numShort then n = n + 1
        dat = []
        for j = k to k + n - 1
            dat.Push(data[j])
        end for
        k = k + n
        ecc = qr_RsRemainder(dat, divisor)
        if i < numShort then dat.Push(0)
        dat.Append(ecc)
        blocks.Push(dat)
    end for
    result = []
    for i = 0 to blocks[0].Count() - 1
        for j = 0 to numBlocks - 1
            if i <> shortLen - eccLen or j >= numShort then result.Push(blocks[j][i])
        end for
    end for
    return result
end function

' --- the symbol ---

function qr_Symbol(ver as Integer) as Object
    size = ver * 4 + 17
    modules = []
    isFunction = []
    for y = 0 to size - 1
        a = []
        b = []
        for x = 0 to size - 1
            a.Push(false)
            b.Push(false)
        end for
        modules.Push(a)
        isFunction.Push(b)
    end for
    return {version: ver, size: size, modules: modules, isFunction: isFunction}
end function

sub qr_Set(s as Object, x as Integer, y as Integer, dark as Boolean)
    s.modules[y][x] = dark
    s.isFunction[y][x] = true
end sub

function qr_AlignmentPositions(ver as Integer) as Object
    if ver = 1 then return []
    n = Int(ver / 7) + 2
    size = ver * 4 + 17
    ' version 32's step is the exception qr.ts keeps; it is above this encoder's range
    gap = Int(-Int(-(ver * 4 + 4) / (n * 2 - 2))) * 2
    result = [6]
    p = size - 7
    while result.Count() < n
        tail = []
        for i = 1 to result.Count() - 1
            tail.Push(result[i])
        end for
        result = [6, p]
        result.Append(tail)
        p = p - gap
    end while
    return result
end function

sub qr_DrawFinder(s as Object, x as Integer, y as Integer)
    for dy = -4 to 4
        for dx = -4 to 4
            dist = qr_Max(Abs(dx), Abs(dy))
            xx = x + dx
            yy = y + dy
            if xx >= 0 and xx < s.size and yy >= 0 and yy < s.size then qr_Set(s, xx, yy, dist <> 2 and dist <> 4)
        end for
    end for
end sub

function qr_Max(a as Integer, b as Integer) as Integer
    if a > b then return a
    return b
end function

sub qr_DrawFormat(s as Object, mask as Integer)
    ' level M's two format bits are 0
    data = mask
    rm = data
    for i = 0 to 9
        rm = qr_Xor(rm << 1, (rm >> 9) * &h537)
    end for
    bits = qr_Xor((data << 10) or rm, &h5412)
    for i = 0 to 5
        qr_Set(s, 8, i, ((bits >> i) and 1) <> 0)
    end for
    qr_Set(s, 8, 7, ((bits >> 6) and 1) <> 0)
    qr_Set(s, 8, 8, ((bits >> 7) and 1) <> 0)
    qr_Set(s, 7, 8, ((bits >> 8) and 1) <> 0)
    for i = 9 to 14
        qr_Set(s, 14 - i, 8, ((bits >> i) and 1) <> 0)
    end for
    for i = 0 to 7
        qr_Set(s, s.size - 1 - i, 8, ((bits >> i) and 1) <> 0)
    end for
    for i = 8 to 14
        qr_Set(s, 8, s.size - 15 + i, ((bits >> i) and 1) <> 0)
    end for
    qr_Set(s, 8, s.size - 8, true)
end sub

sub qr_DrawVersion(s as Object)
    if s.version < 7 then return
    rm = s.version
    for i = 0 to 11
        rm = qr_Xor(rm << 1, (rm >> 11) * &h1F25)
    end for
    bits = (s.version << 12) or rm
    for i = 0 to 17
        dark = ((bits >> i) and 1) <> 0
        a = s.size - 11 + (i mod 3)
        b = Int(i / 3)
        qr_Set(s, a, b, dark)
        qr_Set(s, b, a, dark)
    end for
end sub

sub qr_DrawFunctionPatterns(s as Object)
    for i = 0 to s.size - 1
        qr_Set(s, 6, i, i mod 2 = 0)
        qr_Set(s, i, 6, i mod 2 = 0)
    end for
    qr_DrawFinder(s, 3, 3)
    qr_DrawFinder(s, s.size - 4, 3)
    qr_DrawFinder(s, 3, s.size - 4)
    ps = qr_AlignmentPositions(s.version)
    last = ps.Count() - 1
    for i = 0 to last
        for j = 0 to last
            corner = (i = 0 and j = 0) or (i = 0 and j = last) or (i = last and j = 0)
            if not corner then
                for dy = -2 to 2
                    for dx = -2 to 2
                        qr_Set(s, ps[i] + dx, ps[j] + dy, qr_Max(Abs(dx), Abs(dy)) <> 1)
                    end for
                end for
            end if
        end for
    end for
    qr_DrawFormat(s, 0)
    qr_DrawVersion(s)
end sub

sub qr_DrawCodewords(s as Object, data as Object)
    i = 0
    total = data.Count() * 8
    col = s.size - 1
    while col >= 1
        if col = 6 then col = 5
        upward = ((col + 1) and 2) = 0
        for vert = 0 to s.size - 1
            for j = 0 to 1
                x = col - j
                y = vert
                if upward then y = s.size - 1 - vert
                if not s.isFunction[y][x] and i < total then
                    s.modules[y][x] = ((data[i >> 3] >> (7 - (i and 7))) and 1) <> 0
                    i = i + 1
                end if
            end for
        end for
        col = col - 2
    end while
end sub

function qr_Invert(mask as Integer, x as Integer, y as Integer) as Boolean
    if mask = 0 then return (x + y) mod 2 = 0
    if mask = 1 then return y mod 2 = 0
    if mask = 2 then return x mod 3 = 0
    if mask = 3 then return (x + y) mod 3 = 0
    if mask = 4 then return (Int(x / 3) + Int(y / 2)) mod 2 = 0
    if mask = 5 then return ((x * y) mod 2) + ((x * y) mod 3) = 0
    if mask = 6 then return (((x * y) mod 2) + ((x * y) mod 3)) mod 2 = 0
    return (((x + y) mod 2) + ((x * y) mod 3)) mod 2 = 0
end function

sub qr_ApplyMask(s as Object, mask as Integer)
    for y = 0 to s.size - 1
        for x = 0 to s.size - 1
            if not s.isFunction[y][x] and qr_Invert(mask, x, y) then s.modules[y][x] = not s.modules[y][x]
        end for
    end for
end sub

' The standard penalty (runs, 2x2 blocks, finder-like patterns, balance).
function qr_Penalty(s as Object) as Integer
    n = s.size
    md = s.modules
    pa = [true, false, true, true, true, false, true, false, false, false, false]
    pb = [false, false, false, false, true, false, true, true, true, false, true]
    score = 0
    for each rows in [true, false]
        for y = 0 to n - 1
            line = []
            for x = 0 to n - 1
                if rows then line.Push(md[y][x]) else line.Push(md[x][y])
            end for
            streak = 1
            for x = 1 to n
                if x < n and line[x] = line[x - 1] then
                    streak = streak + 1
                else
                    if streak >= 5 then score = score + 3 + (streak - 5)
                    streak = 1
                end if
            end for
            for x = 0 to n - 11
                a = true
                b = true
                for k = 0 to 10
                    if line[x + k] <> pa[k] then a = false
                    if line[x + k] <> pb[k] then b = false
                end for
                if a then score = score + 40
                if b then score = score + 40
            end for
        end for
    end for
    dark = 0
    for y = 0 to n - 1
        for x = 0 to n - 1
            c = md[y][x]
            if c then dark = dark + 1
            if x + 1 < n and y + 1 < n then
                if c = md[y][x + 1] and c = md[y + 1][x] and c = md[y + 1][x + 1] then score = score + 3
            end if
        end for
    end for
    total = n * n
    score = score + (Int(-Int(-Abs(dark * 20 - total * 10) / total)) - 1) * 10
    return score
end function

' The data codewords: byte mode, the count, the bytes, the terminator and padding.
function qr_DataFor(bytes as Object, ver as Integer) as Object
    bits = []
    qr_Put(bits, 4, 4)
    qr_Put(bits, bytes.Count(), qr_CountBits(ver))
    for each b in bytes
        qr_Put(bits, b, 8)
    end for
    capacity = qr_DataCodewords(ver) * 8
    term = capacity - bits.Count()
    if term > 4 then term = 4
    qr_Put(bits, 0, term)
    qr_Put(bits, 0, (8 - (bits.Count() mod 8)) mod 8)
    out = []
    for i = 0 to bits.Count() - 1 step 8
        v = 0
        for k = 0 to 7
            v = (v << 1) or bits[i + k]
        end for
        out.Push(v)
    end for
    pad = &hEC
    while out.Count() < capacity / 8
        out.Push(pad)
        pad = qr_Xor(pad, qr_Xor(&hEC, &h11))
    end while
    return out
end function

sub qr_Put(bits as Object, value as Integer, length as Integer)
    for i = length - 1 to 0 step -1
        bits.Push((value >> i) and 1)
    end for
end sub

' The QR code of text: rows of modules, true = dark; invalid when it is too long.
function QrEncode(text as String) as Dynamic
    ba = CreateObject("roByteArray")
    ba.FromAsciiString(text)
    bytes = []
    for i = 0 to ba.Count() - 1
        bytes.Push(ba[i])
    end for
    ver = 0
    for v = 1 to 10
        if 4 + qr_CountBits(v) + bytes.Count() * 8 <= qr_DataCodewords(v) * 8 then
            ver = v
            exit for
        end if
    end for
    if ver = 0 then return invalid
    codewords = qr_WithErrorCorrection(qr_DataFor(bytes, ver), ver)
    best = invalid
    bestScore = 0
    for mask = 0 to 7
        s = qr_Symbol(ver)
        qr_DrawFunctionPatterns(s)
        qr_DrawCodewords(s, codewords)
        qr_ApplyMask(s, mask)
        qr_DrawFormat(s, mask)
        score = qr_Penalty(s)
        if best = invalid or score < bestScore then
            bestScore = score
            best = s.modules
        end if
    end for
    return best
end function
