$ErrorActionPreference='Stop'
$project=(Resolve-Path "$PSScriptRoot/..").Path
$out=Join-Path $project 'build/release/routerich-ax3000'
[void][IO.Directory]::CreateDirectory($out)
$utf8=[Text.UTF8Encoding]::new($false)
function Get-PackageVersion([string]$name) {
    $recipe=[IO.File]::ReadAllText((Join-Path $project "openwrt/$name/Makefile"))
    $version=[regex]::Match($recipe,'(?m)^PKG_VERSION:=([0-9.]+)\s* @('24.10.8','25.12.6')){
    $folder=Join-Path $project "build/candidate-filogic-$version"
    $files=[ordered]@{}
    if($version.StartsWith('25')){
        $names=@("sota-gateway-$gateway.apk","luci-app-sota-$luci.apk",'sota-lab.pem')
    } else {
        $names=@("sota-gateway_${gateway}_aarch64_cortex-a53.ipk","luci-app-sota_${luci}_all.ipk")
    }
    foreach($name in $names){$files[$name]=Join-Path $folder $name}
    $files['INSTALL.md']=Join-Path $project 'openwrt/ROUTERICH.md'
    $manifest=foreach($name in $files.Keys){
        '{0}  {1}' -f (Get-FileHash -LiteralPath $files[$name] -Algorithm SHA256).Hash.ToLowerInvariant(),$name
    }
    $zipName="Sota-Gateway-$revision-Routerich-AX3000-OpenWrt-$version.zip"
    $zipPath=Join-Path $out $zipName
    $temp=Join-Path $out ([IO.Path]::GetRandomFileName())
    $zip=[IO.Compression.ZipFile]::Open($temp,'Create')
    try{
        foreach($name in $files.Keys){
            [void][IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,$files[$name],$name,'Optimal')
        }
        $writer=[IO.StreamWriter]::new($zip.CreateEntry('SHA256SUMS').Open(),$utf8)
        try{$writer.Write(($manifest -join [char]10)+[char]10)}finally{$writer.Dispose()}
    }finally{$zip.Dispose()}
    $zip=[IO.Compression.ZipFile]::OpenRead($temp)
    try{
        if($zip.Entries.Count -ne ($files.Count+1)){throw 'Unexpected ZIP contents'}
        $reader=[IO.StreamReader]::new($zip.GetEntry('SHA256SUMS').Open(),$utf8)
        try{$lines=$reader.ReadToEnd().TrimEnd().Split([char]10)}finally{$reader.Dispose()}
        foreach($line in $lines){
            $hash,$name=$line -split '  ',2
            $stream=$zip.GetEntry($name).Open()
            $sha=[Security.Cryptography.SHA256]::Create()
            try{$actual=[Convert]::ToHexString($sha.ComputeHash($stream))}finally{$sha.Dispose();$stream.Dispose()}
            if($actual -ne $hash){throw "ZIP verification failed: $name"}
        }
    }finally{$zip.Dispose()}
    [IO.File]::Move($temp,$zipPath,$true)
    $item=Get-Item -LiteralPath $zipPath
    [pscustomobject]@{Bundle=$item.Name;MiB=[math]::Round($item.Length/1MB,2);SHA256=(Get-FileHash -LiteralPath $zipPath).Hash.ToLowerInvariant()} | ConvertTo-Json -Compress
}).Groups[1].Value
    $release=[regex]::Match($recipe,'(?m)^PKG_RELEASE:=(\d+)\s* @('24.10.8','25.12.6')){
    $folder=Join-Path $project "build/candidate-filogic-$version"
    $files=[ordered]@{}
    if($version.StartsWith('25')){
        $names=@('sota-gateway-0.1.0-r19.apk','luci-app-sota-0.1.0-r15.apk','sota-lab.pem')
    } else {
        $names=@('sota-gateway_0.1.0-r19_aarch64_cortex-a53.ipk','luci-app-sota_0.1.0-r15_all.ipk')
    }
    foreach($name in $names){$files[$name]=Join-Path $folder $name}
    $files['INSTALL.md']=Join-Path $project 'openwrt/ROUTERICH.md'
    $manifest=foreach($name in $files.Keys){
        '{0}  {1}' -f (Get-FileHash -LiteralPath $files[$name] -Algorithm SHA256).Hash.ToLowerInvariant(),$name
    }
    $zipName="Sota-Gateway-r19-Routerich-AX3000-OpenWrt-$version.zip"
    $zipPath=Join-Path $out $zipName
    $temp=Join-Path $out ([IO.Path]::GetRandomFileName())
    $zip=[IO.Compression.ZipFile]::Open($temp,'Create')
    try{
        foreach($name in $files.Keys){
            [void][IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,$files[$name],$name,'Optimal')
        }
        $writer=[IO.StreamWriter]::new($zip.CreateEntry('SHA256SUMS').Open(),$utf8)
        try{$writer.Write(($manifest -join [char]10)+[char]10)}finally{$writer.Dispose()}
    }finally{$zip.Dispose()}
    $zip=[IO.Compression.ZipFile]::OpenRead($temp)
    try{
        if($zip.Entries.Count -ne ($files.Count+1)){throw 'Unexpected ZIP contents'}
        $reader=[IO.StreamReader]::new($zip.GetEntry('SHA256SUMS').Open(),$utf8)
        try{$lines=$reader.ReadToEnd().TrimEnd().Split([char]10)}finally{$reader.Dispose()}
        foreach($line in $lines){
            $hash,$name=$line -split '  ',2
            $stream=$zip.GetEntry($name).Open()
            try{$actual=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($stream))}finally{$stream.Dispose()}
            if($actual -ne $hash){throw "ZIP verification failed: $name"}
        }
    }finally{$zip.Dispose()}
    [IO.File]::Move($temp,$zipPath,$true)
    $item=Get-Item -LiteralPath $zipPath
    [pscustomobject]@{Bundle=$item.Name;MiB=[math]::Round($item.Length/1MB,2);SHA256=(Get-FileHash -LiteralPath $zipPath).Hash.ToLowerInvariant()} | ConvertTo-Json -Compress
}).Groups[1].Value
    if (!$version -or !$release) { throw "Invalid package version in $name/Makefile" }
    return "$version-r$release"
}
$gateway=Get-PackageVersion 'sota-gateway'
$luci=Get-PackageVersion 'luci-app-sota'
$revision=$gateway.Split('-')[-1]
foreach($version in @('24.10.8','25.12.6')){
    $folder=Join-Path $project "build/candidate-filogic-$version"
    $files=[ordered]@{}
    if($version.StartsWith('25')){
        $names=@('sota-gateway-0.1.0-r19.apk','luci-app-sota-0.1.0-r15.apk','sota-lab.pem')
    } else {
        $names=@('sota-gateway_0.1.0-r19_aarch64_cortex-a53.ipk','luci-app-sota_0.1.0-r15_all.ipk')
    }
    foreach($name in $names){$files[$name]=Join-Path $folder $name}
    $files['INSTALL.md']=Join-Path $project 'openwrt/ROUTERICH.md'
    $manifest=foreach($name in $files.Keys){
        '{0}  {1}' -f (Get-FileHash -LiteralPath $files[$name] -Algorithm SHA256).Hash.ToLowerInvariant(),$name
    }
    $zipName="Sota-Gateway-r19-Routerich-AX3000-OpenWrt-$version.zip"
    $zipPath=Join-Path $out $zipName
    $temp=Join-Path $out ([IO.Path]::GetRandomFileName())
    $zip=[IO.Compression.ZipFile]::Open($temp,'Create')
    try{
        foreach($name in $files.Keys){
            [void][IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,$files[$name],$name,'Optimal')
        }
        $writer=[IO.StreamWriter]::new($zip.CreateEntry('SHA256SUMS').Open(),$utf8)
        try{$writer.Write(($manifest -join [char]10)+[char]10)}finally{$writer.Dispose()}
    }finally{$zip.Dispose()}
    $zip=[IO.Compression.ZipFile]::OpenRead($temp)
    try{
        if($zip.Entries.Count -ne ($files.Count+1)){throw 'Unexpected ZIP contents'}
        $reader=[IO.StreamReader]::new($zip.GetEntry('SHA256SUMS').Open(),$utf8)
        try{$lines=$reader.ReadToEnd().TrimEnd().Split([char]10)}finally{$reader.Dispose()}
        foreach($line in $lines){
            $hash,$name=$line -split '  ',2
            $stream=$zip.GetEntry($name).Open()
            try{$actual=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($stream))}finally{$stream.Dispose()}
            if($actual -ne $hash){throw "ZIP verification failed: $name"}
        }
    }finally{$zip.Dispose()}
    [IO.File]::Move($temp,$zipPath,$true)
    $item=Get-Item -LiteralPath $zipPath
    [pscustomobject]@{Bundle=$item.Name;MiB=[math]::Round($item.Length/1MB,2);SHA256=(Get-FileHash -LiteralPath $zipPath).Hash.ToLowerInvariant()} | ConvertTo-Json -Compress
}

