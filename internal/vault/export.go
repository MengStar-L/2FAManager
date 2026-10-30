package vault

import (
	"errors"
	"strings"
)

// ExportCredential is returned only for an explicit credential preview. It must
// never be used in the ordinary state/token DTO or written to logs.
type ExportCredential struct {
	ID      string `json:"id"`
	Issuer  string `json:"issuer"`
	Account string `json:"account"`
	Secret  string `json:"secret"`
	URI     string `json:"uri"`
}

// ExportSnapshot keeps the original selected records private to the vault so a
// later deletion can check they have not changed since export.
type ExportSnapshot struct {
	credentials []ExportCredential
	records     []record
	text        string
}

func (s *ExportSnapshot) Credentials() []ExportCredential {
	return append([]ExportCredential(nil), s.credentials...)
}

func (s *ExportSnapshot) Text() string { return s.text }

func (s *Store) ExportSelection(ids []string) (*ExportSnapshot, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, errors.New("请选择 1–100 个账号查看或导出凭证")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := &ExportSnapshot{}
	seen := make(map[string]bool, len(ids))
	var lines []string
	for _, id := range ids {
		if seen[id] {
			return nil, errors.New("所选账号重复，请重新选择")
		}
		seen[id] = true
		index := s.index(id)
		if index < 0 {
			return nil, ErrNotFound
		}
		r := s.records[index]
		uri, err := ExportURI(r.TokenInput)
		if err != nil {
			return nil, err
		}
		// A backup used to authorize deletion must be accepted by the importer.
		parsed, err := PreviewURI(uri)
		if err != nil || parsed.Issuer != r.Issuer || parsed.Account != r.Account || parsed.Secret != r.Secret || parsed.Algorithm != r.Algorithm || parsed.Digits != r.Digits || parsed.Period != r.Period {
			return nil, errors.New("所选账号无法生成兼容的令牌链接，请检查服务和账户名称")
		}
		snapshot.records = append(snapshot.records, r)
		snapshot.credentials = append(snapshot.credentials, ExportCredential{ID: r.ID, Issuer: r.Issuer, Account: r.Account, Secret: r.Secret, URI: uri})
		lines = append(lines, uri)
	}
	snapshot.text = strings.Join(lines, "\n")
	if len(snapshot.text) > 256<<10 {
		return nil, errors.New("导出内容过长，请减少所选账号数量")
	}
	return snapshot, nil
}

// DeleteExportSnapshot performs one encrypted replacement after validating every
// selected record. A stale/missing record or storage failure changes nothing.
func (s *Store) DeleteExportSnapshot(snapshot *ExportSnapshot) error {
	if snapshot == nil || len(snapshot.records) == 0 {
		return errors.New("导出记录无效，请重新导出")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := make(map[string]bool, len(snapshot.records))
	for _, original := range snapshot.records {
		index := s.index(original.ID)
		if index < 0 || s.records[index] != original {
			return errors.New("刚才导出的账号已被修改或删除，未删除任何账号；请重新选择并导出")
		}
		selected[original.ID] = true
	}
	next := make([]record, 0, len(s.records)-len(selected))
	for _, r := range s.records {
		if !selected[r.ID] {
			next = append(next, r)
		}
	}
	if err := s.save(next); err != nil {
		return err
	}
	s.records = next
	return nil
}
