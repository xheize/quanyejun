package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const DefaultVisitorName = "방문자님"

// VisitorContext contains no internal identifier. Role is set by server authentication.
type VisitorContext struct {
	Role        string `json:"role"`
	DisplayName string `json:"displayName"`
}

func (v VisitorContext) Normalized() VisitorContext {
	if v.Role != "authenticated" {
		v.Role = "anonymous"
	}
	if v.DisplayName == "" {
		v.DisplayName = DefaultVisitorName
	}
	return v
}

type VisitorProfile struct {
	DisplayName string
	ExpiresAt   *time.Time
}

// Empty text explicitly resets the alias. Count Unicode code points, not bytes.
func ValidateVisitorName(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("별칭은 올바른 UTF-8이어야 합니다")
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", fmt.Errorf("별칭에는 제어 문자를 사용할 수 없습니다")
		}
	}
	name := strings.Join(strings.Fields(raw), " ")
	if utf8.RuneCountInString(name) > 40 {
		return "", fmt.Errorf("별칭은 40자 이하여야 합니다")
	}
	return name, nil
}
