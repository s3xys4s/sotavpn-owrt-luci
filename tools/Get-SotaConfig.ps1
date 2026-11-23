[CmdletBinding()]
param(
    [ValidateSet('meowconnect.com', 'meow5g.com')]
    [string]$ApiHost = 'meowconnect.com',
    [int]$GateId = -1,
    [string]$DataDirectory = (Join-Path $PSScriptRoot 'sota-probe-data')
)

# Fetch one Sota configuration; does not start a tunnel or modify routing.
# The subscription key is read interactively and is never written to a file.
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Net.Http
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$utf8 = New-Object System.Text.UTF8Encoding($false)
[IO.Directory]::CreateDirectory($DataDirectory) | Out-Null
$DataDirectory = [IO.Path]::GetFullPath($DataDirectory)
$deviceFile = Join-Path $DataDirectory 'device.json'

if (Test-Path -LiteralPath $deviceFile) {
    $device = Get-Content -LiteralPath $deviceFile -Raw | ConvertFrom-Json
    $hwid = [string]$device.hwid
    if ($hwid -notmatch '^[0-9a-f]{64}$') {
        throw 'Invalid device.json: expected a 64-character hexadecimal HWID. No new ID was generated.'
    }
} else {
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $bytes = New-Object byte[] 32
        $random.GetBytes($bytes)
        $hwid = ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
    } finally { $random.Dispose() }
    $device = [ordered]@{ hwid = $hwid; createdAt = [DateTime]::UtcNow.ToString('o') }
    [IO.File]::WriteAllText($deviceFile, ($device | ConvertTo-Json), $utf8)
}

$secret = Read-Host 'Sota subscription key (hidden)' -AsSecureString
$pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secret)
try { $accessKey = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer).Trim() }
finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer); $secret.Dispose() }
if ([string]::IsNullOrWhiteSpace($accessKey)) { throw 'Subscription key is empty.' }

$runDirectory = Join-Path $DataDirectory ([DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffZ'))
[IO.Directory]::CreateDirectory($runDirectory) | Out-Null
$handler = New-Object Net.Http.HttpClientHandler
$handler.AllowAutoRedirect = $false
$client = New-Object Net.Http.HttpClient($handler)
$client.Timeout = [TimeSpan]::FromSeconds(25)
$client.DefaultRequestHeaders.Add('X-Access-Key', $accessKey)
$client.DefaultRequestHeaders.Add('X-HWID', $hwid)
$client.DefaultRequestHeaders.TryAddWithoutValidation('User-Agent', 'Sota Connect (v1.8.11/windows)') | Out-Null
$client.DefaultRequestHeaders.Accept.ParseAdd('application/json')
$accessKey = $null

function Get-SotaResponse([string]$ApiPath, [string]$FileName) {
    $url = 'https://' + $ApiHost + '/api/v1/public' + $ApiPath
    Write-Host ('GET ' + $ApiPath)
    try { $response = $client.GetAsync($url).GetAwaiter().GetResult() }
    catch {
        throw ('Transport failure for ' + $ApiHost + ': ' + $_.Exception.GetBaseException().Message)
    }
    try {
        $body = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        $savedPath = Join-Path $runDirectory $FileName
        [IO.File]::WriteAllText($savedPath, $body, $utf8)
        Write-Host ('HTTP ' + [int]$response.StatusCode + '; response saved: ' + $savedPath)
        if (-not $response.IsSuccessStatusCode) {
            throw ('API refused the request: HTTP ' + [int]$response.StatusCode + '. See the saved response. Redirects are not followed.')
        }
        try { $parsed = ConvertFrom-Json -InputObject $body }
        catch { throw ('API response is not JSON. See ' + $savedPath) }
        return [pscustomobject]@{ Value = $parsed; Path = $savedPath }
    } finally { $response.Dispose() }
}

try {
    Write-Host ('HWID: ' + $hwid)
    $list = Get-SotaResponse '/connection/list' 'list-response.json'
    $locations = $list.Value
    if ($locations -isnot [Array] -or $locations.Count -eq 0) {
        throw 'Unexpected or empty location list. Raw response was saved; no connect request was sent.'
    }
    if ($GateId -ge 0) {
        $selected = $locations | Where-Object { [string]$_.id -eq [string]$GateId } | Select-Object -First 1
    } else {
        $selected = $locations | Where-Object { $_.shortname -eq 'NL' -or $_.name -match 'Netherlands' } | Select-Object -First 1
        if ($null -eq $selected) { $selected = $locations | Select-Object -First 1 }
    }
    if ($null -eq $selected -or [string]$selected.id -notmatch '^\d+$') {
        throw 'No matching location ID. Inspect list-response.json and run with -GateId NUMBER.'
    }
    Write-Host ('Location: ' + $selected.name + '; gate_id=' + $selected.id)
    $reply = Get-SotaResponse ('/connection/connect?gate_id=' + $selected.id) 'connect-response.json'
    $config = $reply.Value.configuration
    if ($null -eq $config) { $config = $reply.Value }
    if ($config -is [string]) { $config = ConvertFrom-Json -InputObject $config }
    if ($null -eq $config.outbounds) {
        throw 'Response has no recognized outbounds. Raw response was saved for inspection.'
    }
    $configFile = Join-Path $runDirectory 'configuration.json'
    [IO.File]::WriteAllText($configFile, ($config | ConvertTo-Json -Depth 100), $utf8)
    $summary = [ordered]@{
        apiHost = $ApiHost
        fetchedAt = [DateTime]::UtcNow.ToString('o')
        hwid = $hwid
        gateId = $selected.id
        location = $selected.name
        responseFields = @($reply.Value.PSObject.Properties.Name)
        outbounds = @($config.outbounds | ForEach-Object {
            [ordered]@{ type = $_.type; tag = $_.tag; flow = $_.flow; tls = [bool]$_.tls.enabled; reality = [bool]$_.tls.reality.enabled }
        })
        tunnelTested = $false
    }
    [IO.File]::WriteAllText((Join-Path $runDirectory 'summary.json'), ($summary | ConvertTo-Json -Depth 10), $utf8)
    Write-Host ('Configuration saved: ' + $configFile)
    Write-Host ('Outbound types: ' + (($config.outbounds | ForEach-Object { $_.type }) -join ', '))
    Write-Host 'API retrieval succeeded. VPN traffic has not been tested.'
} finally {
    $client.Dispose()
    $handler.Dispose()
    $accessKey = $null
}
