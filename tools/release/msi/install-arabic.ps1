# Installs the Player's .msi with its Arabic transform through Windows Installer itself (Epic 13
# story 13.14), then uninstalls it: CI's Desktop Player job runs it on the .msi packageMsi made.
# theme_msi.py --check applies the transform with MsiDatabaseApplyTransform, which lets pass what
# msiexec refuses (its error conditions, its validation against the package).
#
# A current-user install, no pages (/qn), into -Dir, with TRANSFORMS=:1025 (the transform embedded
# in the package, which Windows Installer picks by itself on an Arabic Windows). It must exit 0,
# put the Player there, and its log (/l*v: every property at the end) must say it ran in Arabic
# (ProductLanguage, which only the transform makes 1025). The uninstall must exit 0 and take the
# Player away. On a failure the log's end is printed.
#
# Usage: install-arabic.ps1 -Msi FILE [-Dir FOLDER] (PowerShell 7 or Windows PowerShell 5.1)
param(
    [Parameter(Mandatory = $true)][string]$Msi,
    [string]$Dir = ''
)
$ErrorActionPreference = 'Stop'

$Msi = (Resolve-Path -LiteralPath $Msi).ProviderPath
$work = Join-Path ([IO.Path]::GetTempPath()) ("msi-arabic-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
if ($Dir -eq '') { $Dir = Join-Path $work 'player' }
$problems = @()

# msiexec with ARGS, waited for (5 minutes at most: none of it may hang the job), its exit code.
# 1618: another install runs on this machine (the runner's own set-up), tried again.
function Invoke-Msiexec([string[]]$Arguments) {
    foreach ($try in 1..6) {
        $p = Start-Process -FilePath msiexec.exe -ArgumentList $Arguments -PassThru
        $null = $p.Handle # without it ExitCode is not kept
        if (-not $p.WaitForExit(300000)) {
            $p.Kill()
            return 'no end within 5 minutes'
        }
        if ($p.ExitCode -ne 1618) { return $p.ExitCode }
        Start-Sleep -Seconds 15
    }
    return 1618
}

function Show-Log([string]$Path, [int]$Lines = 60) {
    if (Test-Path -LiteralPath $Path) {
        Write-Host "--- the end of $(Split-Path -Leaf $Path)"
        Get-Content -LiteralPath $Path -Tail $Lines | ForEach-Object { Write-Host $_ }
    }
}

$log = Join-Path $work 'install.log'
$code = Invoke-Msiexec @('/i', "`"$Msi`"", 'TRANSFORMS=:1025', '/qn', '/norestart', '/l*v', "`"$log`"", "INSTALLDIR=`"$Dir`"")
$installed = $code -eq 0
if (-not $installed) { $problems += "msiexec /i TRANSFORMS=:1025 exited $code" }
$text = if (Test-Path -LiteralPath $log) { Get-Content -LiteralPath $log -Raw } else { '' }
[regex]::Matches($text, '(?m)^.*TRANSFORMS (property|=).*$') | Select-Object -First 5 | ForEach-Object { Write-Host $_.Value.Trim() }
if ($text -notmatch 'ProductLanguage = 1025') { $problems += 'the install did not run in Arabic (no ProductLanguage = 1025 in the log)' }
if ($installed -and -not (Test-Path -LiteralPath (Join-Path $Dir 'maktaba-player.exe'))) {
    $problems += "no maktaba-player.exe in $Dir"
}

if ($installed) {
    $unlog = Join-Path $work 'uninstall.log'
    $code = Invoke-Msiexec @('/x', "`"$Msi`"", '/qn', '/norestart', '/l*v', "`"$unlog`"")
    if ($code -ne 0) { $problems += "msiexec /x exited $code" }
    elseif (Test-Path -LiteralPath (Join-Path $Dir 'maktaba-player.exe')) { $problems += "the uninstall left maktaba-player.exe in $Dir" }
}

if ($problems.Count -gt 0) {
    Show-Log $log
    if ($installed) { Show-Log $unlog }
    foreach ($p in $problems) { Write-Host "::error::install-arabic: $(Split-Path -Leaf $Msi): $p" }
    exit 1
}
Write-Host "install-arabic: $(Split-Path -Leaf $Msi): installed with TRANSFORMS=:1025 in Arabic (ProductLanguage = 1025), and uninstalled"
Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
