package fleetcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func Handler(store *Store) http.Handler {
	if store == nil {
		store = NewMemoryStore(DefaultWorkerTTL)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/v1/workers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workers": store.List()})
	})
	mux.HandleFunc("/v1/workers/register", func(w http.ResponseWriter, r *http.Request) {
		handleWorkerHeartbeat(w, r, store, VerbRegister)
	})
	mux.HandleFunc("/v1/workers/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		handleWorkerHeartbeat(w, r, store, VerbHeartbeat)
	})
	mux.HandleFunc("/v1/workers/", func(w http.ResponseWriter, r *http.Request) {
		handleWorker(w, r, store)
	})
	return mux
}

func handleWorkerHeartbeat(w http.ResponseWriter, r *http.Request, store *Store, verb string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var heartbeat WorkerHeartbeat
	if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode %s: %v", verb, err))
		return
	}
	record, err := store.UpsertHeartbeat(heartbeat)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func handleWorker(w http.ResponseWriter, r *http.Request, store *Store) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/workers/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "worker not found")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		record, ok := store.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "worker not found")
			return
		}
		writeJSON(w, http.StatusOK, record)
		return
	}
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "worker route not found")
		return
	}
	switch parts[1] {
	case "assignments":
		handleWorkerAssignments(w, r, store, id)
	case "reports":
		handleWorkerReports(w, r, store, id)
	default:
		writeError(w, http.StatusNotFound, "worker route not found")
	}
}

func handleWorkerAssignments(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	assignment, err := store.AwaitAssignment(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if assignment == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, assignment)
}

func handleWorkerReports(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var report WorkerReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode report-status: %v", err))
		return
	}
	if strings.TrimSpace(report.ID) == "" {
		report.ID = id
	} else if strings.TrimSpace(report.ID) != id {
		writeError(w, http.StatusBadRequest, "report worker id does not match path")
		return
	}
	record, err := store.Report(report)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
