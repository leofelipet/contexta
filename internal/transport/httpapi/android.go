package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

type androidUpdate struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

func (h *handler) androidPath(name string) (string, bool) {
	if h.androidDistDir == "" || (name != "latest.json" && name != "app.apk") {
		return "", false
	}
	dir := filepath.Clean(h.androidDistDir)
	path := filepath.Join(dir, name)
	if filepath.Dir(path) != dir {
		return "", false
	}
	return path, true
}

func (h *handler) androidUpdate(w http.ResponseWriter, _ *http.Request) {
	path, ok := h.androidPath("latest.json")
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var update androidUpdate
	if err := json.Unmarshal(raw, &update); err != nil || update.VersionCode < 1 {
		writeError(w, http.StatusInternalServerError, "invalid android update metadata")
		return
	}
	writeJSON(w, http.StatusOK, update)
}

func (h *handler) androidApk(w http.ResponseWriter, r *http.Request) {
	path, ok := h.androidPath("app.apk")
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, err := os.Stat(path); err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	http.ServeFile(w, r, path)
}
