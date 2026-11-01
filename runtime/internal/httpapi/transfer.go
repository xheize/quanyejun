package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quanyejun/quanyejun/runtime/internal/transfer"
)

// SetTransfers is called at startup, before Handler is served.
func (s *Server) SetTransfers(service *transfer.Service) { s.transfers = service }

func (s *Server) uploadFile(c *gin.Context) {
	if s.transfers == nil {
		c.Status(http.StatusNotFound)
		return
	}
	if !s.budget.allow("transfer:"+c.ClientIP(), 30, time.Now()) {
		tooManyRequests(c)
		return
	}
	values := c.Request.Header.Values("Authorization")
	var token string
	if len(values) == 1 {
		scheme, value, ok := strings.Cut(values[0], " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			token = value
		}
	}
	digest := sha256.Sum256([]byte(token))
	expected := s.transfers.KeyDigest()
	if token == "" || subtle.ConstantTimeCompare(digest[:], expected[:]) != 1 {
		c.Header("WWW-Authenticate", "Bearer")
		c.Status(http.StatusUnauthorized)
		return
	}
	// This is an operator API, not a browser endpoint. No credentialed CORS.
	if c.GetHeader("Origin") != "" {
		c.Status(http.StatusForbidden)
		return
	}
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(5 * time.Minute))
	defer controller.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	result, err := s.transfers.Upload(c.Request.WithContext(ctx), c.Writer)
	if err != nil {
		var requestError *transfer.Error
		if errors.As(err, &requestError) {
			if requestError.Status == http.StatusTooManyRequests {
				c.Header("Retry-After", "5")
			}
			c.JSON(requestError.Status, gin.H{"error": requestError.Message})
		} else {
			// Do not log filesystem errors: paths and file contents may be sensitive.
			slog.Warn("management file transfer failed", "status", http.StatusInternalServerError)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "file transfer failed"})
		}
		return
	}
	slog.Info("management file transfer completed", "format", result.Format, "files", result.Files, "bytes", result.Bytes, "replaced", result.Replaced)
	status := http.StatusCreated
	if result.Replaced {
		status = http.StatusOK
	}
	c.JSON(status, result)
}
