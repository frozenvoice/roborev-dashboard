[Setup]
; Dark product-family presentation layer for Inno Setup 6.7.3.
; The procedure bodies match SIDETALK installer/SetupTheme.iss at bc9f0b9, which
; is the WebPier theme (7ad1f11) plus the taller failure detail. CycleArc's
; 600x340 charcoal window is the shared look. RoboRev Dashboard includes this
; file from installer/RoboRevDashboard.iss and does not let it own install policy.
;
; This file only changes how the wizard looks. Every page, button, checkbox,
; progress value and error message still belongs to Inno Setup and to the
; including script: the Install button is Inno's own NextButton, the desktop
; option writes Inno's own task selection, the progress bar mirrors Inno's real
; file progress, the failure text is exactly what the script's PrepareToInstall
; returned, and silent, very silent, logging, uninstall, exit codes and message
; boxes are untouched. Nothing here closes, stops or inspects any process. The
; look is a 600x340 frameless charcoal window with soft amber, magenta and teal
; light pools, one light-blue accent and thin panels.
;
; The file starts with a section header because it is included from inside the
; product's [Code] section, where a ';' line would be Pascal, not a comment.
; Include it at the end of the product script, after its [Code], with:
;   #define public SetupThemeAppName "RoboRev Dashboard"
;   #define public SetupThemeAppObject "RoboRev Dashboard를"
;   #define public SetupThemeVersion AppVersion
;   #define public SetupThemeNote "..."               ; optional one-line note
;   #define public SetupThemeTask "desktopicon"       ; optional [Tasks] entry shown
;   #define public SetupThemeTaskCaption "..."        ;   as one checkbox on the
;                                                     ;   confirmation page
;   #define public SetupThemeArt "..\assets\installer" ; go run ./scripts/setupart
; (public, because ISPP keeps a plain #define private to the defining file)
; and define these two functions in the product [Code]:
;   function SetupThemeMode(): Integer;     0 install, 1 update, 2 reinstall
;   function SetupThemeFailure(): String;   the PrepareToInstall failure, or ''
;
; Windows high contrast (and /NOSTYLE) disables Inno's custom style. The window
; then keeps the same compact layout inside a normal system frame, with system
; colours and no decorative glow, as CycleArc does.

#ifndef SetupThemeNote
  #define SetupThemeNote ""
#endif

; Polar's dark appearance is Inno's built-in style with a filled accent default
; button, which is the closest built-in match to CycleArc's primary button.
; WizardBackColor makes message boxes and the uninstaller charcoal as well.
WizardStyle=modern dark polar hidebevels
WizardBackColor=#141416
WizardBackImageFile={#SetupThemeArt}\setup-backdrop-1x.png,{#SetupThemeArt}\setup-backdrop-2x.png
WizardImageFile=
WizardSmallImageFile=

[Files]
; A separate solid chunk: these are read before the wizard opens, and inside the
; product's chunk each one would first decompress the whole payload before it.
Source: "{#SetupThemeArt}\setup-brand.png"; Flags: dontcopy solidbreak
Source: "{#SetupThemeArt}\setup-logo.png"; Flags: dontcopy
Source: "{#SetupThemeArt}\setup-done.png"; Flags: dontcopy
Source: "{#SetupThemeArt}\setup-failed.png"; Flags: dontcopy
Source: "{#SetupThemeArt}\setup-panel-location.png"; Flags: dontcopy
Source: "{#SetupThemeArt}\setup-panel-detail.png"; Flags: dontcopy
Source: "{#SetupThemeArt}\setup-track.png"; Flags: dontcopy
Source: "{#SetupThemeArt}\setup-minimize.png"; Flags: dontcopy
Source: "{#SetupThemeArt}\setup-close.png"; Flags: dontcopy

[Code]
const
  { Layout, in 96-DPI units of the 600x340 client area (CycleArc SetupLayout).
    The page notebooks fill the client area, so page and window coordinates
    are the same. }
  ThemeClientWidth = 600;
  ThemeClientHeight = 340;

  { CycleArc SetupPalette.Dark as TColor ($BBGGRR). }
  ThemeText = $F6F4F4;
  ThemeTextSecondary = $B3A9A9;
  ThemeTextMuted = $958A8A;
  ThemePathText = $DAD3D3;
  ThemeAccent = $FFBB8D;
  ThemePanel = $1F1B1B;
  ThemePanelText = $D2C9C9;
  ThemeBorder = $342E2E;

  ThemeModeInstall = 0;
  ThemeModeUpdate = 1;
  ThemeModeReinstall = 2;

  ThemeGWL_WNDPROC = -4;
  ThemeGWL_STYLE = -16;
  ThemeWS_BORDER = $00800000;
  ThemeWM_NCHITTEST = $0084;
  ThemeWM_SYSCOMMAND = $0112;
  ThemeSC_MINIMIZE = $F020;
  ThemeHTTRANSPARENT = -1;
  ThemeHTCLIENT = 1;
  ThemeHTCAPTION = 2;
  ThemeDWMWA_WINDOW_CORNER_PREFERENCE = 33;
  ThemeDWMWA_BORDER_COLOR = 34;
  ThemeDWMWCP_ROUND = 2;

var
  ThemeStyled: Boolean;
  ThemeMode: Integer;
  ThemeBrandName, ThemeBrandVersion: TNewStaticText;
  ThemeBrandMark: TBitmapImage;
  ThemeMinimize, ThemeClose: TBitmapButton;
  ThemeReadyHeading, ThemeReadyBody, ThemeReadyLocation, ThemeReadyPath, ThemeReadyNote: TNewStaticText;
  ThemeReadyIcon, ThemeReadyPanel: TBitmapImage;
  ThemeTask: TNewCheckBox;
  ThemeProgressHeading, ThemeProgressHint: TNewStaticText;
  ThemeTrack: TBitmapImage;
  ThemeFill: TPanel;
  ThemeDoneHeading, ThemeDoneBody, ThemeDoneLocation, ThemeDonePath: TNewStaticText;
  ThemeDoneIcon, ThemeDonePanel: TBitmapImage;
  ThemeRun: TNewCheckBox;
  ThemeFailedHeading: TNewStaticText;
  ThemeFailedIcon, ThemeFailedPanel: TBitmapImage;
  ThemeFailedDetail: TNewMemo;
  ThemeHookedWindows: array of HWND;
  ThemeHookedProcs: array of Longint;

function ThemeDwmSetWindowAttribute(Wnd: HWND; Attribute: Integer; var Value: Integer; Size: Integer): Integer;
  external 'DwmSetWindowAttribute@dwmapi.dll stdcall delayload';
function ThemeSetWindowLong(Wnd: HWND; Index: Integer; NewLong: Longint): Longint;
  external 'SetWindowLongW@user32.dll stdcall';
function ThemeGetWindowLong(Wnd: HWND; Index: Integer): Longint;
  external 'GetWindowLongW@user32.dll stdcall';
function ThemeCallWindowProc(Proc: Longint; Wnd: HWND; Msg: Cardinal; WParam, LParam: Longint): Longint;
  external 'CallWindowProcW@user32.dll stdcall';

{ --- Text --------------------------------------------------------------- }

function ThemeConfirmHeading(): String;
begin
  case ThemeMode of
    ThemeModeUpdate: Result := '{#SetupThemeAppName} 업데이트';
    ThemeModeReinstall: Result := '{#SetupThemeAppName} 다시 설치';
  else
    Result := '{#SetupThemeAppName} 설치';
  end;
end;

function ThemeConfirmBody(): String;
begin
  case ThemeMode of
    ThemeModeUpdate: Result := '기존 설치를 같은 위치에서 업데이트합니다. 프로필과 설정은 그대로 유지됩니다.';
    ThemeModeReinstall: Result := '같은 버전을 같은 위치에 다시 설치합니다. 프로필과 설정은 그대로 유지됩니다.';
  else
    Result := '현재 Windows 계정에만 설치합니다. 관리자 권한은 필요하지 않습니다.';
  end;
end;

function ThemeIsUpdate(): Boolean;
begin
  Result := ThemeMode = ThemeModeUpdate;
end;

{ --- Building blocks ----------------------------------------------------- }

function ThemeFontHeight(Tenths: Integer): Integer;
begin
  { Pixel heights from CycleArc's SetupFonts, scaled like the layout. }
  Result := -((ScaleY(Tenths) + 5) div 10);
end;

{ Boxes are scaled one edge at a time, so neighbours keep a few units apart:
  boxes that merely touch at 96 DPI overlap by a rounded pixel at 213%. }
procedure ThemePlace(Control: TControl; X, Y, W, H: Integer);
begin
  Control.SetBounds(ScaleX(X), ScaleY(Y), ScaleX(W), ScaleY(H));
end;

function ThemeLabel(Parent: TWinControl; X, Y, W, H: Integer; Tenths, Color: Integer;
  Bold, Centered, Wrap: Boolean): TNewStaticText;
begin
  Result := TNewStaticText.Create(WizardForm);
  Result.Parent := Parent;
  Result.AutoSize := False;
  Result.WordWrap := Wrap;
  Result.ShowAccelChar := False;
  if Centered then
    Result.Alignment := taCenter;
  Result.Font.Name := 'Malgun Gothic';
  Result.Font.Height := ThemeFontHeight(Tenths);
  if Bold then
    Result.Font.Style := [fsBold];
  if ThemeStyled then begin
    Result.StyleElements := Result.StyleElements - [seFont];
    Result.Font.Color := Color;
  end else if Parent = WizardForm then
    { Unstyled labels are opaque; the ones over the page notebook match the
      page instead of the form face. }
    Result.Color := clWindow;
  ThemePlace(Result, X, Y, W, H);
end;

function ThemeImage(Parent: TWinControl; const Name: String; X, Y, W, H: Integer): TBitmapImage;
var
  Png: TPngImage;
begin
  Result := TBitmapImage.Create(WizardForm);
  Result.Parent := Parent;
  Result.BackColor := clNone;
  Result.Stretch := True;
  ThemePlace(Result, X, Y, W, H);
  Png := TPngImage.Create;
  try
    Png.LoadFromFile(ExpandConstant('{tmp}\') + Name);
    Result.PngImage := Png;
  finally
    Png.Free;
  end;
end;

function ThemeCheckBox(Parent: TWinControl; const Caption: String; X, Y, W, H: Integer; Click: TNotifyEvent): TNewCheckBox;
begin
  Result := TNewCheckBox.Create(WizardForm);
  Result.Parent := Parent;
  Result.Caption := Caption;
  Result.Font.Name := 'Malgun Gothic';
  Result.Font.Height := ThemeFontHeight(135);
  Result.OnClick := Click;
  ThemePlace(Result, X, Y, W, H);
end;

function ThemeCaptionButton(const Name, Caption: String; X: Integer; Click: TNotifyEvent): TBitmapButton;
var
  Png: TPngImage;
begin
  Result := TBitmapButton.Create(WizardForm);
  Result.Parent := WizardForm;
  Result.Caption := Caption;
  Result.Hint := Caption;
  Result.ShowHint := True;
  Result.Stretch := True;
  Result.Cursor := crHand;
  Result.OnClick := Click;
  ThemePlace(Result, X, 8, 36, 30);
  Png := TPngImage.Create;
  try
    Png.LoadFromFile(ExpandConstant('{tmp}\') + Name);
    Result.PngImage := Png;
  finally
    Png.Free;
  end;
end;

procedure ThemeButton(Button: TNewButton; X: Integer; const Caption: String);
begin
  Button.Caption := Caption;
  ThemePlace(Button, X, 270, 100, 36);
end;

{ --- Frameless window ---------------------------------------------------- }

{ Empty space drags the window, as a title bar would. The pages and notebooks
  pass the hit through to the form beneath them; the form answers as a
  caption. Controls such as buttons and labels keep their own answers. }
function ThemeWindowProc(Wnd: HWND; Msg: Cardinal; WParam, LParam: Longint): Longint;
var
  I: Integer;
begin
  Result := 0;
  for I := 0 to GetArrayLength(ThemeHookedWindows) - 1 do begin
    if ThemeHookedWindows[I] = Wnd then begin
      Result := ThemeCallWindowProc(ThemeHookedProcs[I], Wnd, Msg, WParam, LParam);
      if (Msg = ThemeWM_NCHITTEST) and (Result = ThemeHTCLIENT) then begin
        if Wnd = WizardForm.Handle then
          Result := ThemeHTCAPTION
        else
          Result := ThemeHTTRANSPARENT;
      end;
      Exit;
    end;
  end;
end;

procedure ThemeHook(Wnd: HWND; Proc: Longint);
var
  Count: Integer;
begin
  Count := GetArrayLength(ThemeHookedWindows);
  SetArrayLength(ThemeHookedWindows, Count + 1);
  SetArrayLength(ThemeHookedProcs, Count + 1);
  ThemeHookedWindows[Count] := Wnd;
  ThemeHookedProcs[Count] := ThemeSetWindowLong(Wnd, ThemeGWL_WNDPROC, Proc);
end;

procedure ThemeHookDragging();
var
  Proc: Longint;
begin
  Proc := CreateCallback(@ThemeWindowProc);
  ThemeHook(WizardForm.Handle, Proc);
  ThemeHook(WizardForm.OuterNotebook.Handle, Proc);
  ThemeHook(WizardForm.InnerPage.Handle, Proc);
  ThemeHook(WizardForm.InnerNotebook.Handle, Proc);
  ThemeHook(WizardForm.ReadyPage.Handle, Proc);
  ThemeHook(WizardForm.PreparingPage.Handle, Proc);
  ThemeHook(WizardForm.InstallingPage.Handle, Proc);
  ThemeHook(WizardForm.FinishedPage.Handle, Proc);
end;

{ The callbacks live in the script engine, so they are removed before Setup
  tears the wizard down. }
procedure ThemeUnhookDragging();
var
  I: Integer;
begin
  for I := GetArrayLength(ThemeHookedWindows) - 1 downto 0 do
    ThemeSetWindowLong(ThemeHookedWindows[I], ThemeGWL_WNDPROC, ThemeHookedProcs[I]);
  SetArrayLength(ThemeHookedWindows, 0);
  SetArrayLength(ThemeHookedProcs, 0);
end;

procedure ThemeMinimizeClick(Sender: TObject);
begin
  SendMessage(WizardForm.Handle, ThemeWM_SYSCOMMAND, ThemeSC_MINIMIZE, 0);
end;

procedure ThemeCloseClick(Sender: TObject);
begin
  { The close button means what the page's own button means: Cancel (with
    Inno's confirmation where Inno asks for one) while Cancel is offered, and
    Finish on the last page, where Inno ignores a plain close request. }
  if WizardForm.CancelButton.Visible and WizardForm.CancelButton.Enabled then
    WizardForm.CancelButton.OnClick(WizardForm.CancelButton)
  else if (WizardForm.CurPageID = wpFinished) and WizardForm.NextButton.Enabled then
    WizardForm.NextButton.OnClick(WizardForm.NextButton);
end;

procedure ThemeFrame();
var
  Value: Integer;
  Rounded: Boolean;
begin
  WizardForm.BorderStyle := bsNone;
  WizardForm.ClientWidth := ScaleX(ThemeClientWidth);
  WizardForm.ClientHeight := ScaleY(ThemeClientHeight);
  Rounded := False;
  try
    Value := ThemeDWMWCP_ROUND;
    Rounded := ThemeDwmSetWindowAttribute(WizardForm.Handle, ThemeDWMWA_WINDOW_CORNER_PREFERENCE, Value, 4) = 0;
    Value := ThemeBorder;
    ThemeDwmSetWindowAttribute(WizardForm.Handle, ThemeDWMWA_BORDER_COLOR, Value, 4);
  except
    Rounded := False;
  end;
  if not Rounded then begin
    { Before Windows 11 there is no rounded DWM frame; a thin border keeps the
      dark window distinct from a dark desktop. }
    ThemeSetWindowLong(WizardForm.Handle, ThemeGWL_STYLE,
      ThemeGetWindowLong(WizardForm.Handle, ThemeGWL_STYLE) or ThemeWS_BORDER);
    WizardForm.ClientWidth := ScaleX(ThemeClientWidth);
    WizardForm.ClientHeight := ScaleY(ThemeClientHeight);
  end;
end;

{ --- Pages ---------------------------------------------------------------- }

procedure ThemeBuildChrome();
begin
  { A picture is not a window, so the mark moves onto whichever page shows. }
  ThemeBrandMark := ThemeImage(WizardForm.ReadyPage, 'setup-brand.png', 24, 19, 18, 18);
  ThemeBrandName := ThemeLabel(WizardForm, 51, 12, 80, 32, 130, ThemeText, True, False, False);
  ThemeBrandName.Caption := '{#SetupThemeAppName}';
  ThemeBrandName.AutoSize := True;
  ThemeBrandName.Top := ScaleY(12) + (ScaleY(32) - ThemeBrandName.Height) div 2;
  ThemeBrandVersion := ThemeLabel(WizardForm, 0, 12, 200, 32, 125, ThemeTextMuted, False, False, False);
  ThemeBrandVersion.Caption := '{#SetupThemeVersion}';
  ThemeBrandVersion.AutoSize := True;
  ThemeBrandVersion.Left := ThemeBrandName.Left + ThemeBrandName.Width + ScaleX(8);
  ThemeBrandVersion.Top := ScaleY(12) + (ScaleY(32) - ThemeBrandVersion.Height) div 2;
  if ThemeStyled then begin
    ThemeMinimize := ThemeCaptionButton('setup-minimize.png', '최소화', 518, @ThemeMinimizeClick);
    ThemeClose := ThemeCaptionButton('setup-close.png', '닫기', 556, @ThemeCloseClick);
  end;
end;

{ Written through at once into Inno's own task selection, which [Icons] reads
  when files are installed. /TASKS= and a previous installation's choice still
  decide the starting state. }
procedure ThemeTaskClick(Sender: TObject);
begin
#ifdef SetupThemeTask
  if ThemeTask.Checked then
    WizardSelectTasks('{#SetupThemeTask}')
  else
    WizardSelectTasks('!{#SetupThemeTask}');
#endif
end;

procedure ThemeBuildReady();
var
  Page: TWinControl;
begin
  Page := WizardForm.ReadyPage;
  ThemeReadyIcon := ThemeImage(Page, 'setup-logo.png', 278, 34, 44, 44);
  ThemeReadyHeading := ThemeLabel(Page, 40, 88, 520, 40, 250, ThemeText, True, True, False);
  ThemeReadyBody := ThemeLabel(Page, 40, 130, 520, 40, 135, ThemeTextSecondary, False, True, True);
  ThemeReadyPanel := ThemeImage(Page, 'setup-panel-location.png', 80, 180, 440, 38);
  ThemeReadyLocation := ThemeLabel(Page, 94, 189, 72, 20, 125, ThemeTextMuted, False, False, False);
  ThemeReadyLocation.Caption := '설치 위치';
  ThemeReadyPath := ThemeLabel(Page, 174, 189, 332, 20, 125, ThemePathText, False, False, False);
  { Malgun Gothic draws a backslash as a won sign; paths use the Latin face. }
  ThemeReadyPath.Font.Name := 'Segoe UI';
#ifdef SetupThemeTask
  ThemeTask := ThemeCheckBox(Page, '{#SetupThemeTaskCaption}', 80, 226, 440, 28, @ThemeTaskClick);
#endif
  ThemeReadyNote := ThemeLabel(Page, 40, 279, 300, 20, 125, ThemeTextMuted, False, False, False);
  ThemeReadyNote.Caption := '{#SetupThemeNote}';
end;

procedure ThemeBuildProgress();
var
  Page: TWinControl;
begin
  Page := WizardForm.InstallingPage;
  ThemeProgressHeading := ThemeLabel(Page, 40, 104, 520, 40, 250, ThemeText, True, True, False);
  ThemeTrack := ThemeImage(Page, 'setup-track.png', 80, 212, 440, 4);
  { A window, not a picture: Inno copies files on the UI thread, and only a
    window can be repainted synchronously while it does. }
  ThemeFill := TPanel.Create(WizardForm);
  ThemeFill.Parent := Page;
  ThemeFill.BevelOuter := bvNone;
  ThemeFill.Caption := '';
  ThemeFill.ParentBackground := False;
  ThemeFill.StyleElements := [];
  ThemeFill.Color := ThemeAccent;
  ThemePlace(ThemeFill, 80, 212, 0, 4);
  ThemeFill.Width := 0;
  ThemeFill.Visible := False;
  ThemeProgressHint := ThemeLabel(Page, 60, 230, 480, 20, 125, ThemeTextMuted, False, True, False);
  ThemeProgressHint.Caption := '잠시 걸립니다. 이 창을 닫지 말고 기다려 주세요.';
  { Inno's own status line is the real stage text; it only moves. }
  WizardForm.StatusLabel.AutoSize := False;
  WizardForm.StatusLabel.WordWrap := False;
  WizardForm.StatusLabel.Alignment := taCenter;
  WizardForm.StatusLabel.Font.Name := 'Malgun Gothic';
  WizardForm.StatusLabel.Font.Height := ThemeFontHeight(135);
  if ThemeStyled then begin
    WizardForm.StatusLabel.StyleElements := WizardForm.StatusLabel.StyleElements - [seFont];
    WizardForm.StatusLabel.Font.Color := ThemeTextSecondary;
  end;
  ThemePlace(WizardForm.StatusLabel, 60, 148, 480, 22);
  WizardForm.FilenameLabel.Visible := False;
  WizardForm.ProgressGauge.Visible := False;
end;

{ Written through at once, so Finish, the close button and Alt+F4 all read the
  same choice from Inno's own run list. }
procedure ThemeRunClick(Sender: TObject);
begin
  if WizardForm.RunList.Items.Count > 0 then
    WizardForm.RunList.Checked[0] := ThemeRun.Checked;
end;

procedure ThemeBuildDone();
var
  Page: TWinControl;
begin
  Page := WizardForm.FinishedPage;
  ThemeDoneIcon := ThemeImage(Page, 'setup-done.png', 278, 34, 44, 44);
  ThemeDoneHeading := ThemeLabel(Page, 40, 88, 520, 40, 250, ThemeText, True, True, False);
  ThemeDoneBody := ThemeLabel(Page, 40, 130, 520, 40, 135, ThemeTextSecondary, False, True, True);
  ThemeDonePanel := ThemeImage(Page, 'setup-panel-location.png', 80, 184, 440, 38);
  ThemeDoneLocation := ThemeLabel(Page, 94, 193, 72, 20, 125, ThemeTextMuted, False, False, False);
  ThemeDoneLocation.Caption := '설치 위치';
  ThemeDonePath := ThemeLabel(Page, 174, 193, 332, 20, 125, ThemePathText, False, False, False);
  ThemeDonePath.Font.Name := 'Segoe UI';
  ThemeRun := ThemeCheckBox(Page, '{#SetupThemeAppName} 실행', 40, 274, 280, 28, @ThemeRunClick);
end;

procedure ThemeBuildFailed();
var
  Page: TWinControl;
begin
  Page := WizardForm.PreparingPage;
  ThemeFailedIcon := ThemeImage(Page, 'setup-failed.png', 280, 30, 40, 40);
  ThemeFailedHeading := ThemeLabel(Page, 40, 74, 520, 38, 250, ThemeText, True, True, False);
  { Taller than WebPier's: a refusal names its reason, what to do and that the
    existing installation is unchanged, and the log path follows it. }
  ThemeFailedPanel := ThemeImage(Page, 'setup-panel-detail.png', 40, 118, 520, 140);
  ThemeFailedDetail := TNewMemo.Create(WizardForm);
  ThemeFailedDetail.Parent := Page;
  ThemeFailedDetail.ReadOnly := True;
  ThemeFailedDetail.WordWrap := True;
  ThemeFailedDetail.ScrollBars := ssNone;
  ThemeFailedDetail.BorderStyle := bsNone;
  { Monospaced like CycleArc's detail box, which also keeps the log path's
    backslashes; Hangul falls back through Windows font linking. }
  ThemeFailedDetail.Font.Name := 'Consolas';
  ThemeFailedDetail.Font.Height := ThemeFontHeight(115);
  if ThemeStyled then begin
    ThemeFailedDetail.StyleElements := [];
    ThemeFailedDetail.Color := ThemePanel;
    ThemeFailedDetail.Font.Color := ThemePanelText;
  end;
  ThemePlace(ThemeFailedDetail, 52, 128, 496, 120);
end;

{ The whole reason stays readable: a text longer than the box gets a scroll
  bar instead of losing its last lines. Measured with a hidden label in the
  memo's font at the memo's text width. }
procedure ThemeFitDetail();
var
  Probe: TNewStaticText;
begin
  Probe := TNewStaticText.Create(WizardForm);
  try
    Probe.Visible := False;
    Probe.Parent := WizardForm.PreparingPage;
    Probe.AutoSize := False;
    Probe.WordWrap := True;
    Probe.ShowAccelChar := False;
    Probe.Font.Name := ThemeFailedDetail.Font.Name;
    Probe.Font.Height := ThemeFailedDetail.Font.Height;
    Probe.Width := ThemeFailedDetail.Width - ScaleX(8);
    Probe.Caption := ThemeFailedDetail.Lines.Text;
    Probe.AdjustHeight;
    if Probe.Height > ThemeFailedDetail.Height then
      ThemeFailedDetail.ScrollBars := ssVertical
    else
      ThemeFailedDetail.ScrollBars := ssNone;
  finally
    Probe.Free;
  end;
end;

{ The preparing page shows the failure parts only for a reported failure;
  otherwise it is a plain "preparing" heading at the progress page's height. }
procedure ThemeShowFailure(Failed: Boolean);
begin
  ThemeFailedIcon.Visible := Failed;
  ThemeFailedPanel.Visible := Failed;
  ThemeFailedDetail.Visible := Failed;
  if Failed then
    ThemePlace(ThemeFailedHeading, 40, 74, 520, 38)
  else
    ThemePlace(ThemeFailedHeading, 40, 104, 520, 40);
end;

procedure ThemeHideStock();
begin
  WizardForm.MainPanel.Visible := False;
  WizardForm.Bevel.Visible := False;
  WizardForm.Bevel1.Visible := False;
  WizardForm.BeveledLabel.Visible := False;
  WizardForm.BackButton.Visible := False;
  WizardForm.ReadyLabel.Visible := False;
  WizardForm.ReadyMemo.Visible := False;
  WizardForm.WizardBitmapImage2.Visible := False;
  WizardForm.FinishedHeadingLabel.Visible := False;
  WizardForm.FinishedLabel.Visible := False;
  WizardForm.RunList.Visible := False;
  WizardForm.YesRadio.Visible := False;
  WizardForm.NoRadio.Visible := False;
  WizardForm.PreparingErrorBitmapImage.Visible := False;
  WizardForm.PreparingLabel.Visible := False;
  WizardForm.PreparingYesRadio.Visible := False;
  WizardForm.PreparingNoRadio.Visible := False;
  WizardForm.PreparingMemo.Visible := False;
end;

<event('InitializeWizard')>
procedure ThemeInitializeWizard();
begin
  { Every image at once, in archive order. }
  ExtractTemporaryFiles('{tmp}\setup-*.png');
  ThemeStyled := IsDarkInstallMode;
  ThemeMode := SetupThemeMode();
  if ThemeStyled then
    ThemeFrame()
  else begin
    WizardForm.ClientWidth := ScaleX(ThemeClientWidth);
    WizardForm.ClientHeight := ScaleY(ThemeClientHeight);
  end;
  ThemeHideStock();
  WizardForm.OuterNotebook.SetBounds(0, 0, WizardForm.ClientWidth, WizardForm.ClientHeight);
  WizardForm.InnerNotebook.SetBounds(0, 0, WizardForm.OuterNotebook.Width, WizardForm.OuterNotebook.Height);
  ThemeBuildChrome();
  ThemeBuildReady();
  ThemeBuildProgress();
  ThemeBuildDone();
  ThemeBuildFailed();
  { The window-wide controls sit above the full-size page notebook. }
  WizardForm.NextButton.BringToFront;
  WizardForm.CancelButton.BringToFront;
  ThemeBrandName.BringToFront;
  ThemeBrandVersion.BringToFront;
  if ThemeStyled then begin
    ThemeMinimize.BringToFront;
    ThemeClose.BringToFront;
    ThemeHookDragging();
  end;
end;

<event('ShouldSkipPage')>
function ThemeShouldSkipPage(PageID: Integer): Boolean;
begin
  { The single task is a checkbox on the confirmation page instead. }
#ifdef SetupThemeTask
  Result := PageID = wpSelectTasks;
#else
  Result := False;
#endif
end;

<event('CurPageChanged')>
procedure ThemeCurPageChanged(CurPageID: Integer);
var
  Failure: String;
begin
  ThemeButton(WizardForm.CancelButton, 352, '취소');
  if CurPageID = wpFinished then
    ThemeBrandMark.Parent := WizardForm.FinishedPage
  else
    ThemeBrandMark.Parent := WizardForm.InnerNotebook.ActivePage;
  case CurPageID of
    wpReady: begin
      { Inno shows its summary memo again whenever it has something to list,
        such as a previous installation's tasks; the themed page replaces it. }
      WizardForm.ReadyLabel.Visible := False;
      WizardForm.ReadyMemo.Visible := False;
      ThemeReadyHeading.Caption := ThemeConfirmHeading();
      ThemeReadyBody.Caption := ThemeConfirmBody();
      ThemeReadyPath.Caption := MinimizePathName(WizardDirValue, ThemeReadyPath.Font, ThemeReadyPath.Width);
#ifdef SetupThemeTask
      ThemeTask.Checked := WizardIsTaskSelected('{#SetupThemeTask}');
#endif
      if ThemeIsUpdate() then
        ThemeButton(WizardForm.NextButton, 460, '업데이트')
      else
        ThemeButton(WizardForm.NextButton, 460, '설치');
    end;
    wpPreparing: begin
      { Inno shows its own error picture, label and Back/Next again for a
        failed preparation; the themed page replaces them. }
      WizardForm.PreparingErrorBitmapImage.Visible := False;
      WizardForm.PreparingLabel.Visible := False;
      WizardForm.BackButton.Visible := False;
      WizardForm.NextButton.Visible := False;
      { Inno passes through this page on every installation, also after a
        successful PrepareToInstall, so it is a failure only when the script
        reported one. }
      Failure := SetupThemeFailure();
      ThemeShowFailure(Failure <> '');
      if Failure <> '' then begin
        if ThemeIsUpdate() then
          ThemeFailedHeading.Caption := '업데이트하지 못했습니다'
        else
          ThemeFailedHeading.Caption := '설치하지 못했습니다';
        ThemeFailedDetail.Lines.Text := Failure + #13#10#13#10 + '로그: ' + ExpandConstant('{log}');
        ThemeFitDetail();
        ThemeButton(WizardForm.CancelButton, 460, '닫기');
      end else begin
        if ThemeIsUpdate() then
          ThemeFailedHeading.Caption := '{#SetupThemeAppName} 업데이트 준비 중...'
        else
          ThemeFailedHeading.Caption := '{#SetupThemeAppName} 설치 준비 중...';
        ThemeButton(WizardForm.CancelButton, 460, '취소');
      end;
    end;
    wpInstalling: begin
      if ThemeIsUpdate() then
        ThemeProgressHeading.Caption := '{#SetupThemeAppName} 업데이트 중...'
      else
        ThemeProgressHeading.Caption := '{#SetupThemeAppName} 설치 중...';
      ThemeButton(WizardForm.CancelButton, 460, '취소');
      { A focused button is drawn as the default one; Cancel stays secondary
        here and Esc still reaches it. }
      WizardForm.ActiveControl := nil;
    end;
    wpFinished: begin
      if ThemeIsUpdate() then
        ThemeDoneHeading.Caption := '업데이트가 완료되었습니다'
      else
        ThemeDoneHeading.Caption := '설치가 완료되었습니다';
      ThemeDonePath.Caption := MinimizePathName(WizardDirValue, ThemeDonePath.Font, ThemeDonePath.Width);
      { Inno's own run list stays the source of truth for [Run] postinstall;
        the themed checkbox only presents its single entry. }
      WizardForm.RunList.Visible := False;
      ThemeRun.Visible := WizardForm.RunList.Items.Count > 0;
      if ThemeRun.Visible then begin
        ThemeRun.Checked := WizardForm.RunList.Checked[0];
        ThemeDoneBody.Caption := '지금 {#SetupThemeAppObject} 실행하거나, 나중에 시작 메뉴에서 열 수 있습니다.';
      end else
        ThemeDoneBody.Caption := '시작 메뉴에서 {#SetupThemeAppObject} 열 수 있습니다.';
      ThemeButton(WizardForm.NextButton, 460, '마침');
    end;
  end;
end;

<event('CurInstallProgressChanged')>
procedure ThemeCurInstallProgressChanged(CurProgress, MaxProgress: Integer);
var
  Width: Integer;
begin
  { Inno's real file progress, never an estimate. }
  if MaxProgress > 0 then
    { Integer '/' truncates in Pascal Script; widen before dividing. }
    Width := Int64(ThemeTrack.Width) * CurProgress div MaxProgress
  else
    Width := 0;
  if Width <> ThemeFill.Width then begin
    ThemeFill.Width := Width;
    ThemeFill.Visible := Width > 0;
    { Paint now rather than when Inno next drains the message queue, as it
      does for its own progress bar. }
    ThemeFill.Repaint;
  end;
end;

<event('CancelButtonClick')>
procedure ThemeCancelButtonClick(CurPageID: Integer; var Cancel, Confirm: Boolean);
begin
  { A failed preparation has nothing left to cancel: Close exits directly.
    Everywhere else Inno's own confirmation stays. }
  if (CurPageID = wpPreparing) and (SetupThemeFailure() <> '') then
    Confirm := False;
end;

<event('DeinitializeSetup')>
procedure ThemeDeinitializeSetup();
begin
  ThemeUnhookDragging();
end;
