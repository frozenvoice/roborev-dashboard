# RoboRev Dashboard

RoboRev Dashboard는 기존 [`roborev`](https://github.com/kenn-io/roborev) CLI/daemon을 보기 좋고 안전하게 조작하는 독립 localhost Web UI다. RoboRev를 fork하거나 저장소를 대체하지 않으며 review 데이터의 source of truth는 항상 RoboRev다.

현재 목표는 Windows 11 Tier 1의 단일 실행 파일과 핵심 review workflow다. 설치된 RoboRev를 런타임에 감지하고 repository/HEAD 상태, panel review, 결과와 member 상태, comment/close/reopen/cancel, prompt/log lazy-load를 제공한다.

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

## 안전 원칙

Dashboard는 shell console을 제공하지 않는다. 모든 작업은 typed request를 허용 목록의 argument array로 변환한다. mutation은 등록된 Git repository와 same-origin/CSRF 검사를 통과해야 한다. RoboRev DB와 agent credential은 읽거나 저장하지 않는다.

설계는 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), 명령 coverage는 [docs/ROBorev-COVERAGE.md](docs/ROBorev-COVERAGE.md), 현재 작업 상태는 [docs/HANDOFF.md](docs/HANDOFF.md)를 참고한다.

