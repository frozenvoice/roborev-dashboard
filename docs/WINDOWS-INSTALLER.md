# Windows 로컬 빌드와 Dashboard 설치

## 실행

Windows 11 amd64에서 저장소 루트의 `build-local.cmd`를 더블클릭한다. 다른 working directory에서도 cmd 파일이 있는 checkout을 기준으로 실행한다. Windows 기본 PowerShell 5.1을 사용하며 PowerShell 7 설치, 영구 실행 정책 변경, Windows 보안 설정 변경은 요구하지 않는다.

```powershell
.\build-local.cmd
.\build-local.cmd -NoInstall
.\build-local.cmd -SilentInstall
```

기본 흐름은 preflight → 개발 도구 확인·준비 → 의존성 준비 → 전체 테스트 → production build → 설치기 생성 → 검증 → 설치 창 → 설치 결과 검증 → 실행이다. 기본 실행에서 테스트를 생략하는 옵션은 없다. 설치 창에서 사용자가 승인하거나 취소할 수 있다. `-NoInstall`은 설치기를 실행하지 않으며 기존 앱을 종료하지 않는다. `-SilentInstall`을 명시해야만 무인 설치하며, `-NoInstall`과 함께 지정하면 오류다.

개발 도구 준비는 기존 `scripts/install.ps1` 및 `prerequisites.ps1` 정책을 재사용한다. Git, Node.js 22 이상/npm, `go.mod`가 요구하는 Go 1.26.8 이상 중 부족한 도구만 winget 계획에 포함한다. Go 버전 검사는 cmd를 연 위치와 관계없이 checkout에서 선택되는 실제 컴파일러를 사용한다. 기본 `GOTOOLCHAIN=auto`는 필요한 Go 도구체인을 다운로드하고 선택할 수 있다. `GOTOOLCHAIN=local` 또는 오프라인 환경에서는 요구 버전을 직접 준비해야 하며 검사 실패를 무시하지 않는다. 해당 도구의 설치 자체가 별도 권한 승인을 요구할 수 있다. 이것은 관리자 권한을 요구하지 않는 Dashboard 앱 설치와 별개다. RoboRev와 AI agent를 준비하거나 실행하지 않는다.

Inno Setup은 WebPier에서 사용한 공식 **6.7.3** 버전으로 고정한다. 공식 HTTPS 배포 파일의 SHA-256과 서명을 검증한 후 준비하며, 검증된 캐시는 재사용한다. 다운로드·무결성·publisher 검증 실패를 무시하거나 `latest`로 대체하지 않는다. 신뢰 기준은 [고정된 공식 릴리스](https://github.com/jrsoftware/issrc/releases/tag/is-6_7_3)다.

## 결과와 실패 처리

사용자에게 안내하는 설치 파일은 다음 경로다.

```text
dist\installer\RoboRev-Dashboard-Setup.exe
```

실행별 staging, build transcript와 manifest는 `dist\local-build`의 고유 run 디렉터리에 남는다. 콘솔이 실제 run 경로와 실패 단계를 출력한다. 이전 공개 설치기와 manifest는 새 run의 `previous`로 이동하므로 실패한 실행 뒤에 오래된 파일이 최신 결과처럼 남지 않는다. 현재 결과를 검증하기 전에는 설치기를 실행하지 않는다. 같은 checkout의 로컬 빌드는 잠금으로 직렬화한다. 다른 터미널에서 별도로 build/test를 실행하거나 빌드 도중 소스를 편집하지 않는 것이 좋다.

테스트·빌드·패키징·검증 실패는 기존 설치를 건드리거나 앱을 종료하지 않는다. cmd는 실패 코드를 전달하고 오류를 읽을 수 있도록 멈춘다. 설치 취소는 성공으로 보고하지 않는다. 설치가 시작된 뒤 실패했다면 일부 파일이 교체되었을 수 있으므로 “기존 설치가 온전하다”고 단정하지 않는다. 해당 run의 설치 로그를 확인하고, 검증된 설치기로 다시 설치한다. 설치기 프로세스가 시작되었다는 사실만으로 설치 성공을 보고하지 않는다.

이 결과는 **unsigned 로컬 개발 빌드**다. 실행 파일의 기존 `--version` 및 `--version-json` 계약은 유지하며 `version=dev`, `release=false`를 사용한다. 로컬 manifest는 기준 버전, commit, dirty 상태와 실제 파일 SHA-256을 기록한다. 소스 버전 파일을 빌드마다 수정하지 않는다. 현재 기준 버전은 `web/package.json`의 `0.1.0`이다. 공식 release provenance로 표시하지 않는다.

`scripts/package.ps1`의 clean-tree, strict SemVer/commit provenance, 재현성 비교, manifest/SHA-256 검증 및 no-overwrite 출력 계약은 별도로 유지한다. `dist\roborev-dashboard.exe`를 만드는 기존 개발 빌드와 단일 EXE 실행도 계속 지원한다. CI workflow, 릴리스 게시, 자동 업데이트 채널은 이번 로컬 설치 흐름에 포함하지 않는다.

## 설치·실행·업데이트·제거

Setup은 고정 AppId로 현재 사용자에게 `%LOCALAPPDATA%\Programs\RoboRev Dashboard`에 설치한다. 설치 경로 페이지와 시작 메뉴 폴더 선택 페이지는 생략한다. 새 버전은 같은 설치 항목을 갱신하고, 같은 기준 버전의 로컬 빌드도 새 파일로 재설치할 수 있다. 명백하게 낮은 기준 버전으로의 다운그레이드는 차단한다.

설치에는 embedded frontend를 포함한 Dashboard EXE와 명시된 배포 파일만 포함한다. Go, Node.js, npm, Inno Setup, 소스, `node_modules`, 테스트 fixture 또는 사용자 설정은 설치하지 않는다. 실행 시 Git은 여전히 필요하며, RoboRev가 없거나 호환되지 않으면 기존 Agents & Setup 안내와 exact-version fail-closed 정책을 따른다. Fix/Refine 등의 차단 상태는 유지된다.

시작 메뉴의 Dashboard 항목은 `roborev-dashboard.exe --installed`를 실행한다. 같은 사용자·같은 실행 파일 경로의 설치본을 다시 열면 기존 서버가 기본 브라우저를 연다. 최초 실행 시 7410이 다른 서버에 점유되어 있으면 기존 규칙대로 다음 사용 가능한 포트를 사용한다. 포트만 보고 다른 프로세스를 Dashboard로 인정하지 않는다.

서버 콘솔은 표시한다. 종료하려면 콘솔에서 **Ctrl+C**를 누르거나 시작 메뉴의 **Stop RoboRev Dashboard** 항목을 사용한다. 브라우저 탭을 닫는 것만으로는 서버가 종료되지 않는다. 고급 사용자는 설치된 EXE의 `--installed-status`로 상태를 확인하고 `--stop-installed`로 정상 종료할 수 있다. `--installed --no-open`은 브라우저를 열지 않는다. 설치 모드와 `--port`, `--config-dir` 등 개별 실행 설정을 섞지 않고, 해당 설정이 필요하면 기존 일반 실행 모드를 사용한다.

업데이트는 승인 뒤 파일 교체 직전에 같은 설치 경로의 Dashboard만 정상 종료한다. Windows 기본 이름 기반 일괄 종료나 무관한 프로세스 종료는 하지 않는다. 정상 종료·파일 잠금 해제를 확인할 수 없으면 교체를 진행하지 않는다. 기존 in-flight operation의 shutdown 정책을 적용하며 RoboRev daemon이나 외부 review job을 임의로 중지하지 않는다.

Windows **설정 → 앱 → 설치된 앱 → RoboRev Dashboard → 제거**로 삭제한다. Dashboard가 설치한 파일, 바로가기, uninstall 등록만 제거한다. 다음 데이터는 업데이트·재설치·제거 후에도 보존한다.

- `%APPDATA%\roborev-dashboard\config.json`의 등록 저장소와 Dashboard 설정
- `~/.roborev`, RoboRev binary/daemon/review DB와 데이터
- Git 저장소와 hook, agent 설정·인증정보·API key·토큰
- 사용자 PATH와 Windows 보안 정책

설치된 앱은 설치 폴더나 바로가기 working directory에 사용자 데이터를 쓰지 않는다. Dashboard 앱 설치와 기존 **Agents & Setup → Install RoboRev**는 서로 다른 기능이다.

## 검증과 Windows Sandbox

자동 단위·회귀 테스트는 fake, 임시 파일과 격리된 프로세스를 사용한다. 실제 RoboRev review와 유료 LLM 호출은 하지 않는다. 로컬 wrapper의 실패 주입 검증과 실제 설치·제거 E2E의 성공을 혼동하지 않는다.

`scripts/prepare-installer-sandbox.ps1`은 현재 검증한 Setup/EXE로 disposable Windows Sandbox 입력을 만든다. 호스트에서 설치기를 실행하거나 Sandbox 기능을 활성화하지 않는다.

```powershell
.\scripts\prepare-installer-sandbox.ps1 `
  -SetupPath .\dist\installer\RoboRev-Dashboard-Setup.exe `
  -ExecutablePath '<해당 run 디렉터리의 roborev-dashboard.exe 경로>'
```

생성된 `.wsb`는 Windows Sandbox가 이미 준비된 Windows 11 머신에서 연다. 소스나 사용자 프로필은 매핑하지 않고 테스트 입력은 읽기 전용, 결과 디렉터리만 쓰기 가능하게 매핑한다. 네트워크·클립보드·프린터·마이크·카메라 공유를 끈다. 테스트 스크립트는 Sandbox 계정과 고정 매핑 경로를 확인하고, 일반 호스트에서는 설치 전 거부한다. 설정 형식은 [Microsoft의 Windows Sandbox 문서](https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/windows-sandbox-configure-using-wsb-file)를 따른다.

Sandbox 자동 검증은 최초 설치, 개발 도구 없는 EXE 버전 조회, 바로가기 대상, 포트 충돌, 중복 실행, 실행 중 동일 버전 재설치, 실행 중 제거와 설정/RoboRev sentinel 보존을 다룬다. `result.json`은 통과한 항목과 여전히 필요한 수동 항목을 나누어 기록한다.

격리된 환경에서 추가로 다음을 검증한다.

1. 기본 Setup 창에서 **취소**한 뒤 설치·설정 파일과 실행 중 PID가 그대로인지 확인한다.
2. 승인하여 설치하고 시작 메뉴로 실행했을 때 브라우저가 열리는지, 종료 바로가기가 Dashboard만 종료하는지 확인한다.
3. 이전 기준 버전으로 만든 설치기에서 새 기준 버전으로 업그레이드한다. 새 EXE 해시, 동일 uninstall 항목, 설정 보존을 확인한다.
4. 다시 구버전 설치기를 실행해 다운그레이드가 거부되는지 확인한다.
5. unsigned Setup/EXE에 대한 실제 SmartScreen/Application Control 동작을 기록한다. 검사나 보안 설정을 우회하지 않는다.

이번 작업 환경은 Windows 11 amd64지만 Windows Sandbox 실행 파일이 없다. 따라서 이 환경의 실제 빌드와 비파괴 회귀 검증 결과를 설치·제거 E2E 성공으로 표시하지 않는다. 마지막 실행 결과는 `docs/HANDOFF.md`의 이번 checkpoint에 기록한다.
