package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB     *sql.DB
	cipher cipher.AEAD
	mu     sync.Mutex
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "credential.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, e := os.Stat(filepath.Join(dir, "yunx.db")); e == nil {
			return nil, errors.New("credential.key 缺失，请从完整备份恢复密钥，不能生成新密钥覆盖现有凭据")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		var f *os.File
		f, err = os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = f.Write(key)
			if e := f.Close(); err == nil {
				err = e
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("凭据密钥长度错误")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "yunx.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, cipher: aead}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS records(bucket TEXT NOT NULL,id TEXT NOT NULL,body BLOB NOT NULL,PRIMARY KEY(bucket,id));
 CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY AUTOINCREMENT,task_id TEXT NOT NULL,kind TEXT NOT NULL,message TEXT NOT NULL,created_at TEXT NOT NULL);
 PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(filepath.Join(dir, "yunx.db"), 0600)
	return s, nil
}
func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func now() string { return time.Now().UTC().Format(time.RFC3339) }
func (s *Store) seal(bucket, id string, b []byte) []byte {
	n := make([]byte, s.cipher.NonceSize())
	if _, err := rand.Read(n); err != nil {
		panic(err)
	}
	return s.cipher.Seal(n, n, b, []byte(bucket+"/"+id))
}
func (s *Store) open(bucket, id string, b []byte) ([]byte, error) {
	n := s.cipher.NonceSize()
	if len(b) < n {
		return nil, errors.New("加密记录损坏")
	}
	return s.cipher.Open(nil, b[:n], b[n:], []byte(bucket+"/"+id))
}
func (s *Store) Put(bucket, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO records(bucket,id,body) VALUES(?,?,?) ON CONFLICT(bucket,id) DO UPDATE SET body=excluded.body", bucket, id, s.seal(bucket, id, b))
	return err
}
func (s *Store) Get(bucket, id string, v any) (bool, error) {
	var b []byte
	err := s.DB.QueryRow("SELECT body FROM records WHERE bucket=? AND id=?", bucket, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	b, err = s.open(bucket, id, b)
	if err != nil {
		return false, fmt.Errorf("%s 记录无法解密，请恢复匹配的密钥", bucket)
	}
	return true, json.Unmarshal(b, v)
}
func (s *Store) List(bucket string) (map[string]json.RawMessage, error) {
	rows, err := s.DB.Query("SELECT id,body FROM records WHERE bucket=? ORDER BY id", bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var id string
		var b []byte
		if err = rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		b, err = s.open(bucket, id, b)
		if err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, rows.Err()
}
func (s *Store) Delete(bucket, id string) error {
	_, err := s.DB.Exec("DELETE FROM records WHERE bucket=? AND id=?", bucket, id)
	return err
}
func (s *Store) Event(taskID, kind, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.DB.Exec("INSERT INTO events(task_id,kind,message,created_at) VALUES(?,?,?,?)", taskID, kind, message, now())
	_, _ = s.DB.Exec("DELETE FROM events WHERE id NOT IN (SELECT id FROM events ORDER BY id DESC LIMIT 1000)")
}

type TaskEvent struct {
	ID        int64  `json:"id"`
	TaskID    string `json:"task_id"`
	Kind      string `json:"kind"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) Events() ([]TaskEvent, error) {
	rows, err := s.DB.Query("SELECT id,task_id,kind,message,created_at FROM events ORDER BY id DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var x TaskEvent
		if err = rows.Scan(&x.ID, &x.TaskID, &x.Kind, &x.Message, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type Settings struct {
	Name            string `json:"name"`
	Concurrency     int    `json:"concurrency"`
	Connections     int    `json:"connections"`
	SpeedLimit      int64  `json:"speed_limit"`
	DefaultDir      string `json:"default_dir"`
	ResumeOnRestart bool   `json:"resume_on_restart"`
	Theme           string `json:"theme"`
}

func DefaultSettings() Settings { return Settings{"云析", 2, 4, 0, "", true, "auto"} }
func (s *Store) Settings() (Settings, error) {
	v := DefaultSettings()
	_, err := s.Get("config", "settings", &v)
	return v, err
}

type Account struct {
	ID          string            `json:"id"`
	Platform    string            `json:"platform"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Message     string            `json:"message,omitempty"`
	Nickname    string            `json:"nickname,omitempty"`
	CheckedAt   string            `json:"checked_at,omitempty"`
	UpdatedAt   string            `json:"updated_at"`
	Credentials map[string]string `json:"credentials,omitempty"`
}

func (a Account) Public() Account { a.Credentials = nil; return a }

type Item struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Size      int64             `json:"size"`
	Directory bool              `json:"directory"`
	Extra     map[string]string `json:"extra,omitempty"`
}
type ParseSession struct {
	Qualities []Quality         `json:"qualities,omitempty"`
	Quality   int               `json:"quality,omitempty"`
	Notice    string            `json:"notice,omitempty"`
	ID        string            `json:"id"`
	Platform  string            `json:"platform"`
	Title     string            `json:"title"`
	Input     string            `json:"input"`
	Password  string            `json:"password,omitempty"`
	AccountID string            `json:"account_id,omitempty"`
	ShareID   string            `json:"share_id,omitempty"`
	Token     string            `json:"token,omitempty"`
	Cloud     bool              `json:"cloud"`
	CreatedAt string            `json:"created_at"`
	Items     map[string]Item   `json:"items"`
	Cursors   map[string]string `json:"cursors,omitempty"`
}
type MediaSource struct {
	Kind         string            `json:"kind,omitempty"`
	Cleanup      string            `json:"cleanup,omitempty"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers,omitempty"`
	Size         int64             `json:"size"`
	ETag         string            `json:"etag,omitempty"`
	LastModified string            `json:"last_modified,omitempty"`
}
type Task struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Platform    string        `json:"platform"`
	AccountID   string        `json:"account_id,omitempty"`
	Status      string        `json:"status"`
	Size        int64         `json:"size"`
	Downloaded  int64         `json:"downloaded"`
	Speed       int64         `json:"speed"`
	Directory   string        `json:"directory"`
	Error       string        `json:"error,omitempty"`
	ErrorCode   string        `json:"error_code,omitempty"`
	CreatedAt   string        `json:"created_at"`
	UpdatedAt   string        `json:"updated_at"`
	FinishedAt  string        `json:"finished_at,omitempty"`
	Session     *ParseSession `json:"session,omitempty"`
	Item        Item          `json:"item,omitempty"`
	Sources     []MediaSource `json:"sources,omitempty"`
	EngineIDs   []string      `json:"engine_ids,omitempty"`
	TransferKey string        `json:"transfer_key,omitempty"`
	PartDone    []bool        `json:"part_done,omitempty"`
	HasFile     bool          `json:"has_file"`
}

func (t Task) Public() Task {
	t.Session = nil
	t.Item = Item{}
	t.Sources = nil
	t.EngineIDs = nil
	t.TransferKey = ""
	t.PartDone = nil
	return t
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *APIError) Error() string         { return e.Message }
func fail(code, message string) *APIError { return &APIError{code, message, 400} }
