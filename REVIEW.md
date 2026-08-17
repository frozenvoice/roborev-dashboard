# RoboRev Dashboard 리뷰 지침

추측성 지적보다 코드 위치, 입력, 실행 경로와 재현 방법이 명확한 actionable finding을 우선한다. 변경된 코드와 직접 관련 없는 기존 문제는 별도로 구분한다.

특히 다음을 검토한다.

- Windows process spawning, executable/argument 분리 및 quoting 오류
- shell command injection과 사용자 입력의 argument 경계 이탈
- path traversal, symlink/junction 우회 및 잘못된 repository target
- localhost privileged mutation의 CSRF, Origin 및 Host 검증 누락
- 감지된 RoboRev 버전과 capability/CLI/API 계약의 불일치
- 사람이 보는 CLI 출력에 의존하는 fragile parsing
- fix, refine, install, update, agentic run이 사용자 확인 없이 실행되는 경로
- review, fix, refine가 선택한 repository/commit/range가 아닌 대상을 실행하는 문제
- 현재 `HEAD` 새 리뷰와 기존 frozen target rerun의 혼동
- goroutine/process leak, child process cancellation 및 Windows process-tree cleanup
- stdout/stderr 무제한 buffering과 큰 prompt/log/review의 eager polling
- browser에서 unsafe Markdown/raw HTML/script 실행
- token, API key, 환경 변수 또는 agent log의 비밀정보 유출
- concurrent mutation, repo switch, polling/stream 간 race condition
- UI 상태와 실제 RoboRev daemon/job 상태 불일치
- Windows path, UTF-8, 한글 및 공백 포함 경로 처리
- 실패/부분 실패/cancellation/구버전 capability에 대한 테스트 누락

finding에는 가능한 경우 severity, 영향, 정확한 코드 위치, 재현 입력 및 최소 수정 방향을 포함한다. 단순 스타일 선호나 근거 없는 미래 가능성은 finding으로 보고하지 않는다.

