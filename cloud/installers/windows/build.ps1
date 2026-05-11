# Build the Windows MSI for maktaba-server. Run from CI with the
# Authenticode signing certificate on the YubiKey reachable via
# signtool / smctl.
#
# Env vars:
#   MAKTABA_WIN_SIGN_THUMB    SHA-1 thumbprint of the signing cert
#   MAKTABA_VERSION           e.g. 0.1.0

param(
    [string]$Version = $env:MAKTABA_VERSION,
    [string]$Thumbprint = $env:MAKTABA_WIN_SIGN_THUMB
)

if (-not $Version) { $Version = "0.1.0-dev" }

$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -o "dist\maktaba-server.exe" `
    -ldflags "-X main.Version=$Version" `
    .\api\

if ($Thumbprint) {
    signtool sign /sha1 $Thumbprint /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 "dist\maktaba-server.exe"
} else {
    Write-Warning "MAKTABA_WIN_SIGN_THUMB unset; skipping Authenticode signing"
}

# Compose the MSI using WiX. WiX must be on PATH.
candle.exe -arch x64 -dVersion=$Version installer.wxs -o "dist\installer.wixobj"
light.exe -ext WixUtilExtension "dist\installer.wixobj" -o "dist\maktaba-server-$Version-windows-amd64.msi"

if ($Thumbprint) {
    signtool sign /sha1 $Thumbprint /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 "dist\maktaba-server-$Version-windows-amd64.msi"
}

Write-Host "Built dist\maktaba-server-$Version-windows-amd64.msi"
