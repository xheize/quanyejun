package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

func (s *MemoryStore) GetVisitorProfile(_ context.Context, actor string) (domain.VisitorProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.profiles[actor]
	if domain.Expired(p.ExpiresAt) {
		return domain.VisitorProfile{}, nil
	}
	return p, nil
}

func (s *MemoryStore) SetVisitorProfile(ctx context.Context, actor, name string) (domain.VisitorProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profiles == nil {
		s.profiles = make(map[string]domain.VisitorProfile)
	}
	if name == "" {
		delete(s.profiles, actor)
		return domain.VisitorProfile{}, nil
	}
	p := domain.VisitorProfile{DisplayName: name, ExpiresAt: domain.Expiry(ctx, actor, time.Now().UTC(), nil)}
	// Editing a live anonymous record cannot extend its original retention window.
	if old := s.profiles[actor]; !domain.Expired(old.ExpiresAt) {
		p.ExpiresAt = domain.Earliest(p.ExpiresAt, old.ExpiresAt)
	}
	s.profiles[actor] = p
	return p, nil
}

func (s *PostgresStore) GetVisitorProfile(ctx context.Context, actor string) (domain.VisitorProfile, error) {
	var p domain.VisitorProfile
	err := s.pool.QueryRow(ctx, `SELECT display_name,expires_at FROM visitor_profiles WHERE actor_id=$1 AND (expires_at IS NULL OR expires_at>CURRENT_TIMESTAMP)`, actor).Scan(&p.DisplayName, &p.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.VisitorProfile{}, nil
	}
	return p, err
}

func (s *PostgresStore) SetVisitorProfile(ctx context.Context, actor, name string) (domain.VisitorProfile, error) {
	if name == "" {
		_, err := s.pool.Exec(ctx, `DELETE FROM visitor_profiles WHERE actor_id=$1`, actor)
		return domain.VisitorProfile{}, err
	}
	p := domain.VisitorProfile{DisplayName: name, ExpiresAt: domain.Expiry(ctx, actor, time.Now().UTC(), nil)}
	err := s.pool.QueryRow(ctx, `INSERT INTO visitor_profiles (actor_id,display_name,expires_at) VALUES ($1,$2,$3) ON CONFLICT (actor_id) DO UPDATE SET display_name=EXCLUDED.display_name, expires_at=CASE WHEN visitor_profiles.expires_at<=CURRENT_TIMESTAMP THEN EXCLUDED.expires_at ELSE LEAST(visitor_profiles.expires_at,EXCLUDED.expires_at) END RETURNING display_name,expires_at`, actor, name, p.ExpiresAt).Scan(&p.DisplayName, &p.ExpiresAt)
	return p, err
}
