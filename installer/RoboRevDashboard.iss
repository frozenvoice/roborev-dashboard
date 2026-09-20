; RoboRev Dashboard current-user installer.
; The installer owns only the Dashboard executable and package metadata.  It
; never installs RoboRev, agents, Git hooks, credentials, or user settings.
#ifndef AppVersion
  #define AppVersion "0.1.0-local"
#endif
#ifndef BaseVersion
  #define BaseVersion "0.1.0"
#endif
#ifndef VersionInfoVersion
  #define VersionInfoVersion "0.1.0.0"
#endif
#ifndef SourceDir
  #define SourceDir "..\dist\local-payload"
#endif
#ifndef OutputDir
  #define OutputDir "..\dist\installer"
#endif
#ifndef ExeSha256
  #define ExeSha256 "__MISSING__"
#endif
#ifndef AppId
  #define AppId "D7B6E4A4-76E5-4F33-A9CE-CF1B8E8DF0AF"
#endif
#ifndef IncludeLicense
  #define IncludeLicense 0
#endif

#define AppName "RoboRev Dashboard"
#define AppPublisher "frozenvoice"
#define AppExeName "roborev-dashboard.exe"
#define AppUserModelID "frozenvoice.RoboRevDashboard"

[Setup]
AppId={{{#AppId}}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL=https://github.com/frozenvoice/roborev-dashboard
VersionInfoVersion={#VersionInfoVersion}
VersionInfoProductName={#AppName}
VersionInfoProductVersion={#VersionInfoVersion}
VersionInfoProductTextVersion={#AppVersion}
VersionInfoTextVersion={#AppVersion}
VersionInfoDescription={#AppName} Setup
DefaultDirName={localappdata}\Programs\RoboRev Dashboard
DisableDirPage=yes
UsePreviousAppDir=no
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
OutputDir={#OutputDir}
OutputBaseFilename=RoboRev-Dashboard-Setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
SetupLogging=yes
UninstallDisplayName={#AppName}
UninstallDisplayIcon={app}\{#AppExeName}
CloseApplications=no
RestartIfNeededByRun=no
AllowCancelDuringInstall=yes
AllowNoIcons=yes
Uninstallable=yes

[Languages]
Name: "korean"; MessagesFile: "compiler:Languages\Korean.isl"

[Files]
Source: "{#SourceDir}\{#AppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\roborev-dashboard.manifest.json"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\roborev-dashboard.sha256"; DestDir: "{app}"; Flags: ignoreversion
#if IncludeLicense
Source: "{#SourceDir}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
#endif

[Icons]
; Keep the console visible so a user has a supported stop path and can see
; startup/runtime diagnostics.  The application recognizes --installed and
; --stop-installed; the latter is deliberately a normal graceful request.
Name: "{group}\RoboRev Dashboard"; Filename: "{app}\{#AppExeName}"; Parameters: "--installed"; WorkingDir: "{app}"; Comment: "RoboRev Dashboard"; AppUserModelID: "{#AppUserModelID}"; Check: not WizardNoIcons
Name: "{group}\Stop RoboRev Dashboard"; Filename: "{app}\{#AppExeName}"; Parameters: "--stop-installed"; WorkingDir: "{app}"; Comment: "Stop the installed Dashboard"; AppUserModelID: "{#AppUserModelID}"; Check: not WizardNoIcons

[Run]
; Interactive installs show the standard checked post-install launch option.
Filename: "{app}\{#AppExeName}"; Parameters: "--installed"; WorkingDir: "{app}"; Description: "Launch RoboRev Dashboard"; Flags: nowait postinstall skipifsilent; Check: ShouldLaunchAfterInstall
; A caller must explicitly choose a silent install.  In that mode there is no
; checkbox, so the documented behavior is to launch after a verified install.
Filename: "{app}\{#AppExeName}"; Parameters: "--installed"; WorkingDir: "{app}"; Flags: nowait skipifdoesntexist; Check: ShouldLaunchAfterSilentInstall

[Code]
const
  ERROR_ALREADY_EXISTS = 183;
  ERROR_FILE_NOT_FOUND = 2;
  WAIT_OBJECT_0 = 0;
  WAIT_TIMEOUT = 258;
  WAIT_FAILED = $FFFFFFFF;
  INVALID_HANDLE_VALUE = -1;
  INVALID_FILE_ATTRIBUTES = $FFFFFFFF;
  FILE_SHARE_READ = $00000001;
  FILE_SHARE_WRITE = $00000002;
  FILE_SHARE_DELETE = $00000004;
  OPEN_EXISTING = 3;
  GENERIC_READ = $80000000;
  GENERIC_WRITE = $40000000;
  DELETE_ACCESS = $00010000;
  SYNCHRONIZE = $00100000;
  STARTF_USESHOWWINDOW = $00000001;
  MAX_STOP_WAIT_MS = 30000;
  InstallerVersion = '{#AppVersion}';
  InstallerBaseVersion = '{#BaseVersion}';
  ExpectedExeSha256 = '{#ExeSha256}';

type
  TDashboardStartupInfo = record
    cb: Cardinal;
    lpReserved: String;
    lpDesktop: String;
    lpTitle: String;
    dwX: Cardinal;
    dwY: Cardinal;
    dwXSize: Cardinal;
    dwYSize: Cardinal;
    dwXCountChars: Cardinal;
    dwYCountChars: Cardinal;
    dwFillAttribute: Cardinal;
    dwFlags: Cardinal;
    wShowWindow: Word;
    cbReserved2: Word;
    lpReserved2: String;
    hStdInput: THandle;
    hStdOutput: THandle;
    hStdError: THandle;
  end;
  TDashboardProcessInformation = record
    hProcess: THandle;
    hThread: THandle;
    dwProcessId: Cardinal;
    dwThreadId: Cardinal;
  end;

var
  InstallingMutex: THandle;
  LaunchAllowed: Boolean;
  VerificationFailed: Boolean;
  UpdateNoLaunch: Boolean;
  UpdateMode: Boolean;
  DashboardResultPath: String;
  DashboardStateWriteFailed: Boolean;

function CreateMutexW(Attributes: Integer; InitialOwner: Boolean; Name: String): THandle;
  external 'CreateMutexW@kernel32.dll stdcall';
function OpenMutexW(DesiredAccess: Cardinal; InheritHandle: Boolean; Name: String): THandle;
  external 'OpenMutexW@kernel32.dll stdcall';
function WaitForSingleObject(Handle: THandle; Milliseconds: Cardinal): Cardinal;
  external 'WaitForSingleObject@kernel32.dll stdcall';
function CloseHandle(Handle: THandle): Boolean;
  external 'CloseHandle@kernel32.dll stdcall';
function GetExitCodeProcess(ProcessHandle: THandle; var ExitCode: Cardinal): Boolean;
  external 'GetExitCodeProcess@kernel32.dll stdcall';
function CreateProcessW(ApplicationName, CommandLine: String; ProcessAttributes, ThreadAttributes: Integer;
  InheritHandles: Boolean; CreationFlags: Cardinal; Environment, CurrentDirectory: String;
  var StartupInfo: TDashboardStartupInfo; var ProcessInformation: TDashboardProcessInformation): Boolean;
  external 'CreateProcessW@kernel32.dll stdcall';
function GetLastError(): Cardinal;
  external 'GetLastError@kernel32.dll stdcall';
function GetFileAttributesW(Name: String): Cardinal;
  external 'GetFileAttributesW@kernel32.dll stdcall';
function CreateFileW(Name: String; DesiredAccess, ShareMode, SecurityAttributes: Cardinal; CreationDisposition, FlagsAndAttributes, TemplateFile: Cardinal): THandle;
  external 'CreateFileW@kernel32.dll stdcall';

function HasParameter(const Name: String): Boolean;
var
  I: Integer;
begin
  Result := False;
  for I := 1 to ParamCount do
    if CompareText(ParamStr(I), Name) = 0 then begin
      Result := True;
      Exit;
    end;
end;

function ParameterValue(const Prefix: String): String;
var
  I: Integer;
  Value: String;
begin
  Result := '';
  for I := 1 to ParamCount do begin
    Value := ParamStr(I);
    if CompareText(Copy(Value, 1, Length(Prefix)), Prefix) = 0 then begin
      Result := Copy(Value, Length(Prefix) + 1, MaxInt);
      Exit;
    end;
  end;
end;

function InstallMutexName(): String;
var
  UserName: String;
begin
  { Global makes the lock span terminal/RDP sessions.  The username suffix
    keeps unrelated users from blocking one another. }
  UserName := ExpandConstant('{sysuserinfoname}');
  if UserName = '' then UserName := 'UnknownUser';
  Result := 'Global\RoboRevDashboard.Installing.' + UserName;
end;

function AcquireInstallingMutex(): Boolean;
var
  LastError: Cardinal;
begin
  Result := False;
  InstallingMutex := CreateMutexW(0, True, InstallMutexName());
  if InstallingMutex = 0 then begin
    SuppressibleMsgBox('RoboRev Dashboard 설치 잠금을 만들 수 없습니다. 기존 설치를 변경하지 않았습니다.', mbError, MB_OK, IDOK);
    Exit;
  end;
  LastError := GetLastError();
  if LastError = ERROR_ALREADY_EXISTS then begin
    CloseHandle(InstallingMutex);
    InstallingMutex := 0;
    SuppressibleMsgBox('다른 RoboRev Dashboard 설치 또는 제거가 이미 진행 중입니다.', mbError, MB_OK, IDOK);
    Exit;
  end;
  Result := True;
end;

procedure ReleaseInstallingMutex();
begin
  if InstallingMutex <> 0 then begin
    CloseHandle(InstallingMutex);
    InstallingMutex := 0;
  end;
end;

function PathHasReparsePoint(const Path: String): Boolean;
var
  Root, Remainder, Component, Cursor: String;
  Slash: Integer;
  Attributes: Cardinal;
begin
  Result := False;
  Root := ExtractFileDrive(Path);
  if Root = '' then Exit;
  Remainder := Copy(Path, Length(Root) + 1, MaxInt);
  Cursor := Root;
  while (Remainder <> '') and ((Remainder[1] = '\') or (Remainder[1] = '/')) do begin
    Delete(Remainder, 1, 1);
    Cursor := AddBackslash(Cursor);
  end;
  while Remainder <> '' do begin
    Slash := Pos('\', Remainder);
    if Slash = 0 then Slash := Pos('/', Remainder);
    if Slash = 0 then begin
      Component := Remainder;
      Remainder := '';
    end else begin
      Component := Copy(Remainder, 1, Slash - 1);
      Delete(Remainder, 1, Slash);
    end;
    if Component <> '' then begin
      Cursor := AddBackslash(Cursor) + Component;
      Attributes := GetFileAttributesW(Cursor);
      if Attributes <> INVALID_FILE_ATTRIBUTES then
        if (Attributes and FILE_ATTRIBUTE_REPARSE_POINT) <> 0 then begin
          Result := True;
          Exit;
        end;
    end;
  end;
end;

function ResultPathIsSafe(const Path: String): Boolean;
var
  Attributes: Cardinal;
begin
  Result := False;
  if (Path = '') or (ExtractFileDrive(Path) = '') then Exit;
  if PathHasReparsePoint(Path) then Exit;
  Attributes := GetFileAttributesW(Path);
  if (Attributes <> INVALID_FILE_ATTRIBUTES) and
    ((Attributes and FILE_ATTRIBUTE_DIRECTORY) <> 0) then Exit;
  Result := True;
end;

function WriteDashboardResult(const State, Message: String): Boolean;
var
  Content: String;
begin
  Result := True;
  if DashboardResultPath = '' then Exit;
  if not ResultPathIsSafe(DashboardResultPath) then begin
    DashboardStateWriteFailed := True;
    Result := False;
    Exit;
  end;
  Content := '{"state":"' + State + '","message":"' + Message + '"}' + #13#10;
  if not SaveStringToFile(DashboardResultPath, Content, False) then begin
    DashboardStateWriteFailed := True;
    Result := False;
  end;
end;

function ExistingExecutableIsLocked(const Path: String): Boolean;
var
  Handle: THandle;
  Attributes: Cardinal;
begin
  Result := False;
  if PathHasReparsePoint(Path) then begin
    Result := True;
    Exit;
  end;
  Attributes := GetFileAttributesW(Path);
  if Attributes = INVALID_FILE_ATTRIBUTES then Exit;
  Handle := CreateFileW(Path, GENERIC_READ or GENERIC_WRITE or DELETE_ACCESS,
    FILE_SHARE_READ or FILE_SHARE_WRITE or FILE_SHARE_DELETE, 0, OPEN_EXISTING, 0, 0);
  if Handle = INVALID_HANDLE_VALUE then begin
    Result := True;
    Exit;
  end;
  CloseHandle(Handle);
end;

function VersionNumber(const Value: String; Index: Integer): Integer;
var
  Cursor, Start, Stop, Plus: Integer;
  Core: String;
  Part: String;
begin
  Core := Value;
  Plus := Pos('+', Core);
  if Plus <> 0 then Delete(Core, Plus, MaxInt);
  Cursor := 1;
  Start := 1;
  while (Cursor <= Length(Core)) and (Core[Cursor] <> '-') do begin
    if Core[Cursor] = '.' then begin
      if Index = 1 then begin
        Part := Copy(Core, Start, Cursor - Start);
        Result := StrToIntDef(Part, -1);
        Exit;
      end;
      Start := Cursor + 1;
      Index := Index - 1;
    end;
    Cursor := Cursor + 1;
  end;
  Stop := Cursor;
  if Index = 1 then Result := StrToIntDef(Copy(Core, Start, Stop - Start), -1)
  else Result := -1;
end;

function VersionSuffix(const Value: String): String;
var
  Dash, Plus: Integer;
  Core: String;
begin
  Core := Value;
  Plus := Pos('+', Core);
  if Plus <> 0 then Delete(Core, Plus, MaxInt);
  Dash := Pos('-', Core);
  if Dash = 0 then Result := ''
  else Result := Copy(Core, Dash + 1, MaxInt);
end;

function IsVersionIdentifier(const Value: String): Boolean;
var
  I: Integer;
begin
  Result := Value <> '';
  for I := 1 to Length(Value) do
    if not (((Value[I] >= '0') and (Value[I] <= '9')) or
      ((Value[I] >= 'A') and (Value[I] <= 'Z')) or
      ((Value[I] >= 'a') and (Value[I] <= 'z')) or
      (Value[I] = '-')) then Result := False;
end;

function IsNumericIdentifier(const Value: String): Boolean;
var
  I: Integer;
begin
  Result := Value <> '';
  for I := 1 to Length(Value) do
    if (Value[I] < '0') or (Value[I] > '9') then Result := False;
end;

function TakeIdentifier(var Value: String): String;
var
  Dot: Integer;
begin
  Dot := Pos('.', Value);
  if Dot = 0 then begin Result := Value; Value := ''; end
  else begin Result := Copy(Value, 1, Dot - 1); Delete(Value, 1, Dot); end;
end;

function VersionEvidenceValid(const Value: String): Boolean;
var
  Base, Build, Prerelease, Part: String;
  Plus, Dash, I, Numeric: Integer;
begin
  Result := False;
  if (Value = '') or (Trim(Value) <> Value) then Exit;
  Base := Value;
  Plus := Pos('+', Base);
  if Plus <> 0 then begin
    Build := Copy(Base, Plus + 1, MaxInt);
    if (Build = '') or (Build[1] = '.') or (Build[Length(Build)] = '.') or
      (Pos('..', Build) <> 0) or (Pos('+', Build) <> 0) then Exit;
    while Build <> '' do begin
      Part := TakeIdentifier(Build);
      if not IsVersionIdentifier(Part) then Exit;
    end;
    Delete(Base, Plus, MaxInt);
  end;
  Dash := Pos('-', Base);
  if Dash <> 0 then begin
    Prerelease := Copy(Base, Dash + 1, MaxInt);
    if (Prerelease = '') or (Prerelease[1] = '.') or (Prerelease[Length(Prerelease)] = '.') or
      (Pos('..', Prerelease) <> 0) then Exit;
    while Prerelease <> '' do begin
      Part := TakeIdentifier(Prerelease);
      if not IsVersionIdentifier(Part) then Exit;
      if IsNumericIdentifier(Part) and (Length(Part) > 1) and (Part[1] = '0') then Exit;
    end;
    Delete(Base, Dash, MaxInt);
  end;
  if (Base = '') or (Base[1] = '.') or (Base[Length(Base)] = '.') or
    (Pos('..', Base) <> 0) then Exit;
  for I := 1 to 3 do begin
    Part := TakeIdentifier(Base);
    if (Part = '') or (not IsNumericIdentifier(Part)) then Exit;
    if (Length(Part) > 1) and (Part[1] = '0') then Exit;
    Numeric := StrToIntDef(Part, -1);
    if Numeric < 0 then Exit;
  end;
  Result := Base = '';
end;

function ComparePrerelease(Left, Right: String): Integer;
var
  L, R: String;
  LN, RN: Boolean;
begin
  Result := 0;
  while (Left <> '') and (Right <> '') do begin
    L := TakeIdentifier(Left); R := TakeIdentifier(Right);
    LN := IsNumericIdentifier(L); RN := IsNumericIdentifier(R);
    if LN and RN and (Length(L) <> Length(R)) then begin Result := Length(L) - Length(R); Exit; end;
    if LN and (not RN) then begin Result := -1; Exit; end;
    if RN and (not LN) then begin Result := 1; Exit; end;
    Result := CompareStr(L, R);
    if Result <> 0 then Exit;
  end;
  if Left <> '' then Result := 1 else if Right <> '' then Result := -1;
end;

function CompareVersions(const Left, Right: String): Integer;
var
  I, L, R: Integer;
  LeftSuffix, RightSuffix: String;
begin
  for I := 1 to 3 do begin
    L := VersionNumber(Left, I); R := VersionNumber(Right, I);
    if (L < 0) or (R < 0) then begin Result := 0; Exit; end;
    if L < R then begin Result := -1; Exit; end;
    if L > R then begin Result := 1; Exit; end;
  end;
  LeftSuffix := VersionSuffix(Left); RightSuffix := VersionSuffix(Right);
  if (LeftSuffix = '') and (RightSuffix <> '') then Result := 1
  else if (LeftSuffix <> '') and (RightSuffix = '') then Result := -1
  else Result := ComparePrerelease(LeftSuffix, RightSuffix);
end;

function TryGracefulStop(const ExePath: String): Boolean;
var
  ChildExitCode: Cardinal;
  CommandLine: String;
  ProcessHandle: THandle;
  ProcessInfo: TDashboardProcessInformation;
  StartupInfo: TDashboardStartupInfo;
  WaitResult: Cardinal;
begin
  Result := False;
  if PathHasReparsePoint(ExePath) then Exit;
  { Interactive Setup asks immediately before the stop request.  A silent
    Setup is itself the explicit -SilentInstall choice made by build-local, so
    it sends the same exact-path request without a second UI prompt. }
  if (not WizardSilent) and
     (MsgBox('RoboRev Dashboard가 실행 중입니다. 설치 전에 --stop-installed로 정상 종료를 요청하시겠습니까?', mbConfirmation, MB_YESNO) <> IDYES) then Exit;
  StartupInfo.cb := SizeOf(StartupInfo);
  StartupInfo.lpReserved := '';
  StartupInfo.lpDesktop := '';
  StartupInfo.lpTitle := '';
  StartupInfo.dwX := 0;
  StartupInfo.dwY := 0;
  StartupInfo.dwXSize := 0;
  StartupInfo.dwYSize := 0;
  StartupInfo.dwXCountChars := 0;
  StartupInfo.dwYCountChars := 0;
  StartupInfo.dwFillAttribute := 0;
  StartupInfo.dwFlags := STARTF_USESHOWWINDOW;
  StartupInfo.wShowWindow := SW_HIDE;
  StartupInfo.cbReserved2 := 0;
  StartupInfo.lpReserved2 := '';
  StartupInfo.hStdInput := 0;
  StartupInfo.hStdOutput := 0;
  StartupInfo.hStdError := 0;
  ProcessInfo.hProcess := 0;
  ProcessInfo.hThread := 0;
  ProcessInfo.dwProcessId := 0;
  ProcessInfo.dwThreadId := 0;
  CommandLine := '"' + ExePath + '" --stop-installed';
  if not CreateProcessW(ExePath, CommandLine, 0, 0, False, 0, '', ExpandConstant('{app}'), StartupInfo, ProcessInfo) then Exit;
  if ProcessInfo.hThread <> 0 then CloseHandle(ProcessInfo.hThread);
  ProcessHandle := ProcessInfo.hProcess;
  if ProcessHandle = 0 then Exit;
  try
    WaitResult := WaitForSingleObject(ProcessHandle, MAX_STOP_WAIT_MS);
    if WaitResult <> WAIT_OBJECT_0 then Exit;
    if not GetExitCodeProcess(ProcessHandle, ChildExitCode) then Exit;
    if ChildExitCode <> 0 then Exit;
  finally
    CloseHandle(ProcessHandle);
  end;
  Result := not ExistingExecutableIsLocked(ExePath);
end;

function DestinationFilesAreSafe(const AppDir: String): Boolean;
begin
  Result :=
    (not PathHasReparsePoint(AddBackslash(AppDir) + '{#AppExeName}')) and
    (not PathHasReparsePoint(AddBackslash(AppDir) + 'roborev-dashboard.manifest.json')) and
    (not PathHasReparsePoint(AddBackslash(AppDir) + 'roborev-dashboard.sha256'));
end;

function InstallPreconditionError(const Uninstalling: Boolean): String;
var
  AppDir, ExePath, InstalledVersion: String;
  RegistryKey: String;
begin
  Result := '';
  InstalledVersion := '';
  AppDir := ExpandConstant('{app}');
  if PathHasReparsePoint(AppDir) then begin
    Result := 'RoboRev Dashboard 설치 경로에 reparse point가 있어 안전하게 진행할 수 없습니다.';
    Exit;
  end;
  if not DestinationFilesAreSafe(AppDir) then begin
    Result := 'RoboRev Dashboard 설치 파일 중 하나가 reparse point라 안전하게 진행할 수 없습니다.';
    Exit;
  end;
  RegistryKey := 'Software\Microsoft\Windows\CurrentVersion\Uninstall\' + Chr(123) + '{#AppId}' + Chr(125) + '_is1';
  if not Uninstalling then begin
    if not VersionEvidenceValid(InstallerBaseVersion) then begin
      Result := '이번 설치 패키지의 기준 버전 정보가 올바르지 않아 진행할 수 없습니다.';
      Exit;
    end;
    if not RegQueryStringValue(HKCU, RegistryKey, 'BuildVersion', InstalledVersion) then
      RegQueryStringValue(HKCU, RegistryKey, 'DisplayVersion', InstalledVersion);
    if InstalledVersion <> '' then begin
      if not VersionEvidenceValid(InstalledVersion) then begin
        Result := '기존 RoboRev Dashboard 버전 정보가 손상되어 안전하게 덮어쓸 수 없습니다.';
        Exit;
      end;
      if CompareVersions(InstallerBaseVersion, InstalledVersion) < 0 then begin
        Result := '설치하려는 RoboRev Dashboard 버전이 현재 설치된 버전보다 낮습니다. 기존 설치는 변경되지 않았습니다.';
        Exit;
      end;
    end;
  end;
  ExePath := AddBackslash(AppDir) + '{#AppExeName}';
  if ExistingExecutableIsLocked(ExePath) then begin
    if not TryGracefulStop(ExePath) then begin
      Result := '기존 RoboRev Dashboard 실행 파일이 사용 중이어서 설치 또는 제거를 중단했습니다. 앱을 정상 종료한 뒤 다시 시도하세요.';
      Exit;
    end;
  end;
end;

function VerifyInstalledExecutable(): Boolean;
var
  ExePath, ActualHash: String;
begin
  ExePath := AddBackslash(ExpandConstant('{app}')) + '{#AppExeName}';
  Result := False;
  if PathHasReparsePoint(ExePath) then begin
    SuppressibleMsgBox('설치된 Dashboard 실행 파일 경로에 reparse point가 있어 실행을 건너뜁니다.', mbError, MB_OK, IDOK);
    Exit;
  end;
  if (ExpectedExeSha256 = '') or (ExpectedExeSha256 = '__MISSING__') then Exit;
  if not FileExists(ExePath) then Exit;
  ActualHash := GetSHA256OfFile(ExePath);
  Result := CompareText(ActualHash, ExpectedExeSha256) = 0;
  if not Result then
    SuppressibleMsgBox('설치된 Dashboard 실행 파일의 SHA-256이 이번 패키지와 일치하지 않습니다. 실행을 건너뜁니다.', mbError, MB_OK, IDOK);
end;

function ShouldLaunchAfterInstall(): Boolean;
begin
  Result := LaunchAllowed and (not WizardSilent) and (not UpdateNoLaunch) and (not HasParameter('/DASHBOARDNOLAUNCH'));
end;

function ShouldLaunchAfterSilentInstall(): Boolean;
begin
  Result := LaunchAllowed and WizardSilent and (not UpdateNoLaunch) and (not HasParameter('/DASHBOARDNOLAUNCH'));
end;

function InitializeSetup(): Boolean;
begin
  InstallingMutex := 0;
  LaunchAllowed := False;
  VerificationFailed := False;
  UpdateMode := False;
  DashboardStateWriteFailed := False;
  DashboardResultPath := ParameterValue('/DASHBOARDRESULT=');
  UpdateNoLaunch := HasParameter('/DASHBOARDNOLAUNCH');
  if DashboardResultPath <> '' then begin
    if not ResultPathIsSafe(DashboardResultPath) then begin
      DashboardResultPath := '';
      Result := False;
      Exit;
    end;
    if not WriteDashboardResult('ready', 'setup-ready') then begin
      SuppressibleMsgBox('설치 결과 상태 파일을 준비할 수 없습니다.', mbError, MB_OK, IDOK);
      Result := False;
      Exit;
    end;
  end;
  Result := AcquireInstallingMutex();
  if not Result then begin
    WriteDashboardResult('failed', 'mutex');
    ReleaseInstallingMutex();
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ErrorText: String;
begin
  { This is the only installation-time process stop point.  The call happens
    after the user has accepted Setup's install confirmation. }
  ErrorText := InstallPreconditionError(False);
  if ErrorText <> '' then begin
    WriteDashboardResult('failed', 'precondition');
    Result := ErrorText;
    Exit;
  end;
  if not WriteDashboardResult('installing', 'copy-started') then begin
    Result := '설치 결과 상태 파일을 기록할 수 없어 설치를 중단했습니다.';
    Exit;
  end;
  Result := '';
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then begin
    LaunchAllowed := VerifyInstalledExecutable();
    VerificationFailed := not LaunchAllowed;
    if LaunchAllowed then begin
      if not RegWriteStringValue(HKCU,
        'Software\Microsoft\Windows\CurrentVersion\Uninstall\' + Chr(123) + '{#AppId}' + Chr(125) + '_is1',
        'BuildVersion', '{#BaseVersion}') then begin
        LaunchAllowed := False;
        VerificationFailed := True;
        WriteDashboardResult('failed', 'registry');
      end else if not WriteDashboardResult('succeeded', 'verified') then begin
        LaunchAllowed := False;
        DashboardStateWriteFailed := True;
      end;
    end else
      WriteDashboardResult('failed', 'verification');
  end;
end;

function GetCustomSetupExitCode(): Integer;
begin
  { Setup calls this only when it would otherwise return zero.  A payload hash
    failure therefore cannot be reported as a successful installation even
    though CurStepChanged runs after the copy phase. }
  if VerificationFailed or DashboardStateWriteFailed then Result := 19
  else Result := 0;
end;

procedure DeinitializeSetup();
begin
  ReleaseInstallingMutex();
end;

function InitializeUninstall(): Boolean;
begin
  InstallingMutex := 0;
  LaunchAllowed := False;
  DashboardResultPath := '';
  DashboardStateWriteFailed := False;
  UpdateNoLaunch := True;
  Result := AcquireInstallingMutex();
  if not Result then ReleaseInstallingMutex();
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ErrorText: String;
begin
  { usUninstall is reached only after Inno's own uninstall confirmation.  A
    cancellation before this event leaves the running Dashboard untouched. }
  if CurUninstallStep = usUninstall then begin
    ErrorText := InstallPreconditionError(True);
    if ErrorText <> '' then begin
      SuppressibleMsgBox(ErrorText, mbError, MB_OK, IDOK);
      Abort;
    end;
  end else if CurUninstallStep = usPostUninstall then begin
    RegDeleteValue(HKCU,
      'Software\Microsoft\Windows\CurrentVersion\Uninstall\' + Chr(123) + '{#AppId}' + Chr(125) + '_is1',
      'BuildVersion');
  end;
end;

procedure DeinitializeUninstall();
begin
  ReleaseInstallingMutex();
end;
