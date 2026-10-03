# 상대 식별과 익명 데이터 보존

## 지속되는 관계

Identity는 하나이고 `ConversationID=global`을 유지한다. 접속마다 대화를 새로 만들지 않는다. 상대는 서버가 인증한 `actorId`로 구분하며 요청 본문이나 query의 `actorId`는 권한 근거로 사용하지 않는다.

- 로그인: 기존 SSO의 Authorization Code + S256 PKCE → access token 교환 → UserInfo의 `sub` 확인. issuer와 sub를 조합한 안정적인 ID로 기록을 연결한다. 이메일 변경은 상대 ID를 바꾸지 않는다.
- 익명: 서버가 UUID를 발급하고 AES-GCM으로 보호한 HttpOnly 쿠키로 브라우저에 유지한다. 쿠키는 90일 유효하지만 데이터 보존기간을 연장하지 않는다. 다른 브라우저·기기·쿠키 삭제 후에는 다른 익명 상대다.
- 로그인해도 익명 기록을 회원 또는 글로벌 메모리로 자동 이전하지 않는다. 로그아웃 후에는 새 익명 관계를 시작한다.
- 기존 로컬 소유자 기록을 임의의 SSO 사용자에게 자동 귀속시키지 않는다. 기존 기록의 소유자 매핑은 별도 관리 작업이다.

## SSO 설정

SSO 주소 예시는 `https://sso.example.com`이다. 연동 서버는 `/oauth2/auth`, `/oauth2/token`, `/oauth2/userinfo` 계약을 제공해야 한다.

실제 사용할 issuer를 discovery 응답으로 확인해야 한다. 서버는 시작 시 discovery의 issuer·endpoint와 설정의 일치를 검사하고, 별칭 주소가 다른 issuer를 가리키면 시작을 거부한다. 이 확인은 client 등록과 실제 로그인 검증을 대체하지 않는다.

1. SSO 관리자에서 client를 등록한다: `authorization_code`, response `code`, scope `openid`, PKCE `S256`.
2. `SSO_ISSUER_URL`, `SSO_CLIENT_ID`, confidential client이면 `SSO_CLIENT_SECRET`을 서버에 설정한다.
3. `SSO_REDIRECT_URL`에 API의 `/auth/callback` 전체 URL을 넣고 SSO의 redirect URI와 정확히 일치시킨다. `WEB_PUBLIC_URL`은 로그인 후 돌아갈 웹 URL이다.
4. `ALLOWED_ORIGINS`는 실제 웹 origin만 명시한다. 쿠키 인증에서 `*`는 서버 시작 시 거부한다. 웹/API는 같은 사이트의 HTTPS 도메인 또는 localhost의 다른 포트를 사용한다. 서로 다른 사이트를 넘나드는 third-party cookie 배치는 지원하지 않는다.
5. `SESSION_ENCRYPTION_KEY`는 선택 사항이다. 미지정 시 HOME의 `session.key`에 32바이트 키를 생성한다. 여러 replica는 같은 키를 사용해야 하며 키 교체는 모든 기존 쿠키를 무효화한다.

인증 세션은 SSO access token의 유효기간과 30분 중 짧은 값으로 제한한다. 각 HTTP 요청과 WebSocket의 주기 점검에서 UserInfo를 재조회해 차단·폐기를 확인한다. SSO 조회 실패 시 인증 세션의 요청을 거부한다. 만료 후에는 다시 로그인해야 하며 refresh token은 저장하지 않는다. 토큰은 암호화된 쿠키에만 포함되고 모델·대화 DB·프론트 JavaScript에는 전달되지 않는다.

## 접근 경계

대화, 기억, 작업, 실행 조회, 이벤트 replay와 WebSocket 전달은 상대별로 제한한다. 로그인 여부는 소유자 권한을 뜻하지 않는다. 익명 및 SSO 방문자는 대화와 자기 기억 저장만 사용할 수 있고, 소유자의 workspace·CLI·Skill·MCP 권한은 자동 부여하지 않는다. 별도 소유자 위임 정책은 후속 기능이다.

브라우저 요청은 `credentials: include`를 사용한다. 변경 요청은 `X-Requested-With: QuanYejun` 헤더와 허용 origin 검사를 요구한다. 로그인 callback에는 쿠키에 묶인 state와 PKCE를 사용한다. 응답은 `Cache-Control: no-store`이며 callback code나 대화 원문을 HTTP 접근 로그로 남기지 않는다.

## 익명 데이터: 생성 기준 72시간

각 메시지·기억·작업·실행·실행 단계·이벤트는 `expires_at`을 갖는다. 익명 원본의 만료는 생성 시각 + 72시간이며 재방문, 조회, 새 메시지 또는 진행 상태 갱신으로 연장하지 않는다. 로그인 사용자 기록은 기본적으로 만료하지 않는다.

이미 만료한 데이터는 정리 작업과 무관하게 모든 저장소 조회에서 즉시 제외한다. 서버 시작 시와 실행 중 30초마다 만료 행을 삭제한다. 따라서 논리적 접근은 정확한 만료 시각에 차단되고 실제 행 삭제는 다음 정리 주기에 일어난다. 서버 중단 중에는 삭제 작업이 실행되지 않으며 재시작 시 먼저 정리한다.

파생 기록은 원본보다 오래 남지 않는다. 모델 실행은 입력에 포함된 익명 대화·기억 중 가장 이른 만료를 사용한다. 답변·새 기억·도구 결과·실행 단계와 이벤트 복사본이 그 시각을 이어받는다. 실행 자체도 이 시각에 취소한다. 이 보수적인 정책 때문에 최근 생성한 답변이 72시간보다 일찍 만료할 수 있다.

원본을 보존하는 DB 백업, 외부 로그 수집기, 외부 LLM 사업자의 보관 정책은 이 애플리케이션 정리 작업의 범위 밖이다. 운영 환경에서도 같은 보존 요구를 적용해야 한다. 익명 OpenAI 요청은 Responses의 `store`를 강제로 false로 보낸다.

프론트는 만료 메타데이터에 따라 열린 화면에서도 기록을 제거한다. 브라우저를 닫아도 기록을 남기는 localStorage에는 대화 내용을 저장하지 않는다.
