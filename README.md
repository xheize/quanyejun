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
- 영속 디렉터리·도구 탐색·권한을 제공하는 교체 가능한 실행 환경 인터페이스
- 연결이 끊겨도 계속 실행되는 데모 비동기 작업
- PostgreSQL이 없을 때 빠른 미리보기를 위한 비영속 메모리 저장소

## 구조

```text
app/                웹 UI
runtime/            Go 런타임
  cmd/server/       실행 진입점
  internal/         domain, environment, store, llm, event hub, tasks, HTTP/WebSocket
deploy/lxc/         LXC 게스트용 systemd 배포 어댑터
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

## Identity 표기명 변경

프로젝트 이름과 내부 ID `identity_quanyejun`은 고정하고, 화면과 대화에서 자신을 부르는 이름만 바꿀 수 있습니다. 런타임을 시작하기 전에 다음 값을 설정합니다.

```powershell
$env:IDENTITY_DISPLAY_NAME = "루멘"
go run ./cmd/server
```

표기명을 바꿔도 같은 내부 Identity를 사용하므로 기존 대화와 기억은 그대로 이어집니다. 빈 값은 기본 이름 `QuanYejun`으로 처리하고, 앞뒤·연속 공백을 정리하며 최대 40자로 제한합니다.

## 실행 환경

Identity 코어는 LXC, Docker, Kubernetes API를 직접 호출하지 않습니다. 대신 `Environment` 인터페이스를 통해 영속 `home`, `workspace`, `config`, `cache`, 셸 실행, 도구 탐색을 사용합니다. 현재 구현은 같은 OS에서 실행하는 `local` 어댑터이며, LXC 안에서 실행할 때도 동일한 코드를 쓰고 환경 변수로 `kind=lxc`와 영속 경로를 지정합니다.

도구의 설치 여부인 capability와 실제 사용 허가인 authority는 분리되어 있습니다. `QUANYEJUN_ENVIRONMENT_ALLOW`에 명시된 capability만 실행 경계가 허용합니다. 현재 API는 임의 셸 명령을 외부에 노출하지 않으며, CLI 기반 추론 엔진만 격리된 임시 작업 디렉터리에서 이 경계를 사용합니다.

현재 환경은 `GET /api/environment`에서 확인할 수 있습니다. 기본 로컬 경로는 `.runtime/` 아래에 생성되며 저장소에는 커밋되지 않습니다.

### LXC 게스트에 설치

LXC는 오케스트레이션 대상이 아니라 첫 서버 실행 어댑터로 취급합니다. Linux용 바이너리를 빌드한 뒤 LXC 게스트 안에서 설치 스크립트를 실행합니다.

```bash
cd runtime
GOOS=linux GOARCH=amd64 go build -o ../dist/quanyejun-runtime ./cmd/server
cd ..
sudo ./deploy/lxc/install.sh ./dist/quanyejun-runtime
```

설치기는 전용 사용자, 영속 디렉터리, `/etc/quanyejun/config.env`, systemd 서비스를 준비합니다. 실제 DB URL과 LLM 키는 게스트의 설정 파일에만 추가하세요. Docker와 Kubernetes는 코어 변경 없이 이후 별도 배포 어댑터로 추가할 수 있습니다.

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
| `GET /api/environment` | 실행 환경, capability, authority, 도구 상태 확인 |
| `POST /api/messages` | 대화 입력 및 Parent LLM 호출 |
| `POST /api/tasks/demo` | 연결 독립적인 비동기 작업 시작 |
| `GET /api/events?after=` | 커서 이후 이벤트 조회 |
| `GET /ws?lastEventId=` | 이벤트 재생 후 실시간 스트림 구독 |

## MVP 경계

이 버전은 분산 작업 큐, 복잡한 관계 점수, 다중 Parent, 조직 RBAC, 외부 서비스 연동을 의도적으로 포함하지 않습니다. 먼저 “하나의 Identity가 대화하고, 기억하고, 작업하고, 나중에 다시 이어지는가?”를 검증합니다.
