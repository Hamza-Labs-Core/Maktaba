# Whether this tree's shell takes the installer hooks' flags (--server-start, --server-stop,
# --server-remove; Epic 38 story 38.3): exit 0 when it does, 1 when it does not, 2 when its
# source cannot be read. installer-hooks.nsh runs it when the installer is compiled, and
# tools/release/win-app/check.sh to know what the installer must have decided.
#
# It reads the shell's source (apps\desktop\src-tauri\src), which the same `tauri build`
# compiles into the Maktaba.exe the installer packs, and not that program: a release build
# need not hold the flags' text at all (LLVM compiles `arg == "--server-remove"` into a length
# and integer compares). A flag counts when its quoted literal is in the code of a .rs file
# (a line that is not a // comment); all three must be there. check.sh runs the installed
# Maktaba.exe with a flag and fails when what it does disagrees with this.
param([Parameter(Mandatory = $true)][string]$Source)
try {
    $files = @(Get-ChildItem -LiteralPath $Source -Recurse -File -Filter *.rs -ErrorAction Stop)
    $code = foreach ($f in $files) {
        Get-Content -LiteralPath $f.FullName -ErrorAction Stop | Where-Object { $_.TrimStart() -notlike '//*' }
    }
} catch {
    Write-Output "knows-server-flags: cannot read the shell's source in ${Source}: $_"
    exit 2
}
if ($files.Count -eq 0) {
    Write-Output "knows-server-flags: no .rs file in $Source"
    exit 2
}
foreach ($flag in '--server-start', '--server-stop', '--server-remove') {
    $quoted = '"' + $flag + '"'
    if (-not ($code | Where-Object { $_.Contains($quoted) } | Select-Object -First 1)) { exit 1 }
}
exit 0
