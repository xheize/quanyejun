# QuanYejun

LLM을 정체성 자체가 아닌 **교체 가능한 추론 엔진**으로 사용하는 지속 Identity 런타임의 첫 번째 MVP입니다.

현재 구현은 하나의 Identity가 대화하고, 중요한 내용을 기억하고, WebSocket 연결과 독립된 비동기 작업을 실행하고, 모든 변화를 재생 가능한 이벤트로 남기는 가장 작은 세로 조각입니다.

## 포함된 기능

- 대화·작업·활동 스트림을 한 화면에서 보여주는 반응형 웹 UI
- Go + Gin HTTP API와 WebSocket 게이트웨이
- `lastEventId` 커서를 이용한 연결 복구 및 이벤트 재생
- PostgreSQL 기반 대화, 기억, 작업, 이벤트 영속화
- 최근 대화 10개와 관련 기억 5개만 사용하는 짧은 LLM 문맥
- OpenAI Responses API, Anthropic Messages API, 로컬 OpenAI 호환 HTTP API를 지원하는 교체 가능한 LLM 인터페이스
- 모델 Tool Call을 권한이 제한된 런타임 도구로 실행하고 모든 단계를 저장하는 Agent Run
- 모델 턴·도구 호출·실행 시간을 제한하고 공개 이벤트에서 Tool 입출력을 제거하는 Agent Harness
- 메타데이터 탐색 후 필요한 지시문만 로드하는 로컬 `SKILL.md` 레지스트리
- 관리자 MCP 서버 목록과 부모/관리자 authority의 교집합만 노출하는 원격 MCP 클라이언트
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
skills/             Provider 독립적인 로컬 SKILL.md 예제
```

## 빠르게 실행하기

필요한 도구는 Node.js 22+, pnpm, Go 1.25+, PostgreSQL입니다.

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
   $env:NEXT_PUBLIC_RUNTIME_URL = "http://localhost:8080"
   pnpm dev
   ```

웹은 `http://localhost:3000`, 런타임은 `http://localhost:8080`에서 실행됩니다. PostgreSQL 없이 런타임만 실행하면 메모리 저장소를 사용하므로 동작은 확인할 수 있지만 재시작 후 데이터는 사라집니다.

### 프론트 단독 미리보기

`NEXT_PUBLIC_RUNTIME_URL`이 비어 있으면 API·WebSocket 요청 없이 미리보기를 표시합니다. 대화 입력·전송, 작업 실행, 별칭 저장은 연결 전까지 비활성화됩니다.

백엔드를 연결하려면 **빌드 전에** `NEXT_PUBLIC_RUNTIME_URL`을 브라우저에서 접근 가능한 API 주소로 설정하세요(예: `https://runtime.example.com`). 주소 변경 후에는 프론트를 다시 빌드·배포해야 합니다. HTTPS 웹에는 HTTPS API를 사용하고 백엔드 `ALLOWED_ORIGINS`에도 웹 주소를 등록하세요.

최초 연결 이후 HTTP·WebSocket 재시도를 페이지당 합계 최대 10회 수행합니다(간격 2.5초). 연결에 성공해도 예산은 초기화되지 않습니다. 한도에 도달하면 자동 재시도를 중단하고 새로고침 안내를 표시합니다.

### Docker Compose로 전체 실행

루트의 `Dockerfile.frontend`는 웹 UI(Node.js, 포트 3000), `Dockerfile.backend`는 Go 런타임(API, 포트 8080)을 각각 빌드합니다. 두 파일 모두 저장소 루트를 빌드 컨텍스트로 사용하며, `compose.yaml`은 각 Dockerfile로 두 서비스와 PostgreSQL을 함께 실행합니다.

```powershell
$env:NEXT_PUBLIC_RUNTIME_URL = "http://localhost:8080"
docker compose up --build
```

이미지를 개별 빌드하려면 저장소 루트에서 실행합니다.

```powershell
docker build -f Dockerfile.backend -t quanyejun-backend .
docker build -f Dockerfile.frontend -t quanyejun-frontend --build-arg NEXT_PUBLIC_RUNTIME_URL=http://localhost:8080 .
```

기본 접속 주소는 웹 `http://localhost:3000`, API `http://localhost:8080`입니다. 브라우저가 API에 접근할 공개 주소가 다르면 이미지를 빌드하기 전에 `NEXT_PUBLIC_RUNTIME_URL`을 설정하세요. 모델 Provider와 API 키도 `.env` 또는 현재 셸 환경 변수로 전달할 수 있으며 기본값은 네트워크 키가 필요 없는 `builtin`입니다.

```powershell
$env:NEXT_PUBLIC_RUNTIME_URL = "https://runtime.example.com"
$env:LLM_PROVIDER = "openai"
$env:OPENAI_API_KEY = "..."
$env:OPENAI_MODEL = "..."
docker compose up --build
```

PostgreSQL, Identity workspace, cache, 관리자 설정은 각각 Docker volume에 유지됩니다. `quanyejun-config` volume은 최초 생성 시 비활성화된 MCP 예제로 초기화되며, 실제 연결을 사용할 때 `/etc/quanyejun/mcp-servers.json`과 `/etc/quanyejun/mcp-authority.json`을 관리자가 수정하면 됩니다. `.dockerignore`는 로컬 의존성, 빌드 결과, 런타임 데이터, 비밀 설정을 빌드 컨텍스트에서 제외합니다.

## SSO 로그인과 익명 방문자

로그인하지 않아도 서버가 발급한 쿠키로 같은 브라우저의 대화를 이어갑니다. 익명 기록은 **각 생성 시점부터 최대 72시간**만 조회할 수 있으며, 접속을 계속해도 만료가 연장되지 않습니다. 파생 답변·기억·실행 결과는 원본의 가장 이른 만료를 이어받고, 만료 행은 서버 시작 및 30초 간격 정리 작업으로 삭제합니다.

기존 SSO를 연결하려면 `.env`에 discovery가 광고하는 정확한 `SSO_ISSUER_URL`, 등록한 `SSO_CLIENT_ID`와 필요한 경우 `SSO_CLIENT_SECRET`을 설정합니다. 2026-09-13 확인 시 `sso.example.com`는 `sso-dev.example.com`를 issuer로 광고했으므로 먼저 실제 사용할 주소를 확정해야 합니다. SSO 관리자에 `SSO_REDIRECT_URL`의 정확한 URL(기본 `http://localhost:8080/auth/callback`), `authorization_code`, `code`, `openid`, PKCE S256을 등록해야 합니다. 운영에서는 웹·API의 HTTPS 주소에 맞게 `WEB_PUBLIC_URL`, `SSO_REDIRECT_URL`, `ALLOWED_ORIGINS`를 지정합니다.

로그인 사용자는 SSO subject로 지속 식별합니다. 익명 기록은 로그인 기록으로 자동 이전하지 않습니다. 로그인만으로 소유자의 CLI·파일·MCP 권한을 부여하지 않습니다. 기존 `person_example` 기록의 SSO 소유자 매핑도 별도 작업입니다. Go 직접 실행은 `.env`를 자동으로 읽지 않으므로 셸에 값을 설정해야 합니다.

쿠키 보호 키는 기본적으로 영속 HOME의 `session.key`에 생성됩니다. 여러 backend replica는 동일한 `SESSION_ENCRYPTION_KEY`(32바이트 난수의 base64)를 사용해야 합니다. 상세 계약은 [상대 식별과 익명 보존 설계](docs/architecture/identity-and-retention.md)를 참고하세요.

## Identity 표기명 변경

프로젝트 이름과 내부 ID `identity_quanyejun`은 고정하고, 화면과 대화에서 자신을 부르는 이름만 바꿀 수 있습니다. 런타임을 시작하기 전에 다음 값을 설정합니다.

```powershell
$env:IDENTITY_DISPLAY_NAME = "루멘"
go run ./cmd/server
```

표기명을 바꿔도 같은 내부 Identity를 사용하므로 기존 대화와 기억은 그대로 이어집니다. 빈 값은 기본 이름 `QuanYejun`으로 처리하고, 앞뒤·연속 공백을 정리하며 최대 40자로 제한합니다.

## 실행 환경

Identity 코어는 LXC, Docker, Kubernetes API를 직접 호출하지 않습니다. 대신 `Environment` 인터페이스를 통해 영속 `home`, `workspace`, `config`, `cache`, 셸 실행, 도구 탐색을 사용합니다. 현재 구현은 같은 OS에서 실행하는 `local` 어댑터이며, LXC 안에서 실행할 때도 동일한 코드를 쓰고 환경 변수로 `kind=lxc`와 영속 경로를 지정합니다.

도구의 설치 여부인 capability와 실제 사용 허가인 authority는 분리되어 있습니다. `QUANYEJUN_ENVIRONMENT_ALLOW`에 `shell`이 포함된 환경에서는 모델에 `environment_inspect`와 `environment_exec`가 노출됩니다. 모델은 설치된 CLI를 탐색하고, 게스트 OS 권한이 허용하면 `apt`, `npm`, `pip` 같은 패키지 관리자를 사용하며, 명령을 워크스페이스 또는 격리된 임시 디렉터리에서 실행할 수 있습니다. 명령은 executable과 args를 분리해 전달하고, 셸 문법이 필요할 때만 `sh -lc`나 `pwsh -Command`를 명시적으로 실행합니다.

`environment_exec`는 작업 디렉터리를 워크스페이스 안으로 제한하고 stdin, 인자 수, 실행 시간, stdout/stderr 크기에 상한을 둡니다. 명령 입력과 결과는 다른 Tool Call과 같이 실행 단계에 저장되며, 실시간 이벤트에는 명령 이름·종료 코드·실행 시간만 게시합니다. `shell` authority가 없는 컨테이너 프로필에서는 이 Tool 자체가 모델에 노출되지 않습니다.

현재 환경은 `GET /api/environment`에서 확인할 수 있습니다. 기본 로컬 경로는 `.runtime/` 아래에 생성되며 저장소에는 커밋되지 않습니다.

## Agent Harness와 Skills

Agent Harness는 Provider가 반환한 Tool Call만 실행하며 다음 세 가지 실행 예산을 강제합니다.

| 환경 변수 | 기본값 | 허용 범위 |
| --- | ---: | ---: |
| `QUANYEJUN_AGENT_MAX_TURNS` | 8 | 1–32 |
| `QUANYEJUN_AGENT_MAX_TOOL_CALLS` | 24 | 1–128 |
| `QUANYEJUN_AGENT_RUN_TIMEOUT_SECONDS` | 300 | 10–3600 |

Skill은 `<skills-root>/<name>/SKILL.md`에 저장합니다. 기본 루트는 `<config>/skills`이며 `QUANYEJUN_SKILLS_DIR`로 바꿀 수 있습니다. 런타임은 처음부터 모든 지시문을 프롬프트에 넣지 않습니다. 모델은 `skill_list`로 이름·설명·콘텐츠 해시만 확인한 뒤, 실제로 필요한 하나를 `skill_load`로 읽습니다. 예제는 `skills/workspace-summary/SKILL.md`에 있습니다.

Skill 지시문은 하네스 정책보다 낮은 신뢰도로 취급합니다. 따라서 Skill이 Tool, filesystem capability, authority를 새로 부여하거나 실행 예산을 변경할 수 없습니다. 다만 이미 `shell` authority가 있는 컴퓨팅 환경에서는 Skill이 CLI 탐색·설치·사용법을 안내하고, 적합한 CLI가 없는 서비스에는 MCP 사용을 안내할 수 있습니다. Tool 입력·출력은 실행 단계 DB에는 남지만 실시간 `run.step` 이벤트에는 실리지 않아 파일 내용과 Skill 본문이 WebSocket 구독자에게 재전송되지 않습니다.

현재 단계에서는 Skill 선택에 RAG를 사용하지 않습니다. Skill 수가 적을 때는 메타데이터 목록과 명시적 로드가 더 예측 가능합니다. 향후 Skill·문서가 많아져 전체 메타데이터도 문맥을 잠식하거나 의미 검색이 필요해지면, 같은 `skill_list` 앞단에 하이브리드 검색을 추가할 수 있습니다.

### 관리자 MCP와 부모 authority

일반 사용자는 MCP 서버나 Tool을 등록할 수 없습니다. 관리자는 `mcp-servers.json`에서 원격 Streamable HTTP MCP 서버와 `allowedTools`를 명시하고, 부모 또는 관리자는 별도 `mcp-authority.json`에서 이 Identity에 실제 사용권한을 부여합니다.

```text
관리자 allowedTools ∩ 부모/관리자 grants = 모델에 노출되는 MCP Tools
```

개발용 형식은 `config/mcp-servers.example.json`과 `config/mcp-authority.example.json`을 참고하세요. 실제 파일은 기본적으로 환경의 `config` 디렉터리에서 읽으며 다음 변수로 경로를 바꿀 수 있습니다.

| 환경 변수 | 기본값 |
| --- | --- |
| `QUANYEJUN_MCP_SERVERS_FILE` | `<config>/mcp-servers.json` |
| `QUANYEJUN_MCP_AUTHORITY_FILE` | `<config>/mcp-authority.json` |
| `QUANYEJUN_MCP_REFRESH_SECONDS` | `30` |

MCP 인증 헤더 값은 JSON에 기록하지 않고 `headerEnv`가 가리키는 환경 변수에서 읽습니다. 예를 들어 `"Authorization": "GITHUB_MCP_AUTHORIZATION"`은 해당 환경 변수의 전체 값을 헤더로 사용합니다. 설정 파일이 변경되면 런타임이 다시 탐색하며, 서버가 비활성화되거나 grant가 회수된 Tool은 새 호출 전에 제거됩니다.

`grantedBy`는 감사용 출처 표시이며 인증 수단 자체는 아닙니다. 현재 MVP의 관리 경계는 해당 두 파일과 프로세스 환경 변수를 수정할 수 있는 OS 권한입니다. 향후 관리 UI를 추가하려면 관리자/부모 인증과 감사 이벤트를 먼저 붙여야 합니다.

안전 기본값은 HTTPS, 명시적 Tool 이름, 실패 시 권한 제거입니다. 로컬 개발 서버처럼 HTTP가 꼭 필요한 경우에만 관리자가 `allowInsecureHttp: true`를 선언할 수 있습니다. 원격 MCP Registry는 외부 서비스용 선택적 capability provider이며, 현재 MCP 클라이언트 transport는 Streamable HTTP입니다. `shell` authority가 있는 게스트는 일반 프로세스를 실행할 수 있지만, 실행한 stdio MCP 프로세스를 Tool Registry에 동적으로 연결하는 어댑터는 아직 포함하지 않습니다.

### LXC 게스트에 설치

LXC는 오케스트레이션 대상이 아니라 첫 서버 실행 어댑터로 취급합니다. Linux용 바이너리를 빌드한 뒤 전용 LXC 게스트 안에서 설치 스크립트를 실행합니다. 제공되는 systemd 프로필은 LXC 자체를 보안 경계로 보고 게스트 내부 패키지 설치와 시스템 도구 사용을 허용하기 위해 런타임을 root로 실행합니다. 공유 호스트나 신뢰하지 않는 게스트에서 이 프로필을 직접 사용하면 안 됩니다.

```bash
cd runtime
GOOS=linux GOARCH=amd64 go build -o ../dist/quanyejun-runtime ./cmd/server
cd ..
sudo ./deploy/lxc/install.sh ./dist/quanyejun-runtime
```

설치기는 전용 사용자, 영속 디렉터리, `/etc/quanyejun/config.env`, systemd 서비스를 준비합니다. 실제 DB URL과 LLM 키는 게스트의 설정 파일에만 추가하세요. Docker와 Kubernetes는 코어 변경 없이 이후 별도 배포 어댑터로 추가할 수 있습니다.

## API 추론 엔진

운영 환경은 `LLM_PROVIDER`로 하나의 API Provider를 명시적으로 선택합니다. 설정이 잘못된 Provider를 다른 엔진으로 조용히 대체하지 않으므로 실행 기록의 모델과 동작을 추적할 수 있습니다. `builtin`은 API 키 없이 전체 Agent Run과 Tool Call 흐름을 확인하기 위한 개발용 응답기입니다.

### 로컬 LLM

Ollama나 LM Studio처럼 OpenAI 호환 HTTP API를 제공하는 로컬 모델은 Provider와 모델을 명시해서 사용합니다.

```powershell
$env:LLM_PROVIDER = "local"
$env:LOCAL_LLM_BASE_URL = "http://localhost:11434/v1"
$env:LOCAL_LLM_MODEL = "qwen3:8b"
go run ./cmd/server
```

### Anthropic API

```powershell
# Claude API
$env:LLM_PROVIDER = "anthropic"
$env:ANTHROPIC_API_KEY = "..."
$env:ANTHROPIC_MODEL = "your-claude-model"
go run ./cmd/server
```

### OpenAI API

```powershell
# OpenAI Responses API
$env:LLM_PROVIDER = "openai"
$env:OPENAI_API_KEY = "..."
$env:OPENAI_MODEL = "your-openai-model"
$env:OPENAI_BASE_URL = "https://api.openai.com/v1"
$env:OPENAI_STORE = "false"
go run ./cmd/server
```

OpenAI 응답의 Provider-side 저장 여부와 무관하게 QuanYejun의 대화, Agent Run, Tool Call, 기억은 PostgreSQL이 기준 데이터입니다. 키는 저장소에 커밋하지 마세요. 전체 설정 항목은 `.env.example`에 정리되어 있습니다.

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
| `POST /api/messages` | 대화 입력 저장, Agent Run 생성, `202 + runId` 반환 |
| `GET /api/runs/:id` | Agent Run과 영속 실행 단계 조회 |
| `POST /api/tasks/demo` | 연결 독립적인 비동기 작업 시작 |
| `GET /api/events?after=` | 커서 이후 이벤트 조회 |
| `GET /ws?lastEventId=` | 이벤트 재생 후 실시간 스트림 구독 |

## MVP 경계

이 버전은 분산 작업 큐, 복잡한 관계 점수, 다중 Parent, 조직 RBAC, 외부 서비스 연동을 의도적으로 포함하지 않습니다. 먼저 “하나의 Identity가 대화하고, 기억하고, 작업하고, 나중에 다시 이어지는가?”를 검증합니다.
