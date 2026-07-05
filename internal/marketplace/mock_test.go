package marketplace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockRepo is a synthetic GitHub repo: a file set at a single fixed SHA.
type mockRepo struct {
	owner, repo string
	sha         string
	files       map[string]string // repo-relative path -> body
	execPaths   map[string]bool   // paths served with mode 100755
}

// newMockGitHub serves the GitHub API + raw endpoints the fetcher uses, backed by
// one or more mockRepos keyed by "owner/repo". It also builds zipballs on the fly.
func newMockGitHub(t *testing.T, repos ...*mockRepo) *httptest.Server {
	t.Helper()
	byKey := map[string]*mockRepo{}
	for _, r := range repos {
		byKey[r.owner+"/"+r.repo] = r
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)

	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") == "Bearer bad-token" {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSONTest(w, map[string]any{"message": "Bad credentials"})
			return
		}
		p := strings.Trim(req.URL.Path, "/")
		segs := strings.Split(p, "/")
		// API: /repos/{owner}/{repo}/...
		if len(segs) >= 3 && segs[0] == "repos" {
			owner, repo := segs[1], segs[2]
			r := byKey[owner+"/"+repo]
			if r == nil {
				http.NotFound(w, req)
				return
			}
			switch {
			case len(segs) == 3:
				writeJSONTest(w, map[string]string{"default_branch": "main"})
			case segs[3] == "commits":
				writeJSONTest(w, map[string]string{"sha": r.sha})
			case segs[3] == "git" && len(segs) >= 5 && segs[4] == "trees":
				writeJSONTest(w, r.treeResponse())
			case segs[3] == "zipball":
				w.Header().Set("Content-Type", "application/zip")
				_, _ = w.Write(r.zipball(t))
			default:
				http.NotFound(w, req)
			}
			return
		}
		// Raw: /{owner}/{repo}/{sha}/{path...}
		if len(segs) >= 4 {
			owner, repo := segs[0], segs[1]
			r := byKey[owner+"/"+repo]
			if r == nil {
				http.NotFound(w, req)
				return
			}
			rel := strings.Join(segs[3:], "/")
			body, ok := r.files[rel]
			if !ok {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, req)
	})
	t.Cleanup(srv.Close)
	return srv
}

func (r *mockRepo) treeResponse() map[string]any {
	var entries []map[string]any
	for path := range r.files {
		mode := "100644"
		if r.execPaths[path] {
			mode = "100755"
		}
		entries = append(entries, map[string]any{
			"path": path, "type": "blob", "size": len(r.files[path]), "mode": mode,
		})
	}
	return map[string]any{"tree": entries, "truncated": false}
}

func (r *mockRepo) zipball(t *testing.T) []byte {
	wrapper := r.repo + "-" + r.sha
	var entries []zipEntry
	for path, body := range r.files {
		e := zipEntry{name: path, body: body}
		if r.execPaths[path] {
			e.mode = 0o755
		}
		entries = append(entries, e)
	}
	return buildZipball(t, wrapper, entries)
}

func writeJSONTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
