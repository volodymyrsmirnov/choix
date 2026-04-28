package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

type picksRequest struct {
	FileID int64  `json:"file_id"`
	Action string `json:"action"` // pick | unpick | reject | unreject
}

type picksResponse struct {
	FileID       int64  `json:"file_id"`
	State        string `json:"state"` // picked | rejected | unmarked
	ExportedPath string `json:"exported_path,omitempty"`
}

func (s *Server) handlePicksPost(w http.ResponseWriter, r *http.Request) {
	var req picksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.FileID == 0 {
		http.Error(w, "file_id required", http.StatusBadRequest)
		return
	}
	if _, err := s.cfg.Store.Files().GetByID(req.FileID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	svc := picks.New(s.cfg.Store, s.cfg.ScanRoot, s.effectivePicksDir())
	var (
		state string
		exp   string
		err   error
	)
	switch req.Action {
	case "pick":
		err = svc.Pick(req.FileID)
		if err == nil {
			p, _ := s.cfg.Store.Picks().GetByFileID(req.FileID)
			if p.ExportedPath.Valid {
				exp = p.ExportedPath.String
			}
		}
		state = "picked"
	case "unpick":
		err = svc.Unpick(req.FileID)
		state = "unmarked"
	case "reject":
		err = svc.Reject(req.FileID)
		state = "rejected"
	case "unreject":
		err = svc.Unreject(req.FileID)
		state = "unmarked"
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(picksResponse{
		FileID: req.FileID, State: state, ExportedPath: exp,
	})
}

func (s *Server) handlePicksList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.cfg.Store.Picks().All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]picksResponse, 0, len(rows))
	for _, p := range rows {
		exp := ""
		if p.ExportedPath.Valid {
			exp = p.ExportedPath.String
		}
		out = append(out, picksResponse{
			FileID: p.FileID, State: p.State, ExportedPath: exp,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"picks": out})
}
