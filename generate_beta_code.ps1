# generate_beta_code.ps1
param(
    [int]$Count = 1,
    [string]$FilePath = "beta_codes.txt",
    [switch]$ListOnly,
    [switch]$Help
)

# Character set (avoid confusing characters: 0/O, 1/I/l)
$Charset = "23456789abcdefghjkmnpqrstuvwxyz"
$CodeLength = 6

# Display help information
function Show-Help {
    @"
Usage: $($MyInvocation.MyCommand.Name) [options]

Options:
    -Count COUNT      Number of beta codes to generate (default: 1)
    -ListOnly         List existing beta codes
    -FilePath FILE    Beta codes file path (default: beta_codes.txt)
    -Help             Show this help information

Examples:
    .\$($MyInvocation.MyCommand.Name) -Count 10                   # Generate 10 beta codes
    .\$($MyInvocation.MyCommand.Name) -ListOnly                  # List existing beta codes
    .\$($MyInvocation.MyCommand.Name) -FilePath custom.txt -Count 5  # Generate 5 beta codes in custom file
"@
}

if ($Help) {
    Show-Help
    exit 0
}

# Generate random beta code
function Generate-BetaCode {
    param (
        [int]$Length,
        [string]$CharsetStr
    )

    $code = ""
    for ($i = 0; $i -lt $Length; $i++) {
        $randomIndex = Get-Random -Minimum 0 -Maximum $CharsetStr.Length
        $code += $CharsetStr[$randomIndex]
    }
    return $code
}

# Read existing beta codes
function Read-ExistingCodes {
    param ([string]$File)

    if (Test-Path $File) {
        $content = Get-Content $File | Where-Object { $_.Trim() -ne "" -and -not $_.StartsWith("#") }
        return $content
    }
    return @()
}

# Check if beta code already exists
function Test-CodeExists {
    param (
        [string]$Code,
        [string]$File
    )

    if (Test-Path $File) {
        $existingCodes = Get-Content $File
        return $existingCodes -contains $Code
    }
    return $false
}

# Add beta code to file
function Add-CodeToFile {
    param (
        [string]$Code,
        [string]$File
    )

    Add-Content -Path $File -Value $Code
}

# Validate beta code format
function Confirm-CodeFormat {
    param ([string]$Code)

    # Check length
    if ($Code.Length -ne $CodeLength) {
        return $false
    }
    # Check if all characters are in the allowed character set
    foreach ($char in $Code.ToCharArray()) {
        if (-not $Charset.Contains($char)) {
            return $false
        }
    }
    return $true
}

# Deduplicate and sort beta codes
function Optimize-CodesFile {
    param ([string]$File)

    if (Test-Path $File) {
        $lines = Get-Content $File | Where-Object { $_.Trim() -ne "" -and -not $_.StartsWith("#") } | Sort-Object -Unique
        Set-Content -Path $File -Value $lines
    }
}

# If listing existing beta codes
if ($ListOnly) {
    if (Test-Path $FilePath) {
        $existingCodes = Read-ExistingCodes -File $FilePath
        if ($existingCodes.Count -eq 0) {
            Write-Host "Beta code list is empty"
        } else {
            Write-Host "Current beta codes ($($existingCodes.Count) total):"
            for ($i = 0; $i -lt $existingCodes.Count; $i++) {
                Write-Host ("{0,3}. {1}" -f ($i + 1), $existingCodes[$i])
            }
        }
    } else {
        Write-Host "Beta codes file does not exist: $FilePath"
    }
    exit 0
}

# Read existing beta codes
$existingCodes = Read-ExistingCodes -File $FilePath

# Generate new beta codes
$newCodes = @()
$maxAttempts = 1000  # Prevent infinite loop

Write-Host "Generating $Count beta codes..."

for ($i = 1; $i -le $Count; $i++) {
    $attempts = 0
    $duplicateFound = $false

    while ($attempts -lt $maxAttempts) {
        $code = Generate-BetaCode -Length $CodeLength -CharsetStr $Charset

        # Validate format
        if (-not (Confirm-CodeFormat -Code $code)) {
            $attempts++
            continue
        }

        # Check if already exists
        if (Test-CodeExists -Code $code -File $FilePath) {
            $attempts++
            continue
        }

        # Check if duplicate with codes generated in this session
        if ($newCodes -contains $code) {
            $attempts++
            continue
        }

        # Successfully generated a unique beta code
        $newCodes += $code
        break
    }

    if ($attempts -eq $maxAttempts) {
        Write-Warning "Reached maximum attempts when generating beta code #$i, character space may be insufficient"
        break
    }
}

# Check if any beta codes were successfully generated
if ($newCodes.Count -eq 0) {
    Write-Host "Failed to generate any new beta codes"
    exit 1
}

# Add to file
foreach ($code in $newCodes) {
    Add-CodeToFile -Code $code -File $FilePath
}

# Deduplicate and sort
Optimize-CodesFile -File $FilePath

Write-Host "Successfully generated $($newCodes.Count) beta codes:"
foreach ($code in $newCodes) {
    Write-Host "  $code"
}
Write-Host ""
Write-Host "Beta codes file: $FilePath"

# Display current total count
if (Test-Path $FilePath) {
    $totalCount = (Read-ExistingCodes -File $FilePath).Count
    Write-Host "Total beta codes: $totalCount"
}

# Display file header information (if it's a new file)
if ((-not (Test-Path $FilePath)) -or ((Get-Content $FilePath).Length -eq $newCodes.Count)) {
    Write-Host ""
    Write-Host "Beta code rules:"
    Write-Host "- Length: $CodeLength characters"
    Write-Host "- Character set: Numbers 2-9, lowercase letters a-z (excluding 0,1,i,l,o to avoid confusion)"
    Write-Host "- Each beta code is unique and non-repetitive"
}
