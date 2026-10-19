# Tests for where install-node.ps1 takes the pipeline from (Resolve-NodePipelineOrigin)
# and how it gets the files (Get-NodePipelineFile), in node-common.ps1. Plain asserts, no
# Pester: runs under Windows PowerShell 5.1 and PowerShell 7, on Windows or elsewhere.
#
#   pwsh -NoProfile -File deploy/packaging/node/tests/node-origin.Tests.ps1
$ErrorActionPreference = 'Stop'

# node-common.ps1 names the per-user folders when it loads; give them somewhere to be.
$tmpRoot = [System.IO.Path]::GetTempPath()
$T = Join-Path $tmpRoot ('node-origin-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $T | Out-Null
foreach ($name in @('APPDATA', 'LOCALAPPDATA', 'SystemRoot')) {
    if (-not [System.Environment]::GetEnvironmentVariable($name)) {
        [System.Environment]::SetEnvironmentVariable($name, (Join-Path $T $name), 'Process')
    }
}
. (Join-Path (Split-Path -Parent $PSScriptRoot) 'windows/node-common.ps1')
Set-StrictMode -Off   # the asserts below poke at hashtables freely

$script:Pass = 0
$script:Fail = 0
function Ok([string]$What) { $script:Pass++; Write-Host "ok    $What" }
function Bad([string]$What) { $script:Fail++; Write-Host "FAIL  $What" -ForegroundColor Red }
function Assert-Equal($Expected, $Actual, [string]$What) {
    if ($Expected -eq $Actual) { Ok $What } else { Bad "${What}: expected '$Expected', got '$Actual'" }
}
function Assert-Throws([scriptblock]$Block, [string]$Pattern, [string]$What) {
    try {
        & $Block | Out-Null
        Bad "${What}: did not throw"
    } catch {
        if ($_.Exception.Message -like "*$Pattern*") { Ok "${What}: refused ($Pattern)" }
        else { Bad "${What}: threw '$($_.Exception.Message)', expected '*$Pattern*'" }
    }
}
function Get-Sha256([string]$Path) { (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }

# --- fixtures -------------------------------------------------------------------------------
$files = Join-Path $T 'files'
New-Item -ItemType Directory -Path $files | Out-Null
$wheel = Join-Path $files 'maktaba_pipeline-9.8.7-py3-none-any.whl'
$cons = Join-Path $files 'maktaba-pipeline-9.8.7-constraints.txt'
[System.IO.File]::WriteAllText($wheel, "not really a wheel`n")
[System.IO.File]::WriteAllText($cons, "structlog==25.1.0`n")
$checkout = Join-Path $T 'checkout\pipeline'
$noConf = Join-Path $T 'none\install.conf'

function New-Bundle([string]$Dir, [switch]$WithFiles) {
    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    if ($WithFiles) {
        Copy-Item -LiteralPath $wheel -Destination $Dir
        Copy-Item -LiteralPath $cons -Destination $Dir
    }
    $conf = "TAG=v9.8.7`nVERSION=9.8.7`nREPO=example/Maktaba`nWHEEL=$(Split-Path -Leaf $wheel)`n" +
        "WHEEL_SHA256=$(Get-Sha256 $wheel)`nCONSTRAINTS=$(Split-Path -Leaf $cons)`n" +
        "CONSTRAINTS_SHA256=$(Get-Sha256 $cons)`nPYTHON=3.13`n"
    [System.IO.File]::WriteAllText((Join-Path $Dir 'release.conf'), $conf)
}
$bundle = Join-Path $T 'bundle'
New-Bundle $bundle -WithFiles
$bare = Join-Path $T 'bare'
New-Bundle $bare
$plain = Join-Path $T 'plain'     # an installer folder with no release.conf
New-Item -ItemType Directory -Path $plain | Out-Null

function New-InstallConf([string]$Text) {
    $p = Join-Path $T ('conf-' + [guid]::NewGuid().ToString('N') + '.conf')
    [System.IO.File]::WriteAllText($p, $Text)
    return $p
}

try {
    # --- release archive -------------------------------------------------------------------------
    $o = Resolve-NodePipelineOrigin -BundleDir $bundle -CheckoutPipeline $checkout -InstallConf $noConf
    Assert-Equal 'release' $o.From 'release archive: FROM=release'
    Assert-Equal (Join-Path $bundle (Split-Path -Leaf $wheel)) $o.Wheel 'release archive: the bundled wheel'
    Assert-Equal (Join-Path $bundle (Split-Path -Leaf $cons)) $o.Constraints 'release archive: the bundled constraints'
    Assert-Equal (Get-Sha256 $wheel) $o.WheelSha256 'release archive: the wheel''s digest'
    Assert-Equal '3.13' $o.Python 'release archive: its Python'
    Assert-Equal 'v9.8.7' $o.Tag 'release archive: its tag'

    $o = Resolve-NodePipelineOrigin -BundleDir $bare -CheckoutPipeline $checkout -InstallConf $noConf
    Assert-Equal 'https://github.com/example/Maktaba/releases/download/v9.8.7/maktaba_pipeline-9.8.7-py3-none-any.whl' $o.Wheel 'bare archive: the release''s wheel URL'
    Assert-Equal 'https://github.com/example/Maktaba/releases/download/v9.8.7/maktaba-pipeline-9.8.7-constraints.txt' $o.Constraints 'bare archive: the release''s constraints URL'

    $o = Resolve-NodePipelineOrigin -BundleDir $bundle -Constraints $cons -CheckoutPipeline $checkout -InstallConf $noConf
    Assert-Equal $cons $o.Constraints 'release archive: -Constraints wins'
    Assert-Equal '' $o.ConstraintsSha256 'release archive: no digest for a given constraints file'

    # --- -FromWheel ----------------------------------------------------------------------------------
    $o = Resolve-NodePipelineOrigin -FromWheel $wheel -BundleDir $bundle -CheckoutPipeline $checkout -InstallConf $noConf
    Assert-Equal 'wheel' $o.From '-FromWheel beats release.conf'
    Assert-Equal $wheel $o.Wheel '-FromWheel: that wheel'
    Assert-Equal '' $o.Constraints '-FromWheel: no constraints unless given'
    Assert-Equal '3.12' $o.Python '-FromWheel: Python 3.12 by default'
    $url = 'https://example.com/dl/maktaba_pipeline-1.0.0-py3-none-any.whl?x=1'
    $o = Resolve-NodePipelineOrigin -FromWheel $url -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $noConf
    Assert-Equal $url $o.Wheel '-FromWheel URL with a query'
    Assert-Equal 'maktaba_pipeline-1.0.0-py3-none-any.whl' (Get-NodeSpecLeaf $url) 'a URL''s file name drops the query'

    # --- refusals ------------------------------------------------------------------------------------
    Assert-Throws { Resolve-NodePipelineOrigin -FromWheel $wheel -Source $checkout -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $noConf } 'give one' '-FromWheel with -Source'
    Assert-Throws { Resolve-NodePipelineOrigin -Constraints $cons -Source $checkout -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $noConf } '-Constraints goes with -FromWheel' '-Constraints with -Source'
    Assert-Throws { Resolve-NodePipelineOrigin -FromWheel $cons -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $noConf } 'takes a wheel' '-FromWheel of a non-wheel'
    Assert-Throws { Resolve-NodePipelineOrigin -FromWheel (Join-Path $files 'other_pkg-1.0-py3-none-any.whl') -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $noConf } 'not a maktaba-pipeline wheel' '-FromWheel of another package'

    # --- the checkout and the last install's choice ------------------------------------------------------
    $o = Resolve-NodePipelineOrigin -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $noConf
    Assert-Equal 'source' $o.From 'checkout: FROM=source'
    Assert-Equal $checkout $o.Source 'checkout: its pipeline\'

    $conf = New-InstallConf "FROM=wheel`nEXTRAS=none`nWHEEL=$wheel`nCONSTRAINTS=$cons`nMODE=Task`n"
    $o = Resolve-NodePipelineOrigin -BundleDir $bundle -CheckoutPipeline $checkout -InstallConf $conf
    Assert-Equal $wheel $o.Wheel 'update: a remembered wheel beats release.conf'
    Assert-Equal $cons $o.Constraints 'update: with its constraints'

    $conf = New-InstallConf "EXTRAS=none`nSOURCE=C:\src\pipeline`nMODE=Task`n"
    $o = Resolve-NodePipelineOrigin -BundleDir $bundle -CheckoutPipeline $checkout -InstallConf $conf
    Assert-Equal 'source' $o.From 'update: an install from before -FromWheel keeps its source'
    Assert-Equal 'C:\src\pipeline' $o.Source 'update: that source'

    $conf = New-InstallConf "FROM=release`nEXTRAS=none`n"
    $o = Resolve-NodePipelineOrigin -BundleDir $bundle -CheckoutPipeline $checkout -InstallConf $conf
    Assert-Equal (Join-Path $bundle (Split-Path -Leaf $wheel)) $o.Wheel 'update: FROM=release takes this archive''s release'
    Assert-Throws { Resolve-NodePipelineOrigin -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $conf } 'not from one' 'update: FROM=release from a checkout'
    $o = Resolve-NodePipelineOrigin -Source $checkout -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $conf
    Assert-Equal 'source' $o.From 'update: -Source switches a release install to the checkout'

    $conf = New-InstallConf "FROM=wheel`n"
    Assert-Throws { Resolve-NodePipelineOrigin -BundleDir $plain -CheckoutPipeline $checkout -InstallConf $conf } 'names no WHEEL' 'install.conf with FROM=wheel and no wheel'

    # --- Get-NodePipelineFile ------------------------------------------------------------------------------
    $dl = Join-Path $T 'dl'
    Assert-Equal (Resolve-Path -LiteralPath $wheel).Path (Get-NodePipelineFile -Spec $wheel -Sha256 (Get-Sha256 $wheel) -DownloadDir $dl) 'a local file with the right digest'
    Assert-Throws { Get-NodePipelineFile -Spec $wheel -Sha256 ('0' * 64) -DownloadDir $dl } 'SHA-256' 'a local file with the wrong digest'
    Assert-Throws { Get-NodePipelineFile -Spec (Join-Path $files 'missing.whl') -DownloadDir $dl } 'no such file' 'a missing file'
} finally {
    Remove-Item -LiteralPath $T -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "$($script:Pass) passed, $($script:Fail) failed"
if ($script:Fail -ne 0) { exit 1 }
