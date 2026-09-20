package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const maxRequestBytes = 64 * 1024

// Count both IPs and actors: rotating anonymous cookies must not reset the budget.
// The bounded map fails closed when full and reclaims expired entries.
type requestBudget struct {
	mu      sync.Mutex
	entries map[string]budgetWindow
}
type budgetWindow struct {
	count int
	until time.Time
}

func (b *requestBudget) allow(key string, limit int, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = make(map[string]budgetWindow)
	}
	window, exists := b.entries[key]
	if !exists && len(b.entries) >= 10000 {
		for k, v := range b.entries {
			if !now.Before(v.until) {
				delete(b.entries, k)
			}
		}
		if len(b.entries) >= 10000 {
			return false
		}
	}
	if !now.Before(window.until) {
		window = budgetWindow{until: now.Add(time.Minute)}
	}
	if window.count >= limit {
		return false
	}
	window.count++
	b.entries[key] = window
	return true
}

func isWorkRequest(c *gin.Context) bool {
	return c.Request.Method == http.MethodPost && (c.Request.URL.Path == "/api/messages" || c.Request.URL.Path == "/api/tasks/demo")
}

func tooManyRequests(c *gin.Context) {
	c.Header("Retry-After", "60")
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "request limit reached; try again later"})
}

func (s *Server) limitIP(c *gin.Context) {
	if isWorkRequest(c) && !s.budget.allow("ip:"+c.ClientIP(), 20, time.Now()) {
		tooManyRequests(c)
		return
	}
	c.Next()
}
func (s *Server) limitActor(c *gin.Context) {
	if isWorkRequest(c) && !s.budget.allow("actor:"+actor(c), 10, time.Now()) {
		tooManyRequests(c)
		return
	}
	c.Next()
}

func decodeRequest(c *gin.Context, target any) bool {
	if c.Request.ContentLength > maxRequestBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body exceeds 64 KiB"})
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBytes))
	err := decoder.Decode(target)
	if err == nil {
		var extra any
		err = decoder.Decode(&extra)
		if err == io.EOF {
			return true
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body exceeds 64 KiB"})
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
	}
	return false
}
