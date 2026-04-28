package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// testEnv groups everything a server test typically needs.
type testEnv struct {
	t        *testing.T
	store    *store.Store
	scanRoot string
	server   *Server
	httptest *httptest.Server
	client   *http.Client
}

func newTestServer(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	// Redirect the global config.toml to a per-test directory so server
	// startup (which runs migrateLegacyKVSettings + config.Load) cannot
	// read or write the user's real settings.
	t.Setenv("HOME", dir)            // macOS: ~/Library/Application Support/choix/...
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux fallback
	dbPath := filepath.Join(dir, ".choix", "state.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv, err := New(Config{
		Store:     st,
		ScanRoot:  dir,
		IdleAfter: time.Minute,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	hts := httptest.NewServer(srv.routes())
	t.Cleanup(hts.Close)

	return &testEnv{
		t:        t,
		store:    st,
		scanRoot: dir,
		server:   srv,
		httptest: hts,
		client:   hts.Client(),
	}
}

func (e *testEnv) get(path string) *http.Response {
	e.t.Helper()
	resp, err := e.client.Get(e.httptest.URL + path)
	if err != nil {
		e.t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func postJSON(t *testing.T, e *testEnv, path string, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := e.client.Post(e.httptest.URL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// seedFile inserts a minimal file row and returns its id.
func seedFile(t *testing.T, st *store.Store, path string) int64 {
	t.Helper()
	id, err := st.Files().Insert(store.File{
		Path: path, Size: 1, Mtime: time.Now().Unix(),
		ContentHash: "h-" + path, Kind: "photo", Format: "jpeg",
		ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	return id
}

// seedFileOnDisk creates the actual file on disk relative to scanRoot, inserts a DB row, and returns its id.
func seedFileOnDisk(t *testing.T, st *store.Store, scanRoot, path string) int64 {
	t.Helper()
	abs := filepath.Join(scanRoot, path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write stub file: %v", err)
	}
	id, err := st.Files().Insert(store.File{
		Path: path, Size: 4, Mtime: time.Now().Unix(),
		ContentHash: "h-" + path, Kind: "photo", Format: "jpeg",
		ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	return id
}

// seedCluster creates a cluster with the given members.
func seedCluster(t *testing.T, st *store.Store, deviceKey string, bucket int64, fileIDs []int64, _ int64) int64 {
	t.Helper()
	cid, err := st.Clusters().InsertCluster(store.Cluster{
		DeviceKey:  deviceKey,
		TimeBucket: sql.NullInt64{Int64: bucket, Valid: true},
	})
	if err != nil {
		t.Fatalf("clusters insert: %v", err)
	}
	for _, fid := range fileIDs {
		if err := st.Clusters().AddMember(cid, fid); err != nil {
			t.Fatalf("addmember: %v", err)
		}
	}
	return cid
}
