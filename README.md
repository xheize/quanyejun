# QuanYejun

LLM을 정체성 자체가 아닌 **교체 가능한 추론 엔진**으로 사용하는 지속 Identity 런타임의 첫 번째 MVP입니다.

현재 구현은 하나의 Identity가 대화하고, 중요한 내용을 기억하고, WebSocket 연결과 독립된 비동기 작업을 실행하고, 모든 변화를 재생 가능한 이벤트로 남기는 가장 작은 세로 조각입니다.

## 포함된 기능

- 대화·작업·활동 스트림을 한 화면에서 보여주는 반응형 웹 UI
- Go + Gin HTTP API와 WebSocket 게이트웨이
- `lastEventId` 커서를 이용한 연결 복구 및 이벤트 재생
- PostgreSQL 기반 대화, 기억, 작업, 이벤트 영속화
- 최근 대화 10개와 관련 기억 5개만 사용하는 짧은 LLM 문맥
- 로컬 LLM, Claude API/Code, OpenAI API, Codex CLI를 자동 선택하는 LLM 인터페이스
- 연결이 끊겨도 계속 실행되는 데모 비동기 작업
- PostgreSQL이 없을 때 빠른 미리보기를 위한 비영속 메모리 저장소

## 구조

```text
app/                웹 UI
runtime/            Go 런타임
  cmd/server/       실행 진입점
  internal/         domain, store, llm, event hub, tasks, HTTP/WebSocket
migrations/         PostgreSQL 초기 스키마
docs/architecture/  설계와 MVP 경계
```

## 빠르게 실행하기

필요한 도구는 Node.js 22+, pnpm, Go 1.23+, PostgreSQL입니다.

1. PostgreSQL을 시작합니다.

   ```powershell
   docker compose up -d postgres
   ```

2. 새 PowerShell에서 런타임을 시작합니다.

   ```powershell
   Set-Location runtime
   $env:DATABASE_URL = "postgres://quanyejun:quanyejun@localhost:5432/quanyejun?sslmode=disable"
   go run ./cmd/server
   ```

3. 다른 PowerShell에서 웹을 시작합니다.

   ```powershell
   pnpm install
   pnpm dev
   ```

웹은 `http://localhost:3000`, 런타임은 `http://localhost:8080`에서 실행됩니다. PostgreSQL 없이 런타임만 실행하면 메모리 저장소를 사용하므로 동작은 확인할 수 있지만 재시작 후 데이터는 사라집니다.

## 추론 엔진 자동 감지

기본값 `LLM_PROVIDER=auto`는 다음 순서로 사용 가능한 엔진을 찾습니다.

1. 실행 중인 Ollama 또는 LM Studio의 로컬 모델
2. `ANTHROPIC_API_KEY` + `ANTHROPIC_MODEL`로 설정된 Claude API
3. 설치·로그인된 Claude Code CLI
4. `OPENAI_API_KEY` + `OPENAI_MODEL`로 설정된 OpenAI Responses API
5. 설치·로그인된 Codex CLI
6. 네트워크 없이 동작하는 내장 미리보기 응답기

`LLM_PROVIDER_PRIORITY`로 순서를 바꾸거나 `LLM_PROVIDER`로 하나를 강제할 수 있습니다. 강제한 provider가 준비되지 않았다면 런타임은 조용히 다른 엔진으로 넘어가지 않고 시작 오류를 냅니다.

### 로컬 LLM

Ollama가 실행 중이고 모델이 하나 이상 설치되어 있으면 첫 모델을 자동으로 선택합니다. 모델을 고정하거나 다른 OpenAI 호환 서버를 쓸 때는 다음처럼 설정합니다.

```powershell
$env:LLM_PROVIDER = "local"
$env:LOCAL_LLM_BASE_URL = "http://localhost:11434/v1"
$env:LOCAL_LLM_MODEL = "qwen3:8b"
go run ./cmd/server
```

### Claude API / Claude Code

```powershell
# Claude API
$env:LLM_PROVIDER = "claude-api"
$env:ANTHROPIC_API_KEY = "..."
$env:ANTHROPIC_MODEL = "your-claude-model"
go run ./cmd/server
```

또는 설치·로그인된 Claude Code를 사용합니다.

```powershell
$env:LLM_PROVIDER = "claude-code"
go run ./cmd/server
```

### OpenAI API / Codex CLI

```powershell
# OpenAI Responses API
$env:LLM_PROVIDER = "openai-api"
$env:OPENAI_API_KEY = "..."
$env:OPENAI_MODEL = "your-openai-model"
$env:OPENAI_BASE_URL = "https://api.openai.com/v1"
go run ./cmd/server
```

또는 설치·로그인된 Codex CLI를 사용합니다.

```powershell
$env:LLM_PROVIDER = "codex-cli"
go run ./cmd/server
```

CLI provider는 매 요청을 임시 작업 폴더에서 실행합니다. Claude Code는 도구와 세션 저장을 끄고, Codex는 `--ephemeral`과 읽기 전용 sandbox를 사용하므로 Identity 응답 생성 중 프로젝트 파일을 수정하지 않습니다. 키는 저장소에 커밋하지 마세요. 전체 설정 항목은 `.env.example`에 정리되어 있습니다.

## 확인

```powershell
pnpm build
Set-Location runtime
go test ./...
```

## 주요 API

| 경로 | 역할 |
| --- | --- |
| `GET /healthz` | 런타임과 현재 LLM 제공자 확인 |
| `GET /api/bootstrap` | Identity, 최근 대화, 기억, 작업, 이벤트 로드 |
| `POST /api/messages` | 대화 입력 및 Parent LLM 호출 |
| `POST /api/tasks/demo` | 연결 독립적인 비동기 작업 시작 |
| `GET /api/events?after=` | 커서 이후 이벤트 조회 |
| `GET /ws?lastEventId=` | 이벤트 재생 후 실시간 스트림 구독 |

## MVP 경계

이 버전은 분산 작업 큐, 복잡한 관계 점수, 다중 Parent, 조직 RBAC, 외부 서비스 연동을 의도적으로 포함하지 않습니다. 먼저 “하나의 Identity가 대화하고, 기억하고, 작업하고, 나중에 다시 이어지는가?”를 검증합니다.
