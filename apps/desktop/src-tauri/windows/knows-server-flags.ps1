# Whether a Maktaba.exe takes the installer hooks' flags (--server-start, --server-stop,
# --server-remove; Epic 38 story 38.3): exit 0 when it does, 1 when it does not, 2 when it
# cannot be read. installer-hooks.nsh runs it when the installer is compiled, on the very
# Maktaba.exe it packs. A shell parses the flags by their names, so a build that takes them
# holds the literal; one from before 38.3 (the client-only app) does not, and opens its
# window for any argument instead.
param([Parameter(Mandatory = $true)][string]$Exe)
try {
    $bytes = [System.IO.File]::ReadAllBytes($Exe)
} catch {
    Write-Output "knows-server-flags: cannot read ${Exe}: $_"
    exit 2
}
# ISO-8859-1 maps every byte to one character, so the ASCII literal is found wherever it is
$text = [System.Text.Encoding]::GetEncoding(28591).GetString($bytes)
if ($text.Contains('--server-remove')) { exit 0 }
exit 1
