package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

func (s *Server) visitorProfile(c *gin.Context) {
	p := principal(c)
	profile, err := s.store.GetVisitorProfile(c.Request.Context(), p.ActorID)
	if err != nil {
		c.Abort()
		fail(c, err)
		return
	}
	p.DisplayName = (domain.VisitorContext{DisplayName: profile.DisplayName}).Normalized().DisplayName
	p.DisplayNameExpiresAt = profile.ExpiresAt
	c.Set("principal", p)
	c.Next()
}

func (s *Server) setDisplayName(c *gin.Context) {
	var input struct {
		DisplayName *string `json:"displayName"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.DisplayName == nil {
		c.JSON(400, gin.H{"error": "displayName 문자열만 입력해주세요"})
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		c.JSON(400, gin.H{"error": "invalid JSON body"})
		return
	}
	name, err := domain.ValidateVisitorName(*input.DisplayName)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	p := principal(c)
	profile, err := s.store.SetVisitorProfile(c.Request.Context(), p.ActorID, name)
	if err != nil {
		fail(c, err)
		return
	}
	p.DisplayName = (domain.VisitorContext{DisplayName: profile.DisplayName}).Normalized().DisplayName
	p.DisplayNameExpiresAt = profile.ExpiresAt
	c.JSON(200, p)
}
