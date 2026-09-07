# Runtime skills

각 하위 디렉터리는 하나의 로컬 Skill 패키지이며 반드시 `SKILL.md`를 포함합니다.

```text
skills/
  workspace-summary/
    SKILL.md
```

`SKILL.md`는 `name`, `description` YAML frontmatter와 본문 지시문으로 구성합니다. 디렉터리 이름과 `name`은 같아야 하며 소문자 영문, 숫자, `_`, `-`만 사용할 수 있습니다.

Skill은 모델에 작업 절차를 알려줄 뿐입니다. 새 Tool, filesystem capability, authority를 만들거나 우회할 수 없습니다. 환경에 이미 `shell` authority가 있다면 Skill은 CLI 탐색·설치·실행 또는 MCP 선택을 안내할 수 있지만, 실제 실행은 항상 `environment_exec`와 Environment의 권한 검사를 거칩니다. 런타임은 먼저 메타데이터만 `skill_list`로 공개하고, 모델이 선택한 Skill 하나만 `skill_load`로 불러옵니다.

개발 중 이 예제 디렉터리를 사용하려면 저장소 루트 기준으로 다음 환경 변수를 설정합니다.

```powershell
$env:QUANYEJUN_SKILLS_DIR = (Resolve-Path skills)
```
