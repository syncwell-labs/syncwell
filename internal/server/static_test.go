package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSafeSDKFSAllowListAndTraversal(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"syncwell.js":  "export {}",
		"types.d.ts":   "export type X = string",
		"package.json": "{}",
		".hidden.js":   "secret",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "hidden.js"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.js"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "secret.js"), filepath.Join(root, "linked.js")); err != nil && !os.IsPermission(err) {
		t.Fatal(err)
	}

	h := http.FileServer(SafeSDKFS(root))
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/syncwell.js", http.StatusOK},
		{"/types.d.ts", http.StatusOK},
		{"/package.json", http.StatusNotFound},
		{"/node_modules/hidden.js", http.StatusNotFound},
		{"/.hidden.js", http.StatusNotFound},
		{"/", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rr.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d", tc.path, rr.Code, tc.want)
			}
		})
	}
	if _, err := SafeSDKFS(root).Open("linked.js"); err == nil {
		t.Fatal("SDK filesystem followed a symlink")
	}
	if _, err := SafeSDKFS(root).Open("../secret.js"); err == nil {
		t.Fatal("SDK filesystem opened a traversal path")
	}
	guarded := RejectSDKTraversal(http.NotFoundHandler())
	for _, rawPath := range []string{"/sdk/../secret.js", "/sdk/%2e%2e/secret.js", "/sdk/a/%2f../secret.js"} {
		rr := httptest.NewRecorder()
		guarded.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, rawPath, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("guarded GET %s = %d, want 404", rawPath, rr.Code)
		}
	}
}

func TestNoDirListFSBlocksListingsDotfilesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"index.html", "plain.txt", ".hidden.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "with-index"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "with-index", "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "plain.txt"), filepath.Join(root, "linked.txt")); err != nil && !os.IsPermission(err) {
		t.Fatal(err)
	}

	h := http.FileServer(NoDirListFS(root))
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/", http.StatusOK},
		{"/plain.txt", http.StatusOK},
		{"/with-index/", http.StatusOK},
		{"/empty/", http.StatusNotFound},
		{"/.hidden.txt", http.StatusNotFound},
		{"/../secret", http.StatusNotFound},
		{"/linked.txt", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rr.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d", tc.path, rr.Code, tc.want)
			}
		})
	}
	if _, err := NoDirListFS(root).Open("empty"); err == nil {
		t.Fatal("static filesystem opened a directory without index.html")
	}
}
