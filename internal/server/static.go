package server

import (
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// RejectSDKTraversal preserves the SDK mount boundary before ServeMux and
// FileServer canonicalize dot segments into a different request path.
func RejectSDKTraversal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped := r.URL.EscapedPath()
		parts := strings.Split(strings.TrimPrefix(escaped, "/"), "/")
		if len(parts) > 0 && parts[0] == "sdk" {
			for _, raw := range parts[1:] {
				decoded, err := url.PathUnescape(raw)
				if err != nil || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\") {
					http.NotFound(w, r)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

var allowedSDKExtensions = map[string]bool{
	".js":  true,
	".mjs": true,
	".cjs": true,
	".map": true,
	".css": true,
}

type safeSDKFS struct{ root string }

// SafeSDKFS serves only regular, non-symlink files directly under dir whose
// suffix is in the SDK runtime allow-list. Directory traversal is never served.
func SafeSDKFS(dir string) http.FileSystem {
	return safeSDKFS{root: dir}
}

func (s safeSDKFS) Open(name string) (http.File, error) {
	rel, ok := cleanStaticPath(name)
	if !ok || rel == "." || strings.Contains(rel, "/") {
		return nil, os.ErrNotExist
	}
	if !allowedSDKExtensions[path.Ext(rel)] && !strings.HasSuffix(rel, ".d.ts") {
		return nil, os.ErrNotExist
	}
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	return os.Open(full)
}

type noDirListFS struct{ root string }

// NoDirListFS serves static files below dir, rejects dotfiles and symlinks,
// and exposes a directory only when it contains a regular index.html file.
func NoDirListFS(dir string) http.FileSystem {
	return noDirListFS{root: dir}
}

func (n noDirListFS) Open(name string) (http.File, error) {
	rel, ok := cleanStaticPath(name)
	if !ok {
		return nil, os.ErrNotExist
	}
	full := n.root
	rootInfo, err := os.Lstat(full)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, os.ErrNotExist
	}
	if rel != "." {
		for _, component := range strings.Split(rel, "/") {
			full = filepath.Join(full, filepath.FromSlash(component))
			info, err := os.Lstat(full)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return nil, os.ErrNotExist
			}
		}
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		indexPath := filepath.Join(full, "index.html")
		indexInfo, err := os.Lstat(indexPath)
		if err != nil || indexInfo.Mode()&os.ModeSymlink != 0 || !indexInfo.Mode().IsRegular() {
			return nil, os.ErrNotExist
		}
	}
	return os.Open(full)
}

func cleanStaticPath(name string) (string, bool) {
	if strings.Contains(name, "\\") {
		return "", false
	}
	trimmed := strings.TrimLeft(name, "/")
	clean := path.Clean(trimmed)
	if clean == "." {
		return clean, true
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", false
	}
	for _, component := range strings.Split(clean, "/") {
		if component == ".." || strings.HasPrefix(component, ".") {
			return "", false
		}
	}
	return clean, true
}
