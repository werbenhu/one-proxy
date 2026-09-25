Add-Type -AssemblyName System.Drawing

$workspace = Split-Path -Parent $PSScriptRoot
$markColor = [System.Drawing.ColorTranslator]::FromHtml('#42d5ae')
$nodeColor = [System.Drawing.ColorTranslator]::FromHtml('#8af3d3')
$hubColor = [System.Drawing.ColorTranslator]::FromHtml('#c4ffe7')
$exitColor = [System.Drawing.ColorTranslator]::FromHtml('#e9fff3')
$background = [System.Drawing.ColorTranslator]::FromHtml('#0b1718')

function New-MarkPng([int] $size) {
    $bitmap = [System.Drawing.Bitmap]::new($size, $size)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $graphics.Clear([System.Drawing.Color]::Transparent)
    $graphics.ScaleTransform($size / 256.0, $size / 256.0)

    $box = [System.Drawing.Drawing2D.GraphicsPath]::new()
    $box.AddArc(0, 0, 108, 108, 180, 90)
    $box.AddArc(148, 0, 108, 108, 270, 90)
    $box.AddArc(148, 148, 108, 108, 0, 90)
    $box.AddArc(0, 148, 108, 108, 90, 90)
    $box.CloseFigure()
    $backgroundBrush = [System.Drawing.SolidBrush]::new($background)
    $graphics.FillPath($backgroundBrush, $box)

    $routePen = [System.Drawing.Pen]::new($markColor, 19)
    $routePen.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
    $routePen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
    $routePen.LineJoin = [System.Drawing.Drawing2D.LineJoin]::Round
    $graphics.DrawLines($routePen, [System.Drawing.PointF[]]@(
        [System.Drawing.PointF]::new(59, 70),
        [System.Drawing.PointF]::new(124, 128),
        [System.Drawing.PointF]::new(59, 186)
    ))
    $graphics.DrawLine($routePen, 124, 128, 196, 128)

    $nodeBrush = [System.Drawing.SolidBrush]::new($nodeColor)
    $graphics.FillEllipse($nodeBrush, 44, 55, 30, 30)
    $graphics.FillEllipse($nodeBrush, 44, 171, 30, 30)
    $hubPen = [System.Drawing.Pen]::new($hubColor, 11)
    $graphics.FillEllipse($backgroundBrush, 108, 112, 32, 32)
    $graphics.DrawEllipse($hubPen, 108, 112, 32, 32)
    $exitBrush = [System.Drawing.SolidBrush]::new($exitColor)
    $graphics.FillEllipse($exitBrush, 181, 113, 30, 30)

    $stream = [System.IO.MemoryStream]::new()
    $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
    $bytes = $stream.ToArray()

    $stream.Dispose()
    $exitBrush.Dispose()
    $hubPen.Dispose()
    $nodeBrush.Dispose()
    $routePen.Dispose()
    $backgroundBrush.Dispose()
    $box.Dispose()
    $graphics.Dispose()
    $bitmap.Dispose()
    return ,$bytes
}

function Write-Icon([string] $path, [int[]] $sizes) {
    $frames = @($sizes | ForEach-Object { New-MarkPng $_ })
    $stream = [System.IO.File]::Create($path)
    $writer = [System.IO.BinaryWriter]::new($stream)
    $writer.Write([UInt16]0)
    $writer.Write([UInt16]1)
    $writer.Write([UInt16]$sizes.Count)
    $offset = 6 + 16 * $sizes.Count
    for ($i = 0; $i -lt $sizes.Count; $i++) {
        $size = $sizes[$i]
        $writer.Write([byte]($size % 256))
        $writer.Write([byte]($size % 256))
        $writer.Write([byte]0)
        $writer.Write([byte]0)
        $writer.Write([UInt16]1)
        $writer.Write([UInt16]32)
        $writer.Write([UInt32]$frames[$i].Length)
        $writer.Write([UInt32]$offset)
        $offset += $frames[$i].Length
    }
    foreach ($frame in $frames) { $writer.Write([byte[]]$frame) }
    $writer.Dispose()
}

[System.IO.File]::WriteAllBytes((Join-Path $workspace 'build/appicon.png'), (New-MarkPng 512))
[System.IO.File]::WriteAllBytes((Join-Path $workspace 'frontend/src/assets/oneproxy-icon.png'), (New-MarkPng 64))
Write-Icon (Join-Path $workspace 'build/windows/icon.ico') @(16, 32, 48, 64, 128, 256)
Write-Icon (Join-Path $workspace 'tray.ico') @(16, 32, 48, 64, 128, 256)
