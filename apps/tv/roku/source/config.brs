' The channel's identity and fixed addresses (Epic 58 story 58.12).

' The channel's version, from the manifest: "<major>.<minor>.<build>".
function AppVersion() as String
    info = CreateObject("roAppInfo")
    if info = invalid then return "0.0.0"
    v = info.GetVersion()
    if v = invalid or v = "" then return "0.0.0"
    return v
end function

' The X-Maktaba-Client header every request carries: tv/<version> (roku; tv).
function ClientHeader() as String
    return "tv/" + AppVersion() + " (roku; tv)"
end function

' Maktaba Cloud's API (the cloud code of story 58.03). The manifest's maktaba_cloud overrides
' it for the tests' mock cloud; a store build leaves it out.
function CloudBase() as String
    info = CreateObject("roAppInfo")
    if info <> invalid then
        v = info.GetValue("maktaba_cloud")
        if v <> invalid and v <> "" then return v
    end if
    return "https://api.maktaba.tv"
end function

' Every request trusts Roku's stock bundle: a home's per-home certificate is publicly trusted
' (story 50.15).
function CertificatesFile() as String
    return "common:/certs/ca-bundle.crt"
end function
