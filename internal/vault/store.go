package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"LumaAuthenticator/internal/platform"
)

const (
	vaultFileName = "vault.dat"
	maxVaultSize  = 8 << 20
	maxTokens     = 10000
	vaultMagic    = "LUMA-VAULT\x01"
)

var (
	ErrDuplicate    = errors.New("此令牌已存在，无需重复导入")
	ErrNotFound     = errors.New("找不到这个令牌，请刷新后重试")
	ErrInvalidVault = errors.New("令牌库损坏或无法解密；原文件已保留，请勿删除")
)

type record struct {
	ID       string `json:"id"`
	Favorite bool   `json:"favorite"`
	TokenInput
}

type document struct {
	Version int      `json:"version"`
	Tokens  []record `json:"tokens"`
}

// Store serializes all access, and publishes mutations only after their
// encrypted replacement file has successfully reached disk. Recomputable
// service defaults may be applied in memory when a startup migration cannot
// be persisted, so the existing vault remains usable.
type Store struct {
	mu      sync.RWMutex
	path    string
	records []record
	protect func([]byte) ([]byte, error)
	write   func(string, []byte) error
	now     func() time.Time
	warning string
}

// Open loads the current user's encrypted token vault. A missing file is a new
// empty vault; an unreadable, corrupt, or undecryptable file is never replaced.
func Open(dir string) (*Store, error) {
	return openWithProtection(dir, platform.Protect, platform.Unprotect)
}

func openWithProtection(dir string, protect, unprotect func([]byte) ([]byte, error)) (*Store, error) {
	return openWithStorage(dir, protect, unprotect, atomicWrite)
}

func openWithStorage(dir string, protect, unprotect func([]byte) ([]byte, error), write func(string, []byte) error) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("无法创建令牌库目录: %w", err)
	}
	store := &Store{path: filepath.Join(dir, vaultFileName), records: make([]record, 0), protect: protect, write: write, now: time.Now}
	file, err := os.Open(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("无法读取令牌库: %w", err)
	}
	defer file.Close()
	encrypted, err := io.ReadAll(io.LimitReader(file, maxVaultSize+1))
	if err != nil {
		return nil, fmt.Errorf("无法读取令牌库: %w", err)
	}
	// Release the original handle before an atomic migration replaces the file
	// on Windows. All validation still completes before any write is attempted.
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("无法读取令牌库: %w", err)
	}
	if len(encrypted) > maxVaultSize || len(encrypted) <= len(vaultMagic) || !bytes.HasPrefix(encrypted, []byte(vaultMagic)) {
		return nil, ErrInvalidVault
	}
	plain, err := unprotect(encrypted[len(vaultMagic):])
	defer clear(plain)
	if err != nil || len(plain) > maxVaultSize {
		return nil, ErrInvalidVault
	}
	var saved document
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil || saved.Version != 1 || saved.Tokens == nil || len(saved.Tokens) > maxTokens {
		return nil, ErrInvalidVault
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, ErrInvalidVault
	}
	ids := make(map[string]bool, len(saved.Tokens))
	identities := make(map[string]bool, len(saved.Tokens))
	for i := range saved.Tokens {
		r := &saved.Tokens[i]
		id, idErr := hex.DecodeString(r.ID)
		if idErr != nil || len(id) != 16 || hex.EncodeToString(id) != r.ID || ids[r.ID] {
			return nil, ErrInvalidVault
		}
		input, err := normalize(r.TokenInput)
		if err != nil || input != r.TokenInput {
			return nil, ErrInvalidVault
		}
		r.TokenInput = input
		key := identity(input)
		if identities[key] {
			return nil, ErrInvalidVault
		}
		ids[r.ID] = true
		identities[key] = true
	}
	migrated := false
	for i := range saved.Tokens {
		input := withDefaultGroup(saved.Tokens[i].TokenInput)
		if input != saved.Tokens[i].TokenInput {
			saved.Tokens[i].TokenInput = input
			migrated = true
		}
	}
	if migrated {
		if err := store.save(saved.Tokens); err != nil {
			store.warning = "自动分组暂未保存，原令牌库已保留；令牌可以继续使用，下次保存时会重试。"
		}
	}
	store.records = saved.Tokens
	return store, nil
}

func (s *Store) Warning() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.warning
}

func (s *Store) List() ([]Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return tokensAt(s.records, s.now().Unix())
}

// Account returns only the selected account metadata, without calculating a
// code or exposing the stored secret to the caller.
func (s *Store) Account(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	index := s.index(id)
	if index < 0 {
		return "", ErrNotFound
	}
	return s.records[index].Account, nil
}

func (s *Store) Add(input TokenInput) (Token, error) {
	input, err := normalize(input)
	if err != nil {
		return Token{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	added, err := s.addInputs([]TokenInput{input})
	if err != nil {
		return Token{}, err
	}
	return added[0], nil
}

func (s *Store) ImportURI(uri string) (Token, error) {
	chunks, err := ParseImportURIs([]string{uri}, true)
	if err != nil {
		return Token{}, err
	}
	if len(chunks) != 1 || len(chunks[0].Inputs) != 1 {
		return Token{}, errors.New("此二维码包含多个令牌，请使用批量导入")
	}
	return s.Add(chunks[0].Inputs[0])
}

// ImportURIs imports a whole QR batch or nothing. Invalid inputs and duplicates
// (against the vault or within the batch) leave both disk and memory untouched.
func (s *Store) ImportURIs(uris []string) ([]Token, error) {
	chunks, err := ParseImportURIs(uris, true)
	if err != nil {
		return nil, err
	}
	inputs := make([]TokenInput, 0)
	for _, chunk := range chunks {
		inputs = append(inputs, chunk.Inputs...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addInputs(inputs)
}

// addInputs requires the write lock and already-normalized inputs.
func (s *Store) addInputs(inputs []TokenInput) ([]Token, error) {
	if len(s.records)+len(inputs) > maxTokens {
		return nil, errors.New("令牌数量已达到上限")
	}
	seen := make(map[string]bool, len(s.records)+len(inputs))
	for _, existing := range s.records {
		seen[identity(existing.TokenInput)] = true
	}
	added := make([]record, 0, len(inputs))
	for _, input := range inputs {
		input = withDefaultGroup(input)
		key := identity(input)
		if seen[key] {
			return nil, ErrDuplicate
		}
		seen[key] = true
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, errors.New("无法安全创建令牌标识，请稍后重试")
		}
		added = append(added, record{ID: hex.EncodeToString(id[:]), TokenInput: input})
	}
	result, err := tokensAt(added, s.now().Unix())
	if err != nil {
		return nil, err
	}
	next := append(append([]record(nil), s.records...), added...)
	if err := s.save(next); err != nil {
		return nil, err
	}
	s.records = next
	return result, nil
}

func (s *Store) Update(id string, input TokenInput) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.index(id)
	if index < 0 {
		return Token{}, ErrNotFound
	}
	if input.Secret == "" {
		input.Secret = s.records[index].Secret
	}
	input, err := normalize(input)
	if err != nil {
		return Token{}, err
	}
	input = withDefaultGroup(input)
	for i, existing := range s.records {
		if i != index && identity(existing.TokenInput) == identity(input) {
			return Token{}, ErrDuplicate
		}
	}
	next := append([]record(nil), s.records...)
	next[index].TokenInput = input
	result, err := tokensAt(next[index:index+1], s.now().Unix())
	if err != nil {
		return Token{}, err
	}
	if err := s.save(next); err != nil {
		return Token{}, err
	}
	s.records = next
	return result[0], nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.index(id)
	if index < 0 {
		return ErrNotFound
	}
	next := make([]record, 0, len(s.records)-1)
	next = append(next, s.records[:index]...)
	next = append(next, s.records[index+1:]...)
	if err := s.save(next); err != nil {
		return err
	}
	s.records = next
	return nil
}

func (s *Store) ToggleFavorite(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.index(id)
	if index < 0 {
		return ErrNotFound
	}
	next := append([]record(nil), s.records...)
	next[index].Favorite = !next[index].Favorite
	if err := s.save(next); err != nil {
		return err
	}
	s.records = next
	return nil
}

func (s *Store) index(id string) int {
	for i := range s.records {
		if s.records[i].ID == id {
			return i
		}
	}
	return -1
}

func identity(input TokenInput) string {
	return fmt.Sprintf("%s/%s/%d/%d", input.Secret, input.Algorithm, input.Digits, input.Period)
}

func tokensAt(records []record, unixTime int64) ([]Token, error) {
	result := make([]Token, 0, len(records))
	for _, r := range records {
		code, remaining, err := codeAt(r.TokenInput, unixTime)
		if err != nil {
			return nil, err
		}
		result = append(result, Token{ID: r.ID, Issuer: r.Issuer, Account: r.Account, Group: r.Group, Favorite: r.Favorite, Color: r.Color, Algorithm: r.Algorithm, Digits: r.Digits, Period: r.Period, Code: code, Remaining: remaining})
	}
	return result, nil
}

func (s *Store) save(records []record) error {
	plain, err := json.Marshal(document{Version: 1, Tokens: records})
	if err != nil {
		return errors.New("无法保存令牌库")
	}
	defer clear(plain)
	encrypted, err := s.protect(plain)
	if err != nil {
		return errors.New("无法加密令牌库，未保存任何更改")
	}
	if len(encrypted)+len(vaultMagic) > maxVaultSize {
		return errors.New("令牌库过大，未保存任何更改")
	}
	if err := s.write(s.path, append([]byte(vaultMagic), encrypted...)); err != nil {
		return fmt.Errorf("无法写入令牌库，未保存任何更改: %w", err)
	}
	s.warning = ""
	return nil
}

func atomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".vault-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return replaceFile(tmp, path)
}
