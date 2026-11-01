# 관리용 파일 전달 API

운영자가 문서, 소스, 데이터, 설정, 자격증명 등 임의의 파일을 런타임에
전달하는 API입니다. 웹 채팅의 첨부 기능은 제공하지 않습니다. Postman이나
curl로 직접 호출하며, 업로드는 대화·기억·이벤트·Agent Run을 생성하지 않습니다.

## 서버 설정

```dotenv
QUANYEJUN_TRANSFER_API_KEY=<암호학적 난수로 생성한 64자리 hex 토큰>
QUANYEJUN_TRANSFER_ROOTS=
QUANYEJUN_TRANSFER_MAX_BYTES=268435456
QUANYEJUN_TRANSFER_MAX_EXPANDED_BYTES=536870912
```

- 키가 비어 있으면 API는 `404`로 비활성화됩니다. 설정한 키는 32–512자의
  공백 없는 printable ASCII여야 합니다. 사람이 만든 비밀번호 대신 32바이트
  난수를 사용하세요. 서버는 시작 시 키를 읽고 메모리에는 SHA-256 검증값을
  보관합니다. 요청의 토큰을 해시하고 상수 시간 비교로 검증합니다.
- 기본 허용 루트는 `workspace` 하나이며, 실제 경로는 `QUANYEJUN_WORKSPACE`로
  결정됩니다. 기본 로컬 실행은 `.runtime/workspace`, 제공된 Compose/LXC는
  `/var/lib/quanyejun/workspace`입니다.
- `QUANYEJUN_TRANSFER_ROOTS`에 JSON 객체를 설정하면 기본 루트 목록을 **교체**합니다.
  각 경로는 서버/LXC/컨테이너 **내부의 절대 경로**이고, 시작 전에 존재해야 합니다.
  존재하지 않는 루트나 잘못된 설정은 서버 시작 실패로 처리합니다.
- 업로드 전체 본문 기본 상한은 256 MiB, ZIP 해제 후 파일 내용의 합은 512 MiB입니다.
  각각 최대 1 GiB, 4 GiB까지 설정할 수 있습니다. multipart 경계와 헤더도 본문
  상한에 포함됩니다. ZIP 엔트리와 암시적 디렉터리를 포함한 파일·폴더 수는 각각
  최대 10,000개, 상대 경로는 최대 32단계입니다.
- 프로세스당 한 번에 하나의 전송만 처리하고, 관리 API 요청은 IP당 분당 30회로
  제한합니다. 본문 읽기 제한 시간은 5분입니다. 다중 replica는 외부 공통 제한과
  공유 파일시스템 조정이 필요합니다. 기존 `TRUSTED_PROXIES` 설정이 IP 판정에 적용됩니다.

Linux에서 토큰은 `openssl rand -hex 32`로 생성할 수 있습니다. 생성한 값은
서버 환경변수와 Postman의 로컬 비밀 변수에만 저장하고 저장소에 커밋하지 마세요.
Go 직접 실행은 `.env`를 자동 로드하지 않습니다. 셸 환경변수로 전달해야 합니다.
Compose는 루트 `.env`의 값을 backend로 전달하며, LXC 서비스는
`/etc/quanyejun/config.env`를 읽습니다. 변경 후 프로세스를 재시작하거나 컨테이너를
재생성해야 합니다. 인증키 교체 시 이전 키는 새 프로세스에서 즉시 무효가 됩니다.

예를 들어 workspace와 별도의 자격증명 폴더를 허용하려면 다음처럼 설정합니다.

```dotenv
QUANYEJUN_TRANSFER_ROOTS='{"workspace":"/var/lib/quanyejun/workspace","credentials":"/var/lib/quanyejun/credentials"}'
```

관리자가 credentials 디렉터리를 먼저 만들고 런타임 사용자에게 쓰기 권한을
부여해야 합니다. Docker 이미지는 UID 10001로 실행합니다. Compose의 기존
`/var/lib/quanyejun` 볼륨 안에 준비하면 재생성 후에도 파일이 유지됩니다.
다른 경로를 사용한다면 별도로 영속 볼륨을 연결하세요. 기본 `/etc/quanyejun`은
컨테이너에서 관리자 소유이므로 ROOTS 설정만으로 쓰기 권한이 생기지 않습니다.

## Postman에서 파일 보내기

1. `POST {{base_url}}/api/admin/files` 요청을 만듭니다.
2. **Authorization → Bearer Token**에 서버에 설정한 토큰을 입력합니다.
3. **Params**에서 `root=workspace`, `path=imports/example.txt`를 설정합니다.
4. **Body → form-data**에서 이름이 `file`인 행을 추가하고 타입을 **File**로
   바꾼 뒤 로컬 파일을 선택합니다.
5. 전송하면 `<workspace>/imports/example.txt`에 저장됩니다. 중간 디렉터리는
   자동 생성합니다. 업로드 파일의 원래 이름은 저장 경로에 사용하지 않습니다.

`Content-Type`은 수동으로 입력하지 마세요. Postman이 multipart boundary를
포함해서 생성합니다. `root`, `path` 등의 옵션은 form-data가 아니라 **Params**에
넣습니다. `file` 필드는 정확히 하나만 받습니다. 웹 쿠키나 `X-Requested-With`
헤더는 필요하지 않으며, 키를 URL이나 쿼리에 넣어도 인증되지 않습니다.

[Postman Collection](examples/admin-file-transfer.postman_collection.json)을 가져오면
파일 업로드, 파일 교체, ZIP 디렉터리 전달 요청을 사용할 수 있습니다.
`base_url`, `transfer_api_key`, `root`, `file_path`, `directory_path`를 설정하고
각 요청의 File 행에서 로컬 파일을 선택하세요. 실제 키를 공유 컬렉션으로 내보내지 마세요.

## 디렉터리 보내기

폴더를 ZIP으로 압축하고 같은 `file` 필드에 선택합니다. Params에
`format=zip`, `path=imports/project-v1`을 지정합니다.

```text
ZIP 내용                     최종 경로
README.md                    <workspace>/imports/project-v1/README.md
src/main.go                  <workspace>/imports/project-v1/src/main.go
empty/                       <workspace>/imports/project-v1/empty/
```

ZIP의 최상위 폴더를 자동 제거하지 않습니다. ZIP에 `my-project/README.md`가
있으면 목적지 아래에도 `my-project/`가 생깁니다. 빈 디렉터리도 보존합니다.
ZIP 파일을 해제하지 않고 그대로 보관하려면 `format=file`을 사용하세요.

새 디렉터리 전체를 준비한 뒤 목적지로 이동합니다. 기존 디렉터리에 병합하거나
덮어쓰지 않으며, 목적지가 이미 존재하면 `409`입니다. 새 버전은 새로운 경로로
전달하세요. 단일 파일은 `overwrite=true`를 명시하면 기존 일반 파일을 교체합니다.
기존 디렉터리·심볼릭 링크는 파일 교체 대상이 아닙니다.

## curl 예제

Linux 셸에서 `TRANSFER_KEY`에 토큰을 별도로 설정한 뒤 실행합니다.

```sh
curl --fail-with-body \
  -H "Authorization: Bearer ${TRANSFER_KEY}" \
  -F 'file=@./example.txt' \
  'https://runtime.example.com/api/admin/files?root=workspace&path=imports/example.txt'

curl --fail-with-body \
  -H "Authorization: Bearer ${TRANSFER_KEY}" \
  -F 'file=@./project.zip' \
  'https://runtime.example.com/api/admin/files?root=workspace&path=imports/project-v1&format=zip'

curl --fail-with-body \
  -H "Authorization: Bearer ${TRANSFER_KEY}" \
  -F 'file=@./kubeconfig' \
  'https://runtime.example.com/api/admin/files?root=credentials&path=kubeconfig&overwrite=true'
```

PowerShell에서는 `curl` 별칭 대신 `curl.exe`를 사용하고 환경변수는
`$env:TRANSFER_KEY`로 참조합니다. 공백·한글이 있는 `path` 값은 Postman Params의
URL 인코딩을 사용하세요. 경로 구분자는 `/`입니다.

## 응답과 실패 처리

성공한 새 파일/디렉터리는 `201`, 기존 파일 교체는 `200`을 반환합니다.

```json
{"root":"workspace","path":"imports/example.txt","format":"file","files":1,"directories":0,"bytes":42,"replaced":false}
```

`bytes`는 파일 내용의 바이트 수이며 ZIP은 해제된 전체 파일 내용의 합입니다.
`directories`는 ZIP 내부의 명시적·암시적 하위 디렉터리 수이며 목적지 자체는
포함하지 않습니다. 단일 파일 업로드의 부모 디렉터리는 이 값에 포함하지 않습니다.

| 상태 | 의미 |
| --- | --- |
| 400 | 잘못된 옵션·multipart·ZIP·경로, 중복 파일, 경로 충돌 |
| 401 | Bearer 키 누락 또는 불일치 |
| 403 | Origin 헤더가 있는 브라우저 요청 |
| 404 | 관리키가 설정되지 않아 비활성화됨 |
| 409 | 목적지 충돌, 교체할 수 없는 파일 종류 |
| 413 | 업로드/해제 크기 또는 ZIP 엔트리 수 상한 초과 |
| 429 | IP 요청 제한 또는 다른 업로드 처리 중 |
| 500 | 쓰기 권한·디스크 공간 등 서버 파일시스템 문제 |

전송이 중단되거나 ZIP 검증이 실패하면 최종 파일/디렉터리를 게시하지 않고
임시 파일을 정리합니다. 다만 미리 만든 빈 부모 디렉터리는 남을 수 있습니다.
프로세스 강제 종료나 호스트 장애 시 `.quanyejun-transfer-*` 임시 항목이 남을 수
있으며, 진행 중인 전송이 없을 때 관리자가 정리합니다. 응답 유실 후 같은
경로로 재시도하면 이미 완료된 전송은 `409`가 될 수 있습니다. 디렉터리의
외부 수정이나 여러 서버의 동시 쓰기를 조정하는 분산 트랜잭션은 제공하지 않습니다.
파일 게시에는 동일 파일시스템의 hard link/rename을 사용하므로 해당 기능을
지원하는 로컬 파일시스템이나 볼륨을 사용하세요.

## 권한과 운영 경계

- HTTPS 또는 신뢰할 수 있는 TLS 프록시를 사용하세요. 프록시의 요청 크기·시간
  제한도 맞춰야 합니다. 이 API는 CORS를 제공하지 않고 Origin이 있는 요청은
  거절합니다. 가능하면 관리망에서만 접근시키세요.
- 키는 등록된 모든 루트에 쓰기 권한을 줍니다. 웹 로그인과 모델의 파일 쓰기
  authority는 이 관리 API의 인증 수단이 아닙니다. 루트에는 필요한 폴더만
  등록하세요. config/skills 같은 경로에 쓰면 기존 런타임의 설정 감시나 다음
  Skill 로드에 영향을 줄 수 있습니다. API 자체는 명령 실행·재시작을 하지 않습니다.
- 절대 경로, `..`, 역슬래시, Windows 장치 이름, 기존 부모 심볼릭 링크와 ZIP의
  심볼릭 링크·장치 파일을 거절합니다. 파일 형식·확장자는 제한하지 않습니다.
  파일은 `0600`, 디렉터리는 `0700`으로 만들며 ZIP의 실행 비트·소유자·시간은
  복원하지 않습니다. Windows의 실제 접근 권한은 ACL에 따릅니다.
- 업로드 내용·인증 헤더·파일 경로는 관리 API 로그에 기록하지 않습니다.
  성공 로그에는 형식, 파일 수, 바이트 수, 교체 여부만 남깁니다.
- 서버의 관리키 환경변수는 `environment_exec` 자식 프로세스에서 제거합니다.
  이는 동일 OS 사용자나 root 셸에 대한 비밀 격리를 보장하지 않습니다. 업로드한
  파일도 런타임/셸의 OS 권한 범위에서 읽을 수 있습니다.
- 전달된 설정 파일의 사용 경로는 별도로 연결해야 합니다. 예를 들어 kubeconfig는
  런타임의 `KUBECONFIG`를 해당 경로로 설정하고 프로세스를 재시작합니다. 업로드만으로
  CLI 설정이나 모델 authority가 자동 변경되지는 않습니다.
- 자동 만료·자동 실행·다운로드·목록·삭제 API는 제공하지 않습니다. 운영자가
  전달한 파일은 익명 대화의 72시간 만료와 별개로 영속 볼륨에 유지됩니다.
