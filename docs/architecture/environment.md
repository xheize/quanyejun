# Replaceable runtime environment

QuanYejun의 Identity, 기억, 이벤트, 작업 모델은 배포 기술과 독립적입니다. LXC, Docker, Kubernetes는 코어의 타입이 아니라 같은 실행 환경 계약을 제공하는 서로 다른 배포·실행 어댑터입니다.

## 코어 계약

`runtime/internal/environment`의 `Environment`는 다음 동작을 제공합니다.

- `Prepare`: 영속 디렉터리와 실행 전제조건 준비
- `Inspect`: OS, 경로, 도구, capability, authority 보고
- `LookPath`: 환경 안에서 실행 가능한 도구 탐색
- `ReadFile`: authority를 검사하고 워크스페이스 경계 안의 파일 읽기
- `Run`: 지정된 authority를 검사한 뒤 명령 실행

환경은 `home`, `workspace`, `config`, `cache` 경로를 명시적으로 소유합니다. 실행되는 프로세스에는 이 경로를 `HOME`, XDG 및 `QUANYEJUN_*` 환경 변수로 전달하므로 CLI 인증과 캐시는 Identity 환경 안에 유지됩니다. LLM Provider는 모델 API 호출만 담당하며 모델이 요청한 환경 작업은 Tool Registry가 이 계약을 통해 실행합니다.

## Capability와 authority

Capability는 환경이 기술적으로 제공하는 기능입니다. Authority는 현재 정책이 사용을 허가한 기능입니다. 명령은 두 조건을 모두 만족해야 실행됩니다. `shell`이 capability와 authority 양쪽에 있을 때만 Agent Harness가 `environment_exec`를 모델에 노출합니다. 명령은 executable과 args를 분리하고, 워크스페이스 경계·timeout·입출력 크기 제한을 적용한 뒤 실행합니다. 패키지 설치 가능 여부는 게스트 OS 사용자 권한과 systemd/container 프로필이 최종 결정합니다.

## 현재 어댑터

- `local`: 개발 PC 또는 단일 서버 OS에서 직접 실행합니다.
- `deploy/lxc`: 같은 바이너리를 전용 LXC 게스트의 systemd 서비스로 설치하고 영속 경로를 연결합니다. 이 프로필은 LXC를 격리 경계로 보고 게스트 내부 root 권한을 제공합니다.

LXC 게스트 생성·삭제 자체는 아직 코어 책임이 아닙니다. Docker 이미지와 Kubernetes 리소스도 필요해질 때 별도 어댑터로 추가하며, Identity와 provider 코드는 변경하지 않습니다.

## Agent Harness 경계

Agent Harness는 LLM Provider와 실행 환경 사이의 유일한 Tool 실행 경계입니다. Provider는 구조화된 Tool Call을 제안하고, Tool Registry는 등록 여부와 인자를 검사한 뒤 Environment가 capability와 authority를 다시 검사합니다. `environment_inspect`는 OS와 설치된 CLI를 알려주고, `environment_exec`는 동기 프로세스를 실행합니다. Harness는 실행마다 모델 턴 수, Tool Call 수, 전체 시간과 개별 명령 시간을 제한합니다.

실행 단계의 전체 입력과 출력은 운영 감사와 재개를 위해 Store에 저장합니다. 실시간 이벤트에는 단계 메타데이터만 게시하여 워크스페이스 파일 내용이나 Skill 본문을 불필요하게 브로드캐스트하지 않습니다.

## Skills와 RAG

로컬 Skill은 `<skills-root>/<name>/SKILL.md` 패키지입니다. 디렉터리 탈출, 심볼릭 링크 탈출, 비정상 이름, 64 KiB 초과 파일을 거부하며 파일 내용의 해시를 버전으로 사용합니다. `skill_list`는 메타데이터만, `skill_load`는 선택한 하나의 지시문만 반환합니다.

Skill은 실행 환경의 capability나 authority가 아닙니다. 지시문이 새로운 권한을 주장하더라도 Harness가 노출하지 않은 Tool은 호출할 수 없습니다. 이미 허가된 환경 안에서는 Skill이 기존 CLI 사용, 패키지 설치, 원격 MCP 선택 또는 작은 어댑터 작성을 안내할 수 있습니다.

MVP에는 벡터 DB 기반 RAG를 넣지 않습니다. 소수 Skill에서는 목록 기반 선택이 더 단순하고 결정적입니다. 일반 문서 코퍼스가 커져 키워드와 의미 검색이 모두 필요해질 때 `skill_list` 또는 별도 knowledge tool의 구현을 하이브리드 검색으로 교체하되, 권한 검사와 Tool 실행 경계는 그대로 유지합니다.

## 관리자 MCP Registry

MCP 연결은 일반 사용자 기능이 아니다. 관리자가 서버 inventory와 정확한 `allowedTools`를 관리하고, 부모 또는 관리자가 Identity별 authority grant를 별도 파일로 관리한다. 모델에 노출되는 Tool은 두 목록의 교집합뿐이다. Skill, 사용자 메시지, MCP 서버가 반환한 설명은 이 설정을 수정하거나 권한을 확장할 수 없다.

런타임은 원격 Streamable HTTP MCP 서버에 `initialize`, `notifications/initialized`, `tools/list`, `tools/call` 순서로 연결한다. 원격 Tool 이름은 Provider 호환 이름으로 namespace 처리하고 원본 이름과 매핑한다. Tool 설명과 schema는 비신뢰 원격 입력으로 취급하며 응답 크기, Tool 수, 페이지 수, 설명 길이와 호출 시간을 제한한다.

설정 또는 authority가 잘못되면 MCP Tool 전체를 fail-closed로 제거한다. 특정 서버가 응답하지 않으면 해당 서버의 Tool만 제외한다. 설정 해시를 주기적으로 검사하고 실행 직전에도 회수 여부를 확인하므로 과거 모델 응답에 포함된 Tool Call이 새 authority를 우회할 수 없다.

인증값은 관리자 설정 파일에 넣지 않고 환경 변수 참조로만 주입하며 실행 이벤트에 남기지 않는다. 현재 관리 신뢰 경계는 OS 파일 권한이다. 인증된 관리 API가 생기기 전까지 일반 HTTP API에는 MCP 등록·grant 변경 endpoint를 제공하지 않는다. 원격 MCP Registry는 Streamable HTTP만 지원한다. `shell` authority로 stdio 프로세스를 실행하는 것은 가능하지만, 그 프로세스의 MCP Tool을 동적으로 등록하는 transport 어댑터는 별도 기능이다.
