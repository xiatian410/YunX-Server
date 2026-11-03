package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

type providerCleanup struct {
	Session ParseSession `json:"session"`
	Source  MediaSource  `json:"source"`
}

func cleanupID(s *ParseSession, source MediaSource) string {
	sum := sha256.Sum256([]byte(s.Platform + "\x00" + s.AccountID + "\x00" + source.Cleanup))
	return hex.EncodeToString(sum[:])
}
func (p *Providers) rememberCleanup(s *ParseSession, sources []MediaSource) error {
	for _, source := range sources {
		if source.Cleanup == "" {
			continue
		}
		copy := *s
		copy.Items = nil
		copy.Cursors = nil
		if e := p.Store.Put("provider_cleanup", cleanupID(s, source), providerCleanup{copy, source}); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) cleanupLater(t Task) {
	if m.closing || t.Session == nil || len(t.Sources) == 0 {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		if e := m.providers.Cleanup(ctx, t.Session, t.Sources); e != nil {
			m.store.Event(t.ID, "cleanup_pending", "平台临时转存尚未清理，可在设置中手动重试")
		}
	}()
}
