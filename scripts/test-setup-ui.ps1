param(
    # Inno scales its wizard from the dialog font. 8 is the design size; 10, 12
    # and 16 are the 125%, 150% and 200% font steps used to stand in for those
    # DPI layouts without changing the system DPI.
    [int[]]$DialogFontSizes = @(8, 10, 12, 16),
    # Proves the fixed TEMP fixture lock without compiling or launching Setup.
    [switch]$FixtureLockSelfTest
)

# Drives a real themed RoboRev-Dashboard-Setup.exe. Every installer is compiled
# from installer\RoboRevDashboard.iss with its own AppId and a /DIR under TEMP.
# The payload is a synthetic executable that exits immediately unless this
# script starts it with --serve. Nothing here opens the production install
# path. The fixed TEMP fixture is exclusively locked; only that owner inspects,
# stops, or deletes it.
$ErrorActionPreference = 'Stop'

function Get-SetupUiFixtureLockName {
    param([Parameter(Mandatory)][string]$Root)
    $normalized = $Root.TrimEnd('\').ToLowerInvariant()
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($normalized))
    } finally { $sha.Dispose() }
    $hash = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
    return ('Global\RoboRevDashboard.SetupUi.' + $hash)
}

if ($FixtureLockSelfTest) {
    $fixed = Join-Path ([IO.Path]::GetTempPath()) 'roborev-dashboard-setup-ui'
    $fixedName = Get-SetupUiFixtureLockName -Root $fixed
    if ($fixedName -cnotmatch '^Global\\RoboRevDashboard\.SetupUi\.[0-9a-f]{64}$') { throw "Unexpected fixture lock name: $fixedName" }
    $again = Get-SetupUiFixtureLockName -Root ($fixed.TrimEnd('\') + '\')
    if ($fixedName -cne $again) { throw 'Fixture lock name changed when the directory slash changed.' }
    $other = Get-SetupUiFixtureLockName -Root (Join-Path ([IO.Path]::GetTempPath()) 'roborev-dashboard-setup-ui-other')
    if ($fixedName -ceq $other) { throw 'Different fixture directories shared one lock name.' }

    $probeRoot = Join-Path ([IO.Path]::GetTempPath()) ('roborev-dashboard-setup-ui-lock-' + [guid]::NewGuid().ToString('N'))
    $probeName = Get-SetupUiFixtureLockName -Root $probeRoot
    $mutex = [Threading.Mutex]::new($false, $probeName)
    $locked = $false
    $holder = $null
    try {
        try { $locked = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $locked = $true }
        if (-not $locked) { throw 'Fixture lock self-test could not acquire its own mutex.' }
        $holder = [PowerShell]::Create()
        [void]$holder.AddScript(@'
param($name)
$mutex = [Threading.Mutex]::new($false, $name)
$owned = $false
try {
    try { $owned = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $owned = $true }
    if ($owned) { 'owned' } else { 'blocked' }
} finally {
    if ($owned) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}
'@).AddArgument($probeName)
        $result = @($holder.Invoke())
        if ($holder.HadErrors) { throw ('Fixture lock overlap probe failed: ' + (($holder.Streams.Error | ForEach-Object { $_.Exception.Message }) -join '; ')) }
        if ($result.Count -ne 1 -or [string]$result[0] -cne 'blocked') { throw "Overlapping fixture run was not blocked: $($result -join ',')" }
    } finally {
        if ($holder) { $holder.Dispose() }
        if ($locked) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
    $released = [Threading.Mutex]::new($false, $probeName)
    $releasedOwned = $false
    try {
        try { $releasedOwned = $released.WaitOne(0) } catch [Threading.AbandonedMutexException] { $releasedOwned = $true }
        if (-not $releasedOwned) { throw 'Fixture lock stayed held after the owner released it.' }
    } finally {
        if ($releasedOwned) { $released.ReleaseMutex() }
        $released.Dispose()
    }
    return
}
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$scriptPath = Join-Path $repo 'installer\RoboRevDashboard.iss'
$evidenceDir = Join-Path $repo '.tmp\setup-ui'
$productionAppId = 'D7B6E4A4-76E5-4F33-A9CE-CF1B8E8DF0AF'
$productionDir = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Programs\RoboRev Dashboard'
$productionGroup = Join-Path ([Environment]::GetFolderPath('Programs')) 'RoboRev Dashboard'
$timer = [Diagnostics.Stopwatch]::StartNew()

function K([string]$Escaped) { return [regex]::Unescape($Escaped) }
$T = @{
    Install      = K '\uC124\uCE58'
    Cancel       = K '\uCDE8\uC18C'
    Finish       = K '\uB9C8\uCE68'
    Close        = K '\uB2EB\uAE30'
    Minimize     = K '\uCD5C\uC18C\uD654'
    Update       = K '\uC5C5\uB370\uC774\uD2B8'
    Yes          = @((K '\uC608(Y)'), (K '\uC608'))
    No           = @((K '\uC544\uB2C8\uC694(N)'), (K '\uC544\uB2C8\uC694'))
    OK           = @((K '\uD655\uC778'), 'OK')
    Run          = K 'RoboRev Dashboard \uC2E4\uD589'
    HeadInstall  = K 'RoboRev Dashboard \uC124\uCE58'
    HeadUpdate   = K 'RoboRev Dashboard \uC5C5\uB370\uC774\uD2B8'
    HeadReinstall = K 'RoboRev Dashboard \uB2E4\uC2DC \uC124\uCE58'
    Installing   = K 'RoboRev Dashboard \uC124\uCE58 \uC911...'
    Updating     = K 'RoboRev Dashboard \uC5C5\uB370\uC774\uD2B8 \uC911...'
    Done         = K '\uC124\uCE58\uAC00 \uC644\uB8CC\uB418\uC5C8\uC2B5\uB2C8\uB2E4'
    DoneUpdate   = K '\uC5C5\uB370\uC774\uD2B8\uAC00 \uC644\uB8CC\uB418\uC5C8\uC2B5\uB2C8\uB2E4'
    Failed       = K '\uC124\uCE58\uD558\uC9C0 \uBABB\uD588\uC2B5\uB2C8\uB2E4'
    Blocked      = K '\uC124\uCE58\uB97C \uB9C8\uCCE4\uC9C0\uB9CC \uC2E4\uD589\uD558\uC9C0 \uC54A\uC2B5\uB2C8\uB2E4'
    Busy         = K '\uC0AC\uC6A9 \uC911'
    Lower        = K '\uB0AE\uC2B5\uB2C8\uB2E4'
    LogLabel     = K '\uB85C\uADF8: '
    Product      = 'RoboRev Dashboard'
}

$report = [ordered]@{
    kind = 'themed RoboRev Dashboard installer UI on an isolated fixture'
    startedAt = [DateTime]::UtcNow.ToString('o')
    checks = @()
    scales = @()
    screenshots = @()
    passed = $false
    error = $null
}
function Add-Check([string]$Name) { $script:report.checks += $Name; Write-Host "PASS $Name" }

Add-Type -AssemblyName System.Drawing
if (-not ('DashboardSetupUi' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;

public static class DashboardSetupUi {
    public delegate bool EnumProc(IntPtr hWnd, IntPtr lParam);
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] public struct POINT { public int X, Y; }
    [StructLayout(LayoutKind.Sequential)] public struct GUITHREADINFO {
        public int cbSize, flags;
        public IntPtr hwndActive, hwndFocus, hwndCapture, hwndMenuOwner, hwndMoveSize, hwndCaret;
        public RECT rcCaret;
    }
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public struct LOGFONT {
        public int lfHeight, lfWidth, lfEscapement, lfOrientation, lfWeight;
        public byte lfItalic, lfUnderline, lfStrikeOut, lfCharSet, lfOutPrecision, lfClipPrecision, lfQuality, lfPitchAndFamily;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string lfFaceName;
    }
    public sealed class Control {
        public IntPtr Handle;
        public string Class;
        public string Text;
        public RECT Rect;
        public bool Enabled;
        public int Style;
    }
    [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc f, IntPtr l);
    [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr h, EnumProc f, IntPtr l);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
    [DllImport("user32.dll")] static extern bool IsWindowEnabled(IntPtr h);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetClassName(IntPtr h, StringBuilder s, int n);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern IntPtr SendMessageTimeout(IntPtr h, uint m, IntPtr w, StringBuilder l, uint flags, uint timeout, out IntPtr result);
    [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr h, out RECT r);
    [DllImport("user32.dll")] static extern bool GetClientRect(IntPtr h, out RECT r);
    [DllImport("user32.dll")] static extern bool ClientToScreen(IntPtr h, ref POINT p);
    [DllImport("user32.dll")] static extern int GetWindowLong(IntPtr h, int index);
    [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr h);
    [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
    [DllImport("user32.dll")] static extern IntPtr SendMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
    [DllImport("user32.dll")] static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
    [DllImport("user32.dll")] static extern IntPtr SendMessageTimeout(IntPtr h, uint m, IntPtr w, IntPtr l, uint flags, uint timeout, out IntPtr result);
    [DllImport("user32.dll")] static extern IntPtr ChildWindowFromPointEx(IntPtr h, POINT p, uint flags);
    [DllImport("user32.dll")] static extern bool ScreenToClient(IntPtr h, ref POINT p);
    [DllImport("user32.dll")] static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
    [DllImport("user32.dll")] static extern bool GetGUIThreadInfo(uint thread, ref GUITHREADINFO info);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int DrawText(IntPtr dc, string text, int count, ref RECT r, uint format);
    [DllImport("user32.dll")] static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
    [DllImport("user32.dll")] static extern uint GetDpiForWindow(IntPtr h);
    [DllImport("gdi32.dll")] static extern IntPtr CreateCompatibleDC(IntPtr dc);
    [DllImport("gdi32.dll")] static extern bool DeleteDC(IntPtr dc);
    [DllImport("gdi32.dll")] static extern IntPtr SelectObject(IntPtr dc, IntPtr obj);
    [DllImport("gdi32.dll")] static extern bool DeleteObject(IntPtr obj);
    [DllImport("gdi32.dll", CharSet = CharSet.Unicode)] static extern int GetObject(IntPtr h, int size, ref LOGFONT lf);
    [DllImport("gdi32.dll", CharSet = CharSet.Unicode)] static extern IntPtr CreateFontIndirect(ref LOGFONT lf);
    [DllImport("gdi32.dll")] static extern IntPtr CreateCompatibleBitmap(IntPtr dc, int w, int h);
    [DllImport("gdi32.dll")] static extern int GetDIBits(IntPtr dc, IntPtr bmp, uint start, uint lines, [Out] int[] bits, ref BITMAPINFOHEADER info, uint usage);
    [DllImport("user32.dll")] static extern IntPtr GetDC(IntPtr h);
    [DllImport("user32.dll")] static extern int ReleaseDC(IntPtr h, IntPtr dc);
    [StructLayout(LayoutKind.Sequential)]
    struct BITMAPINFOHEADER {
        public int biSize, biWidth, biHeight;
        public short biPlanes, biBitCount;
        public int biCompression, biSizeImage, biXPelsPerMeter, biYPelsPerMeter, biClrUsed, biClrImportant;
    }
    const uint BM_CLICK = 0x00F5, BM_GETCHECK = 0x00F0, WM_GETFONT = 0x0031, WM_NCHITTEST = 0x0084;
    const uint WM_KEYDOWN = 0x0100, WM_KEYUP = 0x0101;
    const uint DT_CALCRECT = 0x0400, DT_NOPREFIX = 0x0800, DT_WORDBREAK = 0x0010, DT_SINGLELINE = 0x0020, DT_EDITCONTROL = 0x2000;
    static void Aware() { SetThreadDpiAwarenessContext(new IntPtr(-4)); }
    static string ClassOf(IntPtr h) { var b = new StringBuilder(256); GetClassName(h, b, b.Capacity); return b.ToString(); }
    static string TextOf(IntPtr h) {
        IntPtr length;
        if (SendMessageTimeout(h, 0x000E, IntPtr.Zero, IntPtr.Zero, 2, 5000, out length) == IntPtr.Zero) return "";
        var b = new StringBuilder(length.ToInt32() + 1);
        IntPtr copied;
        SendMessageTimeout(h, 0x000D, new IntPtr(b.Capacity), b, 2, 5000, out copied);
        return b.ToString();
    }
    public static IntPtr[] TopLevel(uint[] pids) {
        Aware();
        var owned = new HashSet<uint>(pids);
        var found = new List<IntPtr>();
        EnumWindows(delegate(IntPtr h, IntPtr l) {
            uint pid;
            GetWindowThreadProcessId(h, out pid);
            if (owned.Contains(pid) && IsWindowVisible(h) && ClassOf(h) != "TApplication") found.Add(h);
            return true;
        }, IntPtr.Zero);
        return found.ToArray();
    }
    public static string ClassName(IntPtr h) { return ClassOf(h); }
    public static uint Dpi(IntPtr h) { return GetDpiForWindow(h); }
    public static Control[] Children(IntPtr top) { return Descendants(top, false); }
    public static Control[] Descendants(IntPtr top, bool includeHidden) {
        Aware();
        var found = new List<Control>();
        if (top == IntPtr.Zero) return found.ToArray();
        EnumChildWindows(top, delegate(IntPtr h, IntPtr l) {
            if (!includeHidden && !IsWindowVisible(h)) return true;
            var c = new Control();
            c.Handle = h; c.Class = ClassOf(h); c.Text = TextOf(h);
            GetWindowRect(h, out c.Rect);
            c.Enabled = IsWindowEnabled(h);
            c.Style = GetWindowLong(h, -16);
            found.Add(c);
            return true;
        }, IntPtr.Zero);
        return found.ToArray();
    }
    public static RECT ClientScreenRect(IntPtr top) {
        Aware();
        RECT r; GetClientRect(top, out r);
        var p = new POINT(); ClientToScreen(top, ref p);
        r.Left += p.X; r.Right += p.X; r.Top += p.Y; r.Bottom += p.Y;
        return r;
    }
    public static bool Click(IntPtr h) { return PostMessage(h, BM_CLICK, IntPtr.Zero, IntPtr.Zero); }
    public static bool Press(IntPtr h) {
        Aware();
        RECT r; GetClientRect(h, out r);
        var at = new IntPtr(((r.Bottom / 2) << 16) | (r.Right / 2));
        return PostMessage(h, 0x0201, new IntPtr(1), at) && PostMessage(h, 0x0202, IntPtr.Zero, at);
    }
    public static int CheckState(IntPtr h) { return SendMessage(h, BM_GETCHECK, IntPtr.Zero, IntPtr.Zero).ToInt32(); }
    public static IntPtr Focus(IntPtr top) {
        uint pid; uint thread = GetWindowThreadProcessId(top, out pid);
        var info = new GUITHREADINFO(); info.cbSize = Marshal.SizeOf(typeof(GUITHREADINFO));
        return GetGUIThreadInfo(thread, ref info) ? info.hwndFocus : IntPtr.Zero;
    }
    public static bool Key(IntPtr focus, int vk) {
        return PostMessage(focus, WM_KEYDOWN, new IntPtr(vk), new IntPtr(1)) &&
               PostMessage(focus, WM_KEYUP, new IntPtr(vk), new IntPtr(unchecked((int)0xC0000001)));
    }
    public static int HitTest(IntPtr h, int screenX, int screenY) {
        Aware();
        IntPtr result;
        var lParam = new IntPtr((screenY << 16) | (screenX & 0xFFFF));
        SendMessageTimeout(h, WM_NCHITTEST, IntPtr.Zero, lParam, 2, 5000, out result);
        return result.ToInt32();
    }
    public static IntPtr DeepestChildAt(IntPtr top, int screenX, int screenY) {
        Aware();
        IntPtr current = top;
        while (true) {
            var p = new POINT(); p.X = screenX; p.Y = screenY;
            ScreenToClient(current, ref p);
            IntPtr child = ChildWindowFromPointEx(current, p, 0x0001 | 0x0004);
            if (child == IntPtr.Zero || child == current) return current;
            current = child;
        }
    }
    public static int[] Measure(IntPtr control, string text, int width, bool wrap) {
        Aware();
        IntPtr font = SendMessage(control, WM_GETFONT, IntPtr.Zero, IntPtr.Zero);
        var lf = new LOGFONT();
        if (font == IntPtr.Zero || GetObject(font, Marshal.SizeOf(typeof(LOGFONT)), ref lf) == 0)
            throw new InvalidOperationException("control font is not readable");
        IntPtr ours = CreateFontIndirect(ref lf);
        IntPtr dc = CreateCompatibleDC(IntPtr.Zero);
        IntPtr previous = SelectObject(dc, ours);
        try {
            var r = new RECT(); r.Right = wrap ? width : 100000;
            uint format = DT_CALCRECT | DT_NOPREFIX | (wrap ? (DT_WORDBREAK | DT_EDITCONTROL) : DT_SINGLELINE);
            DrawText(dc, text, -1, ref r, format);
            return new int[] { r.Right - r.Left, r.Bottom - r.Top };
        } finally { SelectObject(dc, previous); DeleteDC(dc); DeleteObject(ours); }
    }
    public sealed class Shot { public int Width, Height; public int[] Pixels; }
    public static Shot Capture(IntPtr top) {
        Aware();
        RECT r; GetWindowRect(top, out r);
        int w = Math.Max(1, r.Right - r.Left), h = Math.Max(1, r.Bottom - r.Top);
        IntPtr screen = GetDC(IntPtr.Zero);
        IntPtr dc = CreateCompatibleDC(screen);
        IntPtr bmp = CreateCompatibleBitmap(screen, w, h);
        IntPtr previous = SelectObject(dc, bmp);
        try {
            PrintWindow(top, dc, 2);
            SelectObject(dc, previous);
            var info = new BITMAPINFOHEADER();
            info.biSize = Marshal.SizeOf(typeof(BITMAPINFOHEADER));
            info.biWidth = w; info.biHeight = -h; info.biPlanes = 1; info.biBitCount = 32;
            var shot = new Shot(); shot.Width = w; shot.Height = h; shot.Pixels = new int[w * h];
            GetDIBits(dc, bmp, 0, (uint)h, shot.Pixels, ref info, 0);
            for (int i = 0; i < shot.Pixels.Length; i++) shot.Pixels[i] |= unchecked((int)0xFF000000);
            return shot;
        } finally { DeleteObject(bmp); DeleteDC(dc); ReleaseDC(IntPtr.Zero, screen); }
    }
    public static int AccentRun(Shot shot, int row, int fromX, int toX) {
        if (row < 0 || row >= shot.Height) return -1;
        int count = 0;
        for (int x = Math.Max(0, fromX); x < Math.Min(shot.Width, toX); x++) {
            int c = shot.Pixels[row * shot.Width + x];
            int red = (c >> 16) & 0xFF, green = (c >> 8) & 0xFF, blue = c & 0xFF;
            if (Math.Abs(red - 0x8D) < 24 && Math.Abs(green - 0xBB) < 24 && Math.Abs(blue - 0xFF) < 24) count++;
        }
        return count;
    }
}
'@
}

function Get-Hash([string]$Path) { (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }

function Get-ProductionSnapshot {
    $rows = [ordered]@{}
    $exe = Join-Path $productionDir 'roborev-dashboard.exe'
    $rows.exe = if (Test-Path -LiteralPath $exe -PathType Leaf) { Get-Hash $exe } else { 'absent' }
    $rows.files = if (Test-Path -LiteralPath $productionDir) {
        (@(Get-ChildItem -LiteralPath $productionDir -Force | Sort-Object Name | ForEach-Object Name) -join '|')
    } else { 'absent' }
    $key = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{$productionAppId}_is1"
    $rows.registration = if (Test-Path -LiteralPath $key) { [string](Get-ItemProperty -LiteralPath $key).DisplayVersion } else { 'absent' }
    $rows.group = if (Test-Path -LiteralPath $productionGroup) {
        (@(Get-ChildItem -LiteralPath $productionGroup -Recurse -Force -File | Sort-Object FullName | ForEach-Object { $_.FullName.Substring($productionGroup.Length) + ':' + (Get-Hash $_.FullName) }) -join '|')
    } else { 'absent' }
    $rows.pids = (@(Get-CimInstance Win32_Process -Filter "Name = 'roborev-dashboard.exe'" | Where-Object { $_.ExecutablePath -and ($_.ExecutablePath -ieq $exe) } | Sort-Object ProcessId | ForEach-Object ProcessId) -join ',')
    $rows | ConvertTo-Json -Compress
}

function Get-TreeIds([Diagnostics.Process]$Process) {
    $ids = [Collections.Generic.List[uint32]]::new()
    $pending = [Collections.Generic.Queue[uint32]]::new()
    $pending.Enqueue([uint32]$Process.Id)
    while ($pending.Count -gt 0) {
        $id = $pending.Dequeue()
        if ($ids.Contains($id)) { continue }
        $ids.Add($id)
        foreach ($child in @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$id" -ErrorAction SilentlyContinue)) {
            $pending.Enqueue([uint32]$child.ProcessId)
        }
    }
    return $ids.ToArray()
}

function Stop-FixtureProcesses([string]$Root) {
    if ([string]::IsNullOrWhiteSpace($Root)) { return }
    $needle = $Root.TrimEnd('\')
    foreach ($proc in @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object { $_.CommandLine -and $_.CommandLine.IndexOf($needle, [StringComparison]::OrdinalIgnoreCase) -ge 0 })) {
        Stop-Process -Id $proc.ProcessId -Force -ErrorAction SilentlyContinue
    }
}

function Get-Window([Diagnostics.Process]$Process, [string]$Class) {
    foreach ($top in @([DashboardSetupUi]::TopLevel((Get-TreeIds $Process)))) {
        if ([DashboardSetupUi]::ClassName($top) -in ($Class -split '\|')) { return $top }
    }
    return $null
}

function Find-Control([IntPtr]$Top, [string]$Text, [string]$Class = '') {
    foreach ($control in @([DashboardSetupUi]::Children($Top))) {
        if ($control.Text.Replace('&', '') -ceq $Text -and (-not $Class -or $control.Class -eq $Class)) { return $control }
    }
    return $null
}

function Get-WindowDump([Diagnostics.Process]$Process) {
    $lines = [Collections.Generic.List[string]]::new()
    foreach ($top in @([DashboardSetupUi]::TopLevel((Get-TreeIds $Process)))) {
        $lines.Add('WINDOW ' + [DashboardSetupUi]::ClassName($top))
        foreach ($control in @([DashboardSetupUi]::Children($top))) {
            if ($control.Text) { $lines.Add('  ' + $control.Class + ' [' + $control.Text + ']') }
        }
    }
    return ($lines -join "`n")
}

function Wait-For([string]$What, [scriptblock]$Probe, [Diagnostics.Process]$Process, [int]$Seconds = 60) {
    $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        $value = & $Probe
        if ($value) { return $value }
        if ($Process -and $Process.HasExited) { throw "Setup exited $($Process.ExitCode) while waiting for $What`n$(Get-WindowDump $Process)" }
        Start-Sleep -Milliseconds 50
    }
    $dump = if ($Process) { Get-WindowDump $Process } else { '' }
    throw "Timed out waiting for $What`n$dump"
}

function Wait-Page([Diagnostics.Process]$Process, [string]$Text, [int]$Seconds = 90) {
    return Wait-For "the page showing '$Text'" {
        $top = Get-Window $Process 'TWizardForm'
        if ($top -and (Find-Control $top $Text)) { $top }
    } $Process $Seconds
}

function Click-Control([IntPtr]$Top, [string]$Text, [string]$Class = '') {
    $control = Find-Control $Top $Text $Class
    if (-not $control) { throw "No visible control '$Text'" }
    $done = if ($control.Class -eq 'TBitmapButton') { [DashboardSetupUi]::Press($control.Handle) } else { [DashboardSetupUi]::Click($control.Handle) }
    if (-not $done) { throw "Clicking '$Text' did not complete" }
}

function Click-Any([IntPtr]$Top, [string[]]$Labels) {
    $wanted = @($Labels | ForEach-Object { $_.Replace('&', '') })
    foreach ($control in @([DashboardSetupUi]::Children($Top))) {
        $text = $control.Text.Replace('&', '')
        if ($wanted -contains $text) {
            $done = if ($control.Class -eq 'TBitmapButton') { [DashboardSetupUi]::Press($control.Handle) } else { [DashboardSetupUi]::Click($control.Handle) }
            if (-not $done) { throw "Clicking '$text' did not complete" }
            return
        }
    }
    $seen = @([DashboardSetupUi]::Children($Top) | ForEach-Object { "$($_.Class)='$($_.Text)'" })
    $codes = @($wanted | ForEach-Object { (($([int[]][char[]]$_)) -join ',') })
    throw "None of [$($wanted -join ' | ')] ($($codes -join ' ; ')) were visible. Controls: $($seen -join '; ')"
}

function Wait-Exit([Diagnostics.Process]$Process, [int]$Expected, [string]$What) {
    if (-not $Process.WaitForExit(120000)) { throw "$What did not exit" }
    $Process.WaitForExit()
    if ($Process.ExitCode -ne $Expected) { throw "$What exited $($Process.ExitCode), expected $Expected" }
}

function Save-Capture($Shot, [string]$Path) {
    $bitmap = [Drawing.Bitmap]::new($Shot.Width, $Shot.Height, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
    try {
        $data = $bitmap.LockBits([Drawing.Rectangle]::new(0, 0, $Shot.Width, $Shot.Height), [Drawing.Imaging.ImageLockMode]::WriteOnly, $bitmap.PixelFormat)
        try { [Runtime.InteropServices.Marshal]::Copy($Shot.Pixels, 0, $data.Scan0, $Shot.Pixels.Length) } finally { $bitmap.UnlockBits($data) }
        $bitmap.Save($Path, [Drawing.Imaging.ImageFormat]::Png)
    } finally { $bitmap.Dispose() }
}

function Save-Shot([IntPtr]$Top, [string]$Name) {
    Save-Capture ([DashboardSetupUi]::Capture($Top)) (Join-Path $evidenceDir "$Name.png")
    $script:report.screenshots += "$Name.png"
}

function Set-Check([IntPtr]$Top, [string]$Text, [int]$Want, [Diagnostics.Process]$Process) {
    $box = Find-Control $Top $Text 'TNewCheckBox'
    if (-not $box) { throw "No visible checkbox '$Text'" }
    if ([DashboardSetupUi]::CheckState($box.Handle) -ne $Want) {
        [void][DashboardSetupUi]::Click($box.Handle)
        Wait-For "checkbox '$Text' = $Want" { [DashboardSetupUi]::CheckState($box.Handle) -eq $Want } $Process 10 | Out-Null
    }
}

function Assert-Layout([IntPtr]$Top, [string]$What) {
    $client = [DashboardSetupUi]::ClientScreenRect($Top)
    $scale = ($client.Right - $client.Left) / 600.0
    $problems = [Collections.Generic.List[string]]::new()
    $leaves = @([DashboardSetupUi]::Children($Top) | Where-Object { $_.Class -in @('TNewStaticText', 'TNewButton', 'TNewCheckBox', 'TBitmapButton', 'TNewMemo') })
    if ($leaves.Count -lt 4) { $problems.Add("only $($leaves.Count) controls are visible") }
    foreach ($c in $leaves) {
        $r = $c.Rect
        $label = "$($c.Class) '$($c.Text)'"
        if ($r.Left -lt $client.Left - 1 -or $r.Top -lt $client.Top - 1 -or $r.Right -gt $client.Right + 1 -or $r.Bottom -gt $client.Bottom + 1) {
            $problems.Add("$label falls outside the client area")
        }
        $width = $r.Right - $r.Left
        $height = $r.Bottom - $r.Top
        if ([string]::IsNullOrEmpty($c.Text) -or $c.Class -eq 'TBitmapButton') { continue }
        switch ($c.Class) {
            'TNewStaticText' {
                $wraps = ($c.Style -band 0x1F) -ne 0x0C
                $line = [DashboardSetupUi]::Measure($c.Handle, $c.Text, $width, $false)
                if ($line[0] -gt $width + 1) {
                    if (-not $wraps) { $problems.Add("$label needs $($line[0])px but has $width") }
                    else {
                        $wrapped = [DashboardSetupUi]::Measure($c.Handle, $c.Text, $width, $true)
                        if ($wrapped[1] -gt $height + 1) { $problems.Add("$label needs $($wrapped[1])px of height but has $height") }
                    }
                } elseif ($line[1] -gt $height + 1) { $problems.Add("$label needs $($line[1])px of height but has $height") }
            }
            'TNewButton' {
                $need = [DashboardSetupUi]::Measure($c.Handle, $c.Text, $width, $false)
                if ($need[0] + [int](16 * $scale) -gt $width -or $need[1] -gt $height) { $problems.Add("$label needs $($need[0])x$($need[1]) inside $width x $height") }
            }
            'TNewCheckBox' {
                $need = [DashboardSetupUi]::Measure($c.Handle, $c.Text, $width, $false)
                if ($need[0] + [int](24 * $scale) -gt $width -or $need[1] -gt $height) { $problems.Add("$label needs $($need[0])x$($need[1]) beside its box in $width x $height") }
            }
            'TNewMemo' {
                $scrolls = ($c.Style -band 0x00200000) -ne 0
                $need = [DashboardSetupUi]::Measure($c.Handle, $c.Text, [Math]::Max(1, $width - [int](8 * $scale)), $true)
                if ($need[1] -gt $height -and -not $scrolls) { $problems.Add("$label needs $($need[1])px of height and has no scroll bar") }
            }
        }
    }
    for ($i = 0; $i -lt $leaves.Count; $i++) {
        for ($j = $i + 1; $j -lt $leaves.Count; $j++) {
            $a = $leaves[$i].Rect; $b = $leaves[$j].Rect
            $overlapW = [Math]::Min($a.Right, $b.Right) - [Math]::Max($a.Left, $b.Left)
            $overlapH = [Math]::Min($a.Bottom, $b.Bottom) - [Math]::Max($a.Top, $b.Top)
            if ($overlapW -gt 1 -and $overlapH -gt 1) {
                $problems.Add("$($leaves[$i].Class) '$($leaves[$i].Text)' overlaps $($leaves[$j].Class) '$($leaves[$j].Text)'")
            }
        }
    }
    if ($problems.Count) { throw "$What layout problems:`n  " + ($problems -join "`n  ") }
    return @{ width = $client.Right - $client.Left; height = $client.Bottom - $client.Top; scale = [Math]::Round($scale, 3); controls = $leaves.Count }
}

function New-Payload([string]$Destination, [string]$ExeSource, [string]$Marker) {
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    Copy-Item -LiteralPath $ExeSource -Destination (Join-Path $Destination 'roborev-dashboard.exe') -Force
    $exe = Join-Path $Destination 'roborev-dashboard.exe'
    $hash = Get-Hash $exe
    $manifest = '{"name":"roborev-dashboard","fixture":"' + $Marker + '","sha256":"' + $hash + '"}' + "`n"
    [IO.File]::WriteAllText((Join-Path $Destination 'roborev-dashboard.manifest.json'), $manifest, [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $Destination 'roborev-dashboard.sha256'), "$hash  roborev-dashboard.exe`n", [Text.UTF8Encoding]::new($false))
    return $hash
}

function Compile-Setup([string]$Version, [string]$Payload, [string]$Name, [string]$ExpectedHash, [int]$DialogFontSize = 0) {
    $output = Join-Path $fixtureRoot "out-$Name"
    New-Item -ItemType Directory -Force -Path $output | Out-Null
    $source = $scriptPath
    if ($DialogFontSize -gt 0) {
        $source = Join-Path $fixtureRoot "scale-$DialogFontSize.iss"
        $installerDir = Split-Path -Parent $scriptPath
        $wrapper = "#include `"$scriptPath`"`r`n[Setup]`r`nSourceDir=$installerDir`r`n[LangOptions]`r`nDialogFontSize=$DialogFontSize`r`n"
        [IO.File]::WriteAllText($source, $wrapper, [Text.UTF8Encoding]::new($false))
    }
    $arguments = @(
        '/Qp', "/O$output", "/DAppVersion=$Version", "/DVersionInfoVersion=$Version.0",
        "/DBaseVersion=$Version", "/DSourceDir=$Payload", "/DOutputDir=$output",
        "/DExeSha256=$ExpectedHash", "/DAppId=$appId", "/DProgramGroupName=$groupName", $source
    )
    $log = @(& $iscc @arguments 2>&1 | ForEach-Object { [string]$_ })
    if ($LASTEXITCODE -ne 0) { throw "ISCC.exe failed compiling $Name ($LASTEXITCODE): $($log -join "`n")" }
    $compiled = Join-Path $output 'RoboRev-Dashboard-Setup.exe'
    if (-not (Test-Path -LiteralPath $compiled -PathType Leaf)) { throw "ISCC.exe did not write $compiled" }
    return $compiled
}

function Start-Setup([string]$Setup, [string[]]$Arguments, [string]$LogName) {
    $log = Join-Path $logDir $LogName
    $quoted = @($Arguments + @('/NORESTART', "/LOG=$log")) | ForEach-Object {
        if ($_ -match '[\s]') { '"' + $_ + '"' } else { $_ }
    }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $Setup
    $psi.Arguments = ($quoted -join ' ')
    $psi.UseShellExecute = $false
    $process = [Diagnostics.Process]::Start($psi)
    $script:owned += $process
    $process | Add-Member -NotePropertyName LogPath -NotePropertyValue $log
    return $process
}

function Get-InstalledVersion {
    if (-not (Test-Path -LiteralPath $uninstallKey)) { return $null }
    return [string](Get-ItemProperty -LiteralPath $uninstallKey).DisplayVersion
}

function Assert-NotInstalled([string]$What) {
    if (Test-Path -LiteralPath (Join-Path $installDir 'roborev-dashboard.exe')) { throw "$What installed files" }
    if (Test-Path -LiteralPath $uninstallKey) { throw "$What registered an installation" }
}

function Assert-Payload([string]$Payload, [string]$What) {
    foreach ($relative in @('roborev-dashboard.exe', 'roborev-dashboard.manifest.json', 'roborev-dashboard.sha256')) {
        $installed = Join-Path $installDir $relative
        $source = Join-Path $Payload $relative
        if ((Get-Hash $installed) -ne (Get-Hash $source)) { throw "$What installed a different $relative" }
    }
    $link = Join-Path $groupDir 'RoboRev Dashboard.lnk'
    if (-not (Test-Path -LiteralPath $link -PathType Leaf)) {
        $programs = [Environment]::GetFolderPath('Programs')
        $found = @(Get-ChildItem -LiteralPath $programs -Recurse -Filter '*.lnk' -Force -ErrorAction SilentlyContinue | Where-Object { $_.Name -like 'RoboRev*' -or $_.FullName -like "*$groupName*" } | ForEach-Object FullName)
        throw "$What did not create the Start Menu shortcut at '$link'. Nearby: $($found -join ', ')"
    }
    $stop = Join-Path $groupDir 'Stop RoboRev Dashboard.lnk'
    if (-not (Test-Path -LiteralPath $stop -PathType Leaf)) { throw "$What did not create the Stop shortcut" }
}

function Assert-NoRunEntry([string]$Log, [string]$What) {
    $text = [IO.File]::ReadAllText($Log)
    if ($text -match '-- Run entry --') { throw "$What started the installed program" }
}

function Read-Result([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $null }
    return [IO.File]::ReadAllText($Path).Trim()
}

function Invoke-Uninstall([string]$What) {
    $uninstaller = Join-Path $installDir 'unins000.exe'
    if (-not (Test-Path -LiteralPath $uninstaller -PathType Leaf)) { throw "$What has no uninstaller" }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $uninstaller
    $psi.Arguments = '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART'
    $psi.UseShellExecute = $false
    $process = [Diagnostics.Process]::Start($psi)
    $script:owned += $process
    if (-not $process.WaitForExit(120000)) { throw "$What uninstall did not exit" }
    if ($process.ExitCode -ne 0) { throw "$What uninstall exited $($process.ExitCode)" }
    Wait-For "$What uninstall to finish" { -not (Test-Path -LiteralPath $uninstaller) -and -not (Test-Path -LiteralPath $uninstallKey) } $null 60 | Out-Null
}

function Assert-NeverFailed([IntPtr]$Top, [string]$Tag) {
    foreach ($c in @([DashboardSetupUi]::Descendants($Top, $true))) {
        if ($c.Text -ceq $T.Failed) { throw "$Tag succeeded but its window showed the failure heading" }
    }
}

function Invoke-InteractiveInstall([string]$Setup, [string]$Tag, [string]$Heading, [string]$Button, [bool]$WatchProgress) {
    $process = Start-Setup $Setup @("/DIR=$installDir", "/GROUP=$groupName") "$Tag.log"
    $top = Wait-Page $process $Heading
    Start-Sleep -Milliseconds 250
    $confirm = Assert-Layout $top "$Tag confirmation page"
    if (-not (Find-Control $top $T.Product)) { throw "$Tag confirmation page has no product name" }
    Save-Shot $top "$Tag-1-confirm"
    Click-Control $top $Button 'TNewButton'
    $partial = 0
    if ($WatchProgress) {
        $progressHeading = if ($Button -eq $T.Update) { $T.Updating } else { $T.Installing }
        $top = Wait-Page $process $progressHeading 30
        $deadline = [DateTime]::UtcNow.AddSeconds(90)
        $savedProgress = $false
        $maxBest = 0
        while ([DateTime]::UtcNow -lt $deadline -and -not (Find-Control $top $T.Finish)) {
            if (Find-Control $top $T.Failed) { throw "$Tag showed the failure page while installing" }
            $capture = [DashboardSetupUi]::Capture($top)
            if (-not $savedProgress) {
                Save-Capture $capture (Join-Path $evidenceDir "$Tag-2-progress.png")
                $script:report.screenshots += "$Tag-2-progress.png"
                $savedProgress = $true
            }
            $from = [int]($capture.Width * 80 / 600.0)
            $to = [int]($capture.Width * 520 / 600.0)
            $full = [Math]::Max(1, $to - $from)
            $best = 0
            $y0 = [int]($capture.Height * 0.50)
            $y1 = [Math]::Min($capture.Height - 1, [int]($capture.Height * 0.80))
            for ($row = $y0; $row -le $y1; $row += 2) {
                $run = [DashboardSetupUi]::AccentRun($capture, $row, $from, $to)
                if ($run -gt $best) { $best = $run }
            }
            if ($best -gt $maxBest) { $maxBest = $best }
            if ($best -gt 4 -and $best -lt ($full - 4) -and $best -gt $partial) { $partial = $best }
            Start-Sleep -Milliseconds 10
        }
        if ($partial -le 0) { throw "Progress fill was not observed (max accent run $maxBest)." }
    }
    $top = Wait-Page $process $T.Finish 120
    Assert-NeverFailed $top $Tag
    Start-Sleep -Milliseconds 200
    $done = Assert-Layout $top "$Tag completion page"
    Save-Shot $top "$Tag-3-done"
    $run = Find-Control $top $T.Run 'TNewCheckBox'
    if (-not $run) { throw "$Tag completion page has no Run checkbox" }
    if ([DashboardSetupUi]::CheckState($run.Handle) -ne 1) { throw "$Tag Run checkbox was not checked by default" }
    Set-Check $top $T.Run 0 $process
    return @{ Process = $process; Top = $top; Partial = $partial; Confirm = $confirm; Done = $done }
}

function Invoke-StopPrompt([Diagnostics.Process]$Process) {
    return Wait-For 'the graceful-stop confirmation' {
        $dialog = Get-Window $Process 'TTaskDialogForm|#32770'
        if (-not $dialog) { return $null }
        foreach ($control in @([DashboardSetupUi]::Children($dialog))) {
            if ($control.Text -like '*--stop-installed*') { return $dialog }
        }
        return $null
    } $Process 20
}

$owned = @()
$appId = $null
$uninstallKey = $null
$groupDir = $null
$serve = $null
$fixtureRoot = $null
$fixtureMutex = $null
$fixtureLocked = $false
try {
    Set-Location -LiteralPath $repo
    $before = Get-ProductionSnapshot
    . (Join-Path $repo 'scripts\prepare-inno.ps1')
    $iscc = Invoke-PrepareInno -ProjectRoot $repo
    $fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) 'roborev-dashboard-setup-ui'
    # Separate checkouts share this one TEMP directory. Own it before looking
    # at or removing anything there; a live run must keep its payload and logs.
    $fixtureMutex = [Threading.Mutex]::new($false, (Get-SetupUiFixtureLockName -Root $fixtureRoot))
    try { $fixtureLocked = $fixtureMutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $fixtureLocked = $true }
    if (-not $fixtureLocked) { throw "Another setup UI run owns $fixtureRoot" }
    if (Test-Path -LiteralPath $fixtureRoot) {
        if (Get-ChildItem -LiteralPath $fixtureRoot -Recurse -Force -Attributes ReparsePoint -ErrorAction SilentlyContinue) { throw "Refusing a reparse point under $fixtureRoot" }
        Stop-FixtureProcesses $fixtureRoot
        Start-Sleep -Milliseconds 300
        Remove-Item -LiteralPath $fixtureRoot -Recurse -Force
    }
    if (Test-Path -LiteralPath $evidenceDir) { Remove-Item -LiteralPath $evidenceDir -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $fixtureRoot, $evidenceDir | Out-Null
    $appId = [guid]::NewGuid().ToString('D').ToUpperInvariant()
    if ($appId -eq $productionAppId) { throw 'Refusing the production AppId' }
    $installDir = Join-Path $fixtureRoot 'app'
    if ($installDir.TrimEnd('\') -ieq $productionDir.TrimEnd('\')) { throw 'Refusing the production install directory' }
    $groupName = 'RoboRevDashboardSetupUi-' + $appId.Substring(0, 8)
    if ($groupName -eq 'RoboRev Dashboard') { throw 'Refusing the production Start Menu group' }
    $groupDir = Join-Path ([Environment]::GetFolderPath('Programs')) $groupName
    $logDir = Join-Path $fixtureRoot 'logs'
    $uninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{$appId}_is1"
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    $sentinel = Join-Path $fixtureRoot 'do-not-touch.txt'
    [IO.File]::WriteAllText($sentinel, 'sentinel', [Text.UTF8Encoding]::new($false))
    $sentinelHash = Get-Hash $sentinel

    $fixtureExe = Join-Path $fixtureRoot 'fixture.exe'
    $oldCgo = $env:CGO_ENABLED
    $env:CGO_ENABLED = '0'
    try {
        & go build -o $fixtureExe ./scripts/setup-ui-fixture
        if ($LASTEXITCODE -ne 0) { throw "fixture build failed: $LASTEXITCODE" }
    } finally { if ($null -eq $oldCgo) { Remove-Item Env:CGO_ENABLED } else { $env:CGO_ENABLED = $oldCgo } }
    $plain = Join-Path $fixtureRoot 'plain.exe'
    Copy-Item -LiteralPath $fixtureExe -Destination $plain -Force
    $bulky = Join-Path $fixtureRoot 'bulky.exe'
    $suffix = [byte[]]::new(32MB)
    [Random]::new(20261025).NextBytes($suffix)
    $sourceBytes = [IO.File]::ReadAllBytes($fixtureExe)
    $combined = New-Object byte[] ($sourceBytes.Length + $suffix.Length)
    [Buffer]::BlockCopy($sourceBytes, 0, $combined, 0, $sourceBytes.Length)
    [Buffer]::BlockCopy($suffix, 0, $combined, $sourceBytes.Length, $suffix.Length)
    [IO.File]::WriteAllBytes($bulky, $combined)

    $payload1 = Join-Path $fixtureRoot 'payload-1'
    $payload2 = Join-Path $fixtureRoot 'payload-2'
    $hash1 = New-Payload $payload1 $bulky 'build-1'
    $hash2 = New-Payload $payload2 $plain 'build-2'
    $setup1 = Compile-Setup '1.0.0' $payload1 'v1' $hash1
    $setup2 = Compile-Setup '1.2.0' $payload2 'v2' $hash2
    $setupDown = Compile-Setup '0.9.0' $payload1 'down' $hash1
    $badHash = ('0' * 64)
    $setupBad = Compile-Setup '1.2.0' $payload2 'bad' $badHash
    Add-Check 'isolated installers compile with a test AppId'

    # 1. The confirmation page installs nothing, and closing it asks first.
    $process = Start-Setup $setup1 @("/DIR=$installDir", "/GROUP=$groupName") 'cancel.log'
    $top = Wait-Page $process $T.HeadInstall
    Start-Sleep -Milliseconds 400
    Assert-NotInstalled 'Opening the confirmation page'
    $client = [DashboardSetupUi]::ClientScreenRect($top)
    $scale = ($client.Right - $client.Left) / 600.0
    $report.monitorDpi = [int][DashboardSetupUi]::Dpi($top)
    if ([Math]::Abs(($client.Bottom - $client.Top) / $scale - 340) -gt 3) { throw 'The window is not the 600x340 design at its own scale' }
    $emptyX = $client.Left + [int](300 * $scale); $emptyY = $client.Top + [int](250 * $scale)
    if ([DashboardSetupUi]::HitTest($top, $emptyX, $emptyY) -ne 2) { throw 'Empty window space is not a drag area' }
    Add-Check 'opening the installer shows the 600x340 confirmation page and installs nothing'
    Click-Control $top $T.Minimize 'TBitmapButton'
    Wait-For 'the minimised window' { [DashboardSetupUi]::IsIconic($top) } $process 10 | Out-Null
    [void][DashboardSetupUi]::ShowWindow($top, 9)
    Wait-For 'the restored window' { -not [DashboardSetupUi]::IsIconic($top) } $process 10 | Out-Null
    Add-Check 'minimise button minimises and the window restores'
    Click-Control $top $T.Close 'TBitmapButton'
    $dialog = Wait-For 'the cancel confirmation' { Get-Window $process 'TTaskDialogForm|#32770' } $process 15
    Save-Shot $dialog 'cancel-confirmation'
    Click-Any -Top $dialog -Labels $T.Yes
    Wait-Exit $process 2 'Cancelled setup'
    Assert-NotInstalled 'Cancelling'
    Add-Check 'the close button asks before cancelling and exits 2 without installing'

    # 2. New install through the real button. Run stays unchecked.
    $install = Invoke-InteractiveInstall $setup1 'install' $T.HeadInstall $T.Install $true
    if ($install.Partial -le 0) { throw 'The progress bar never showed a partial fill while files were copied' }
    Click-Control $install.Top $T.Finish 'TNewButton'
    Wait-Exit $install.Process 0 'Interactive install'
    Assert-Payload $payload1 'Interactive install'
    if ((Get-InstalledVersion) -ne '1.0.0') { throw 'Interactive install registered the wrong version' }
    Assert-NoRunEntry $install.Process.LogPath 'Interactive install'
    $report.scales += @{ dialogFontSize = 'default'; dpi = $report.monitorDpi; confirm = $install.Confirm; done = $install.Done }
    Add-Check "install button copies the payload, the real progress bar moved ($($install.Partial)px), and unchecked Run starts nothing"

    # 3. Update wording and a new payload.
    $update = Invoke-InteractiveInstall $setup2 'update' $T.HeadUpdate $T.Update $false
    if (-not (Find-Control $update.Top $T.DoneUpdate)) { throw 'Update completion page does not say the update finished' }
    Click-Control $update.Top $T.Finish 'TNewButton'
    Wait-Exit $update.Process 0 'Interactive update'
    if ((Get-InstalledVersion) -ne '1.2.0') { throw 'Interactive update did not register the new version' }
    Assert-Payload $payload2 'Interactive update'
    Assert-NoRunEntry $update.Process.LogPath 'Interactive update'
    Add-Check 'an existing installation is offered as an update and Finish does not launch it'

    # 4. Same version is a reinstall. Keyboard reaches Run and leaves it unchecked.
    $keys = Start-Setup $setup2 @("/DIR=$installDir", "/GROUP=$groupName") 'keyboard.log'
    $top = Wait-Page $keys $T.HeadReinstall
    Start-Sleep -Milliseconds 250
    $focus = [DashboardSetupUi]::Focus($top)
    if ($focus -eq [IntPtr]::Zero) { $focus = $top }
    [void][DashboardSetupUi]::Key($focus, 0x0D)
    $top = Wait-Page $keys $T.Finish 120
    Assert-NeverFailed $top 'keyboard'
    $run = Find-Control $top $T.Run 'TNewCheckBox'
    $reached = $false
    for ($i = 0; $i -lt 8 -and -not $reached; $i++) {
        if ([DashboardSetupUi]::Focus($top) -eq $run.Handle) { $reached = $true; break }
        $focus = [DashboardSetupUi]::Focus($top)
        if ($focus -eq [IntPtr]::Zero) { $focus = $top }
        [void][DashboardSetupUi]::Key($focus, 0x09)
        Start-Sleep -Milliseconds 150
    }
    if (-not $reached) { throw 'Tab never reached the Run checkbox' }
    [void][DashboardSetupUi]::Key($run.Handle, 0x20)
    Wait-For 'Space to uncheck Run' { [DashboardSetupUi]::CheckState($run.Handle) -eq 0 } $keys 10 | Out-Null
    [void][DashboardSetupUi]::Key($run.Handle, 0x0D)
    Wait-Exit $keys 0 'Keyboard reinstall'
    Assert-NoRunEntry $keys.LogPath 'Keyboard reinstall'
    Add-Check 'same version is a reinstall; Enter installs, Tab and Space clear Run, and Enter finishes'

    # 5. A running fixture is not stopped until the confirmation is accepted.
    $installedExe = Join-Path $installDir 'roborev-dashboard.exe'
    $hashBeforeStop = Get-Hash $installedExe
    $serve = Start-Process -FilePath $installedExe -ArgumentList '--serve' -PassThru -WindowStyle Hidden
    $script:owned += $serve
    Start-Sleep -Milliseconds 300
    $decline = Start-Setup $setup2 @("/DIR=$installDir", "/GROUP=$groupName") 'decline-stop.log'
    $top = Wait-Page $decline $T.HeadReinstall
    Click-Control $top $T.Install 'TNewButton'
    $stopDialog = Invoke-StopPrompt $decline
    Save-Shot $stopDialog 'stop-confirmation'
    Click-Any -Top $stopDialog -Labels $T.No
    $top = Wait-Page $decline $T.Failed 30
    $detail = @([DashboardSetupUi]::Children($top) | Where-Object { $_.Class -eq 'TNewMemo' })
    if ($detail.Count -ne 1 -or -not $detail[0].Text.Contains($T.Busy)) { throw "Decline did not show the installer's own busy reason" }
    if (-not $detail[0].Text.Contains($T.LogLabel)) { throw 'Decline failure did not name the log' }
    $layout = Assert-Layout $top 'declined stop failure page'
    Save-Shot $top 'decline-stop'
    Click-Control $top $T.Close 'TNewButton'
    Wait-Exit $decline 7 'Declined stop'
    if ((Get-Hash $installedExe) -ne $hashBeforeStop) { throw 'Declining the stop changed installed files' }
    if ($serve.HasExited) { throw 'Declining the stop terminated the running fixture' }
    Assert-NoRunEntry $decline.LogPath 'Declined stop'
    Add-Check 'a running Dashboard is left running when the stop confirmation is declined; exit 7 and files stay'
    $stopper = Start-Process -FilePath $installedExe -ArgumentList '--stop-installed' -PassThru -WindowStyle Hidden -Wait
    if ($stopper.ExitCode -ne 0) { throw "Owned fixture --stop-installed exited $($stopper.ExitCode)" }
    if (-not $serve.WaitForExit(5000)) { throw 'Owned fixture did not exit after --stop-installed' }
    $serve = $null

    # 6. Accepting the confirmation uses --stop-installed and then installs.
    $serve = Start-Process -FilePath $installedExe -ArgumentList '--serve' -PassThru -WindowStyle Hidden
    $script:owned += $serve
    Start-Sleep -Milliseconds 300
    $accept = Start-Setup $setup2 @("/DIR=$installDir", "/GROUP=$groupName") 'accept-stop.log'
    $top = Wait-Page $accept $T.HeadReinstall
    Click-Control $top $T.Install 'TNewButton'
    $stopDialog = Invoke-StopPrompt $accept
    Click-Any -Top $stopDialog -Labels $T.Yes
    $top = Wait-Page $accept $T.Finish 120
    Assert-NeverFailed $top 'accepted stop'
    Set-Check $top $T.Run 0 $accept
    Click-Control $top $T.Finish 'TNewButton'
    Wait-Exit $accept 0 'Accepted stop reinstall'
    if (-not $serve.WaitForExit(5000)) { throw 'Accepting the stop left the fixture running' }
    $serve = $null
    Assert-Payload $payload2 'Accepted stop reinstall'
    Assert-NoRunEntry $accept.LogPath 'Accepted stop reinstall'
    Add-Check 'accepting the stop confirmation runs --stop-installed and then reinstalls'

    # 7. Downgrade is refused before copy. The result file records the failure.
    $resultPath = Join-Path $fixtureRoot 'downgrade-result.json'
    $beforeDown = Get-Hash $installedExe
    $down = Start-Setup $setupDown @("/DIR=$installDir", "/GROUP=$groupName", "/DASHBOARDRESULT=$resultPath") 'downgrade.log'
    $top = Wait-Page $down $T.HeadInstall
    Click-Control $top $T.Install 'TNewButton'
    $top = Wait-Page $down $T.Failed 30
    $detail = @([DashboardSetupUi]::Children($top) | Where-Object { $_.Class -eq 'TNewMemo' })
    if ($detail.Count -ne 1 -or -not $detail[0].Text.Contains($T.Lower)) { throw 'Downgrade detail does not contain the version refusal' }
    Assert-Layout $top 'downgrade failure page' | Out-Null
    Save-Shot $top 'downgrade'
    Click-Control $top $T.Close 'TNewButton'
    Wait-Exit $down 7 'Downgrade'
    if ((Get-Hash $installedExe) -ne $beforeDown) { throw 'Downgrade changed installed files' }
    if ((Get-InstalledVersion) -ne '1.2.0') { throw 'Downgrade changed the registration' }
    $downResult = Read-Result $resultPath
    if ($downResult -notmatch '"state"\s*:\s*"failed"' -or $downResult -notmatch '"message"\s*:\s*"precondition"') { throw "Downgrade result was $downResult" }
    Assert-NoRunEntry $down.LogPath 'Downgrade'
    Add-Check 'a downgrade is refused with its own reason, exits 7, keeps the install, and records failed/precondition'

    # 8. Silent no-launch still reports the verified result and does not run.
    $silentResult = Join-Path $fixtureRoot 'silent-result.json'
    $seen = [Collections.Generic.List[string]]::new()
    $silent = Start-Setup $setup2 @('/VERYSILENT', '/SUPPRESSMSGBOXES', "/DIR=$installDir", "/GROUP=$groupName", '/DASHBOARDNOLAUNCH', "/DASHBOARDRESULT=$silentResult") 'silent.log'
    $deadline = [DateTime]::UtcNow.AddSeconds(90)
    while ([DateTime]::UtcNow -lt $deadline -and -not $silent.HasExited) {
        $current = Read-Result $silentResult
        if ($current -and -not $seen.Contains($current)) { $seen.Add($current) }
        Start-Sleep -Milliseconds 20
    }
    Wait-Exit $silent 0 'Silent reinstall'
    $finalResult = Read-Result $silentResult
    if ($finalResult -notmatch '"state"\s*:\s*"succeeded"' -or $finalResult -notmatch '"message"\s*:\s*"verified"') { throw "Silent result was $finalResult" }
    if (-not ($seen -match '"ready"') -or -not ($seen -match '"installing"')) { throw "Silent result never passed through ready and installing: $($seen -join ' | ')" }
    Assert-NoRunEntry $silent.LogPath 'Silent reinstall'
    Assert-Payload $payload2 'Silent reinstall'
    Add-Check '/VERYSILENT /DASHBOARDNOLAUNCH records ready, installing, then succeeded/verified and does not launch'

    # 9. A payload hash mismatch is not a successful install and does not launch.
    $badResult = Join-Path $fixtureRoot 'bad-result.json'
    $bad = Start-Setup $setupBad @("/DIR=$installDir", "/GROUP=$groupName", "/DASHBOARDRESULT=$badResult") 'bad-hash.log'
    $top = Wait-Page $bad $T.HeadReinstall
    Click-Control $top $T.Install 'TNewButton'
    $errorDialog = Wait-For 'the verification message' {
        $dialog = Get-Window $bad 'TTaskDialogForm|#32770'
        if ($dialog -and ((Find-Control $dialog $T.OK[0]) -or (Find-Control $dialog $T.OK[1]))) { $dialog }
    } $bad 30
    Click-Any -Top $errorDialog -Labels $T.OK
    $top = Wait-Page $bad $T.Blocked 30
    if (Find-Control $top $T.Run) { throw 'Verification failure still offered Run' }
    if (Find-Control $top $T.Done) { throw 'Verification failure used the success heading' }
    Save-Shot $top 'bad-hash'
    Click-Control $top $T.Finish 'TNewButton'
    Wait-Exit $bad 19 'Verification failure'
    $badText = Read-Result $badResult
    if ($badText -notmatch '"state"\s*:\s*"failed"' -or $badText -notmatch '"message"\s*:\s*"verification"') { throw "Verification result was $badText" }
    Assert-NoRunEntry $bad.LogPath 'Verification failure'
    Add-Check 'a SHA-256 mismatch exits 19, records failed/verification, and the finished page does not offer Run'

    # 10. High contrast (/NOSTYLE) keeps the layout inside a system frame. Esc asks.
    $plain = Start-Setup $setup2 @('/NOSTYLE', "/DIR=$installDir", "/GROUP=$groupName") 'nostyle.log'
    $plainTop = Wait-Page $plain $T.HeadReinstall
    Start-Sleep -Milliseconds 250
    Assert-Layout $plainTop 'Unstyled confirmation page' | Out-Null
    if (Find-Control $plainTop $T.Minimize) { throw 'Unstyled window kept the custom caption buttons' }
    if (-not (Find-Control $plainTop $T.Product)) { throw 'Unstyled window lost the product name' }
    Save-Shot $plainTop 'nostyle-confirm'
    $focus = [DashboardSetupUi]::Focus($plainTop)
    if ($focus -eq [IntPtr]::Zero) { $focus = $plainTop }
    [void][DashboardSetupUi]::Key($focus, 0x1B)
    $dialog = Wait-For 'the cancel confirmation after Esc' { Get-Window $plain 'TTaskDialogForm|#32770' } $plain 15
    Click-Any -Top $dialog -Labels $T.Yes
    Wait-Exit $plain 2 'Unstyled cancel'
    Add-Check 'unstyled fallback keeps the product name inside a system frame; Esc asks and exits 2'

    Invoke-Uninstall 'Fixture'
    if (Test-Path -LiteralPath $groupDir) { throw 'Uninstall left the Start Menu group' }
    if ((Get-Hash $sentinel) -ne $sentinelHash) { throw 'Uninstall changed the sibling sentinel' }
    Add-Check 'fixture uninstall removes the files, shortcuts and registration'

    foreach ($size in $DialogFontSizes) {
        $scaled = Compile-Setup '1.2.0' $payload2 "scale-$size" $hash2 $size
        $run = Invoke-InteractiveInstall $scaled "scale-$size" $T.HeadInstall $T.Install $false
        Click-Control $run.Top $T.Finish 'TNewButton'
        Wait-Exit $run.Process 0 "Install at DialogFontSize $size"
        Assert-Payload $payload2 "DialogFontSize $size"
        $held = [IO.File]::Open((Join-Path $installDir 'roborev-dashboard.exe'), [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
        try {
            $failed = Start-Setup $scaled @("/DIR=$installDir", "/GROUP=$groupName") "scale-$size-busy.log"
            $top = Wait-Page $failed $T.HeadReinstall
            Click-Control $top $T.Install 'TNewButton'
            $stopDialog = Invoke-StopPrompt $failed
            Click-Any -Top $stopDialog -Labels $T.No
            $top = Wait-Page $failed $T.Failed 30
            $failedLayout = Assert-Layout $top "DialogFontSize $size failure page"
            Save-Shot $top "scale-$size-failed"
            Click-Control $top $T.Close 'TNewButton'
            Wait-Exit $failed 7 "Busy refusal at DialogFontSize $size"
        } finally { $held.Dispose() }
        Invoke-Uninstall "DialogFontSize $size"
        $report.scales += @{ dialogFontSize = $size; confirm = $run.Confirm; done = $run.Done; failed = $failedLayout }
        Add-Check ("DialogFontSize $size (scale {0}, {1}x{2}) fits the confirmation, completion and failure pages" -f $run.Confirm.scale, $run.Confirm.width, $run.Confirm.height)
    }

    if ((Get-Hash $sentinel) -ne $sentinelHash) { throw 'A scale run changed the sibling sentinel' }
    $after = Get-ProductionSnapshot
    if ($after -ne $before) { throw "The production installation changed:`n$before`n$after" }
    Add-Check 'the production install path, registration, Start Menu group and running Dashboard are unchanged'
    $report.passed = $true
} catch {
    $report.error = $_.Exception.Message
    throw
} finally {
    try {
        if ($fixtureLocked) {
            if ($serve -and -not $serve.HasExited) {
                try { Start-Process -FilePath (Join-Path $installDir 'roborev-dashboard.exe') -ArgumentList '--stop-installed' -WindowStyle Hidden -Wait | Out-Null } catch { }
                if (-not $serve.HasExited) { Stop-Process -Id $serve.Id -Force -ErrorAction SilentlyContinue }
            }
            foreach ($process in $owned) {
                try {
                    if (-not $process) { continue }
                    foreach ($id in @(Get-TreeIds $process)) { Stop-Process -Id $id -Force -ErrorAction SilentlyContinue }
                } catch { }
            }
            Stop-FixtureProcesses $fixtureRoot
            try {
                if ($uninstallKey -and (Test-Path -LiteralPath $uninstallKey)) {
                    $leftover = Join-Path $installDir 'unins000.exe'
                    if (Test-Path -LiteralPath $leftover -PathType Leaf) { Invoke-Uninstall 'Recovery' }
                    if (Test-Path -LiteralPath $uninstallKey) { Remove-Item -LiteralPath $uninstallKey -Recurse -Force -ErrorAction SilentlyContinue }
                }
                if ($logDir -and (Test-Path -LiteralPath $logDir) -and $evidenceDir) {
                    New-Item -ItemType Directory -Force -Path $evidenceDir | Out-Null
                    Copy-Item -Path (Join-Path $logDir '*.log') -Destination $evidenceDir -Force -ErrorAction SilentlyContinue
                }
                if ($groupDir -and (Test-Path -LiteralPath $groupDir)) { Remove-Item -LiteralPath $groupDir -Recurse -Force -ErrorAction SilentlyContinue }
                if ($fixtureRoot -and (Test-Path -LiteralPath $fixtureRoot)) { Remove-Item -LiteralPath $fixtureRoot -Recurse -Force -ErrorAction SilentlyContinue }
            } catch {
                Write-Warning $_.Exception.Message
            }
            $report.seconds = [Math]::Round($timer.Elapsed.TotalSeconds, 1)
            $report.finishedAt = [DateTime]::UtcNow.ToString('o')
            New-Item -ItemType Directory -Force -Path $evidenceDir | Out-Null
            $report | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $evidenceDir 'report.json') -Encoding utf8
            Write-Host "Setup UI report: $(Join-Path $evidenceDir 'report.json')"
        }
    } finally {
        if ($null -ne $fixtureMutex) {
            try { if ($fixtureLocked) { $fixtureMutex.ReleaseMutex() } } finally { $fixtureMutex.Dispose() }
        }
    }
}
if (-not $report.passed) { throw 'Setup UI checks did not pass' }
Write-Host ("All setup UI checks passed ({0:F1}s)." -f $timer.Elapsed.TotalSeconds)
