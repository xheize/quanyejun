package domain

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const AnonymousLifetime = 72 * time.Hour

type expiryKey struct{}

func Anonymous(actor string) bool { return strings.HasPrefix(actor, "anon:") }
func Earliest(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil || a.Before(*b) {
		return a
	}
	return b
}

// Copies of a message, memory, run or task cannot outlive their source record.
func EventExpiry(ctx context.Context, event Event) *time.Time {
	expiry := Expiry(ctx, event.ActorID, event.Timestamp, event.ExpiresAt)
	data, _ := json.Marshal(event.Payload)
	var value any
	_ = json.Unmarshal(data, &value)
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if raw, ok := x["expiresAt"].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
					expiry = Earliest(expiry, &t)
				}
			}
			for _, child := range x {
				visit(child)
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(value)
	return expiry
}
func Expired(expiry *time.Time) bool { return expiry != nil && !expiry.After(time.Now().UTC()) }
func WithExpiry(ctx context.Context, expiry *time.Time) context.Context {
	return context.WithValue(ctx, expiryKey{}, Earliest(ContextExpiry(ctx), expiry))
}
func ContextExpiry(ctx context.Context) *time.Time {
	expiry, _ := ctx.Value(expiryKey{}).(*time.Time)
	return expiry
}
func Expiry(ctx context.Context, actor string, created time.Time, explicit *time.Time) *time.Time {
	expiry := Earliest(explicit, ContextExpiry(ctx))
	if Anonymous(actor) {
		deadline := created.Add(AnonymousLifetime)
		expiry = Earliest(expiry, &deadline)
	}
	return expiry
}
