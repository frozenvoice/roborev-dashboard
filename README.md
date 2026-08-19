# RoboRev Dashboard

RoboRev Dashboard는 기존 [`roborev`](https://github.com/kenn-io/roborev) CLI/daemon을 보기 좋고 안전하게 조작하는 독립 localhost Web UI다. RoboRev를 fork하거나 저장소를 대체하지 않으며 review 데이터의 source of truth는 항상 RoboRev다.

현재 목표는 Windows 11 Tier 1의 단일 실행 파일과 핵심 review workflow다. 설치된 RoboRev를 런타임에 감지하고, 없으면 Dashboard와 완전히 호환되는 pinned RoboRev를 명시적 확인 뒤 설치할 수 있다. repository/HEAD 상태, repository별 panel 선택, 결과와 member 상태, comment/close/reopen/cancel, prompt/log lazy-load와 finding용 Fix Prompt를 제공한다.

## 새 clone에서 개발 시작

소스에서 Dashboard를 빌드하려면 Windows 개발 도구가 필요하다.

- Git
- Node.js 22 이상과 npm
- Go 1.24 이상

새 checkout에서는 다음 순서로 검사·설치 계획을 확인하고 개발 의존성을 준비한다.

```powershell
git clone <repository-url>
Set-Location roborev-dashboard
.\scripts\install.ps1
.\scripts\test.ps1
.\scripts\build.ps1
.\dist\roborev-dashboard.exe
```

`scripts/install.ps1`은 소스 개발용 toolchain bootstrap이다. 정의된 Git/Node.js LTS/Go 패키지만 Windows Package Manager로 설치하거나 업그레이드하고, 현재 PowerShell 프로세스의 PATH만 새로 합친다. 이후 `web` 의존성을 `npm ci`로 설치한다. 이 과정은 RoboRev를 검사하거나 설치하거나 실행하지 않는다. winget이 필요 없는 상태라면 winget을 요구하지 않는다.

자동 테스트는 실제 RoboRev나 LLM을 호출하지 않는다.

## 언어 전환

Dashboard UI는 한국어와 English를 지원한다. 사이드바 하단의 native language selector에서 언어를 바꿀 수 있으며, 선택은 `roborev-dashboard-language`에 안전하게 저장되고 다음 실행에서 복원된다. 저장소, panel, review, modal, setup 진행 상태와 입력 내용은 언어 전환으로 재설정되지 않고 네트워크 요청이나 mutation도 발생하지 않는다. 열린 Install, daemon, agent check, Fix Prompt, Panel Refine 확인 화면에서도 헤더의 compact selector로 즉시 전환할 수 있다.

번역 대상은 Dashboard chrome과 안내 문구이며 RoboRev, Git/SHA/API/CLI/CSRF/URL/JSON, 경로·버전·이름·ID와 backend 원문 오류 같은 technical/source-of-truth 값은 그대로 표시한다. 한국어가 긴 상태나 모바일 폭에서 줄바꿈되도록 최소한의 responsive CSS만 적용한다.

## 릴리스 실행 요구사항

배포된 `roborev-dashboard.exe`에는 frontend assets가 embed되어 있으므로 Go, Node.js, npm 같은 개발 도구가 필요 없다. Git은 runtime에 필요하다. RoboRev가 없으면 Dashboard의 **Agents & Setup**에서 지원되는 pinned release와 설치 위치, 정확한 SHA-256을 확인한 뒤 **Install RoboRev**를 선택할 수 있다. 사용자가 확인 모달에서 **Install & Start**를 승인하기 전에는 다운로드나 설치를 시작하지 않는다.

Dashboard runtime installer는 현재 Windows amd64에서 RoboRev v0.64.0만 설치한다. 공식 GitHub release ZIP의 고정 크기와 Dashboard에 포함된 SHA-256을 검증하고, ZIP root의 `roborev.exe` 하나만 `%USERPROFILE%\.roborev\bin\roborev.exe`에 기존 파일을 덮어쓰지 않고 설치한다. 이후 `version --json`을 다시 검증해 현재 Dashboard process에 absolute path로 연결하고 daemon health를 확인한다. PATH, Windows 보안 정책, RoboRev init, Git hook, AI agent 및 credential은 변경하지 않는다. 이미 다른 경로의 supported binary가 있으면 그대로 사용하고, newer/unknown/blocked binary는 자동 downgrade 또는 교체하지 않는다.

## 실행

```powershell
.\scripts\dev.ps1
```

프로덕션 빌드와 전체 테스트:

```powershell
.\scripts\test.ps1
.\scripts\build.ps1
.\dist\roborev-dashboard.exe
```

기본 주소는 `127.0.0.1`이며 사용 가능한 포트를 7410부터 찾는다. browser 자동 열기를 막으려면 `--no-open`을 사용한다.

```powershell
.\dist\roborev-dashboard.exe --no-open --repo 'E:\Dev\GitHub\roborev-dashboard'
```

Dashboard는 선택한 repository의 global/local RoboRev panel을 발견한다. panel이 하나면 자동 선택하고 여러 개면 사용자가 선택할 때까지 `Review HEAD`를 실행하지 않는다. `Single agent · no panel`은 RoboRev v0.64.0의 명시적 `none` bypass다.

현재 clean-machine checkpoint는 RoboRev가 설치되지 않은 상태를 보존한다. 다음 수동 E2E에서 Setup 화면의 pinned v0.64.0 확인 범위와 Install & Start를 검토한 뒤에만 RoboRev를 설치하고, daemon online·panel 복원·Review HEAD·Panel Refine을 확인한다.

**Agents & Setup**은 effective `[review.subagents]` alias와 underlying RoboRev agent를 구분해 표시하고, exact v0.64.0 command descriptor와 safe executable lookup으로 quota를 사용하지 않는 `Detected` 상태를 제공한다. Cursor의 `agent` command는 격리된 임시 working directory와 credential/preload 변수가 제거된 환경에서 bounded version-only identity probe를 실행해 Grok alias와 혼동하지 않도록 fail closed한다. `Health`는 자동 실행되지 않으며, 사용자가 특정 canonical agent의 **Run check**를 확인한 경우에만 작은 실제 RoboRev smoke request를 보내므로 subscription/API quota를 사용할 수 있다. 다른 RoboRev 버전에서는 discovery와 check를 실행하지 않는다.

같은 화면의 **RoboRev Runtime**에서는 daemon Start/Stop/Restart와 queue Pause/Resume을 제공한다. Pause는 실행 중인 job을 중지하지 않고 새 queued job의 시작만 막는다. Queue control은 이미 실행 중인 daemon의 typed HTTP API만 사용하며 daemon을 암묵적으로 시작하지 않는다. Stop/Restart 확인 화면은 backend가 생성한 RoboRev executable과 exact argument array를 표시한다. Stop/Restart는 명시적 확인 뒤 process-scoped operation으로 진행되고 Dashboard shutdown에서 취소되며, exact v0.64.0 이외의 버전에서는 모든 runtime mutation을 차단한다.

Finding review의 detail에서는 `Copy Fix Prompt` 또는 `Open Fix Prompt`로 현재 코드에서 finding을 검증·수정하도록 안내하는 편집 가능한 prompt를 만들 수 있다. 이 작업은 로컬 deterministic template이며 LLM, review rerun, fix 또는 file mutation을 실행하지 않는다.

## Panel Refine

`Panel Refine`은 현재 repository의 clean HEAD를 `dashboard_cross` 같은 선택 panel로 새로 review하고, synthesis가 실패하면 Codex fix를 실행해 새 commit을 다시 review하는 자동 반복 workflow다. 대화상자에서 iteration panel, fix agent(Codex), 최대 1~10회(기본 3회), 선택적인 final deep panel(`dashboard_deep`)을 지정한다. 시작 전 preview에는 실제 `POST /api/panel-refines` body와 `roborev fix --agent codex --quiet <synthesisJobID>` argv template이 표시되며, executable은 서버 설정에서만 선택된다. final panel을 지정하면 iteration panel이 통과한 뒤에만 실행되며, final finding도 같은 fix budget으로 수정한 후 iteration panel부터 다시 검증한다.

진행 상태는 Dashboard backend가 소유하므로 browser reload/reopen 뒤에도 process가 살아 있는 동안 다시 표시된다. Dashboard process를 종료하면 실행 중인 Panel Refine은 취소되며 재시작 후 자동 resume하지 않는다. 취소 결과에는 fresh inspection의 HEAD, branch, dirty 여부와 inspection 실패가 함께 표시된다. 시작과 각 iteration 전후에 clean working tree, branch, HEAD, 등록 repository identity를 다시 확인하므로 외부 변경이나 dirty 결과는 자동 fix 대신 주의 상태로 멈춘다. 같은 repository에서 Review HEAD, Rerun 또는 다른 Panel Refine과 동시에 실행할 수 없다.

RoboRev v0.64.0의 `fix --agent codex --quiet <synthesis-id>`가 fix commit 뒤 panel 없는 default review를 자동 enqueue할 수 있어, Dashboard가 선택한 panel review와 별도의 unrelated extra review가 보일 수 있다. Dashboard는 이를 추측으로 숨기지 않는다.

## 안전 원칙

Dashboard는 shell console을 제공하지 않는다. 모든 작업은 typed request를 허용 목록의 argument array로 변환한다. mutation은 등록된 Git repository와 same-origin/CSRF 검사를 통과해야 한다. RoboRev DB와 agent credential은 읽거나 저장하지 않는다.

설계는 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), 명령 coverage는 [docs/ROBorev-COVERAGE.md](docs/ROBorev-COVERAGE.md), 현재 작업 상태는 [docs/HANDOFF.md](docs/HANDOFF.md)를 참고한다.
