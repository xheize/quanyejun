# Replaceable runtime environment

QuanYejun의 Identity, 기억, 이벤트, 작업 모델은 배포 기술과 독립적입니다. LXC, Docker, Kubernetes는 코어의 타입이 아니라 같은 실행 환경 계약을 제공하는 서로 다른 배포·실행 어댑터입니다.

## 코어 계약

`runtime/internal/environment`의 `Environment`는 다음 네 가지 동작만 제공합니다.

- `Prepare`: 영속 디렉터리와 실행 전제조건 준비
- `Inspect`: OS, 경로, 도구, capability, authority 보고
- `LookPath`: 환경 안에서 실행 가능한 도구 탐색
- `Run`: 지정된 authority를 검사한 뒤 명령 실행

환경은 `home`, `workspace`, `config`, `cache` 경로를 명시적으로 소유합니다. CLI 기반 LLM provider도 호스트 프로세스를 직접 실행하지 않고 이 계약을 통합니다.

## Capability와 authority

Capability는 환경이 기술적으로 제공하는 기능입니다. Authority는 현재 정책이 사용을 허가한 기능입니다. 명령은 두 조건을 모두 만족해야 실행됩니다. 기본 정책은 외부 임의 셸 API를 열지 않고, 내부 provider가 격리 작업 디렉터리에서 셸을 사용하는 범위만 허용합니다.

## 현재 어댑터

- `local`: 개발 PC 또는 단일 서버 OS에서 직접 실행합니다.
- `deploy/lxc`: 같은 바이너리를 LXC 게스트의 systemd 서비스로 설치하고 영속 경로를 연결합니다.

LXC 게스트 생성·삭제 자체는 아직 코어 책임이 아닙니다. Docker 이미지와 Kubernetes 리소스도 필요해질 때 별도 어댑터로 추가하며, Identity와 provider 코드는 변경하지 않습니다.
