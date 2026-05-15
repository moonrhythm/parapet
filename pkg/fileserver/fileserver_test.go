package fileserver_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/fileserver"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFileServerServesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "hello.txt", "hi there")

	m := New(dir)
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/hello.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.False(t, called, "fallback should not run when file is served")
	assert.Equal(t, http.StatusOK, w.Code)
	body, _ := io.ReadAll(w.Body)
	assert.Equal(t, "hi there", string(body))
}

func TestFileServerFallbackOnMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	m := New(dir)
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("fallback"))
	}))

	r := httptest.NewRequest("GET", "/does-not-exist.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
	assert.Equal(t, http.StatusTeapot, w.Code)
	body, _ := io.ReadAll(w.Body)
	assert.Equal(t, "fallback", string(body))
}

func TestFileServerDirectoryListingDisabled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "sub/inside.txt", "x")

	m := New(dir)
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNotFound)
	}))

	r := httptest.NewRequest("GET", "/sub/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called, "directory should fall through to handler")
}

func TestFileServerRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	linkPath := filepath.Join(root, "escape.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), linkPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	m := New(root)
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNotFound)
	}))

	r := httptest.NewRequest("GET", "/escape.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called, "symlink escaping the root must fall through to the not-found handler")
}

func TestFileServerAllowsInternalSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	m := New(root)
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("fallback should not run")
	}))

	r := httptest.NewRequest("GET", "/link.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	body, _ := io.ReadAll(w.Body)
	assert.Equal(t, "hi", string(body))
}

func TestFileServerListDirectoryEnabled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "sub/inside.txt", "x")

	m := &FileServer{Root: dir, ListDirectory: true}
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("fallback should not be called")
	}))

	r := httptest.NewRequest("GET", "/sub/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	body, _ := io.ReadAll(w.Body)
	assert.Contains(t, string(body), "inside.txt")
}
