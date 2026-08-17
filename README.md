# RoboRev Dashboard

RoboRev Dashboard는 기존 [`roborev`](https://github.com/kenn-io/roborev) CLI/daemon을 보기 좋고 안전하게 조작하는 독립 localhost Web UI다. RoboRev를 fork하거나 저장소를 대체하지 않으며 review 데이터의 source of truth는 항상 RoboRev다.

현재 목표는 Windows 11 Tier 1의 단일 실행 파일과 핵심 review workflow다. 설치된 RoboRev를 런타임에 감지하고 repository/HEAD 상태, repository별 panel 선택, 결과와 member 상태, comment/close/reopen/cancel, prompt/log lazy-load와 finding용 Fix Prompt를 제공한다.

## 개발 요구사항

- Go 1.24 이상
- Node.js 22 이상과 npm
- dogfood smoke test에만 RoboRev v0.64.0 이상

자동 테스트는 실제 RoboRev나 LLM을 호출하지 않는다.

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

Finding review의 detail에서는 `Copy Fix Prompt` 또는 `Open Fix Prompt`로 현재 코드에서 finding을 검증·수정하도록 안내하는 편집 가능한 prompt를 만들 수 있다. 이 작업은 로컬 deterministic template이며 LLM, review rerun, fix 또는 file mutation을 실행하지 않는다.

## Panel Refine

`Panel Refine`은 현재 repository의 clean HEAD를 `dashboard_cross` 같은 선택 panel로 새로 review하고, synthesis가 실패하면 Codex fix를 실행해 새 commit을 다시 review하는 자동 반복 workflow다. 대화상자에서 iteration panel, fix agent(Codex), 최대 1~10회(기본 3회), 선택적인 final deep panel(`dashboard_deep`)을 지정한다. 시작 전 preview에는 실제 `POST /api/panel-refines` body와 `roborev fix --agent codex --quiet <synthesisJobID>` argv template이 표시되며, executable은 서버 설정에서만 선택된다. final panel을 지정하면 iteration panel이 통과한 뒤에만 실행되며, final finding도 같은 fix budget으로 수정한 후 iteration panel부터 다시 검증한다.

진행 상태는 Dashboard backend가 소유하므로 browser reload/reopen 뒤에도 process가 살아 있는 동안 다시 표시된다. Dashboard process를 종료하면 실행 중인 Panel Refine은 취소되며 재시작 후 자동 resume하지 않는다. 취소 결과에는 fresh inspection의 HEAD, branch, dirty 여부와 inspection 실패가 함께 표시된다. 시작과 각 iteration 전후에 clean working tree, branch, HEAD, 등록 repository identity를 다시 확인하므로 외부 변경이나 dirty 결과는 자동 fix 대신 주의 상태로 멈춘다. 같은 repository에서 Review HEAD, Rerun 또는 다른 Panel Refine과 동시에 실행할 수 없다.

RoboRev v0.64.0의 `fix --agent codex --quiet <synthesis-id>`가 fix commit 뒤 panel 없는 default review를 자동 enqueue할 수 있어, Dashboard가 선택한 panel review와 별도의 unrelated extra review가 보일 수 있다. Dashboard는 이를 추측으로 숨기지 않는다.

## 안전 원칙

Dashboard는 shell console을 제공하지 않는다. 모든 작업은 typed request를 허용 목록의 argument array로 변환한다. mutation은 등록된 Git repository와 same-origin/CSRF 검사를 통과해야 한다. RoboRev DB와 agent credential은 읽거나 저장하지 않는다.

설계는 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), 명령 coverage는 [docs/ROBorev-COVERAGE.md](docs/ROBorev-COVERAGE.md), 현재 작업 상태는 [docs/HANDOFF.md](docs/HANDOFF.md)를 참고한다.
