// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package apiserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/juju/juju/state/api/params"
)

// backupHandler handles backup requests
type backupHandler struct {
	httpHandler
}

func (h *backupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h.authenticate(r); err != nil {
		h.authError(w, h)
		return
	}

	switch r.Method {
	case "POST":
		//filename = doBackup()
		filename := "example"
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		file, err := os.Open(filename)
		if err != nil {
			h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("backup failed"))
			return
		}
		io.Copy(w, file)
		// deleteBackup(filename)
	default:
		h.sendError(w, http.StatusMethodNotAllowed, fmt.Sprintf("unsupported method: %q", r.Method))
	}
}

// sendError sends a JSON-encoded error response.
func (h *backupHandler) sendError(w http.ResponseWriter, statusCode int, message string) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	body, err := json.Marshal(&params.BackupResponse{Error: message})
	if err != nil {
		return err
	}
	w.Write(body)
	return nil
}
