package fleetcontrol

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
		if !reconcile(w, store) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workers": store.List()})
	})
	mux.HandleFunc("/v1/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		result, err := store.ReconcileActor(actorFromRequest(r, store))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("/v1/audit", func(w http.ResponseWriter, r *http.Request) {
		handleAudit(w, r, store)
	})
	mux.HandleFunc("/v1/service-accounts/", func(w http.ResponseWriter, r *http.Request) {
		handleServiceAccount(w, r, store)
	})
	mux.HandleFunc("/v1/service-accounts", func(w http.ResponseWriter, r *http.Request) {
		handleServiceAccounts(w, r, store)
	})
	mux.HandleFunc("/v1/storage/budget", func(w http.ResponseWriter, r *http.Request) {
		handleStorageBudget(w, r, store)
	})
	mux.HandleFunc("/v1/storage/prune", func(w http.ResponseWriter, r *http.Request) {
		handleStoragePrune(w, r, store)
	})
	mux.HandleFunc("/v1/images/gc", func(w http.ResponseWriter, r *http.Request) {
		handleImageGC(w, r, store)
	})
	mux.HandleFunc("/v1/images/prepare", func(w http.ResponseWriter, r *http.Request) {
		handleImagePrepare(w, r, store)
	})
	mux.HandleFunc("/v1/policies/lifecycle", func(w http.ResponseWriter, r *http.Request) {
		handleLifecyclePolicy(w, r, store)
	})
	mux.HandleFunc("/v1/placements/plan", func(w http.ResponseWriter, r *http.Request) {
		handlePlacementPlan(w, r, store)
	})
	mux.HandleFunc("/v1/warm-pools/claim", func(w http.ResponseWriter, r *http.Request) {
		handleWarmPoolClaim(w, r, store)
	})
	mux.HandleFunc("/v1/warm-pools/", func(w http.ResponseWriter, r *http.Request) {
		handleWarmPool(w, r, store)
	})
	mux.HandleFunc("/v1/warm-pools", func(w http.ResponseWriter, r *http.Request) {
		handleWarmPools(w, r, store)
	})
	mux.HandleFunc("/v1/workers/register", func(w http.ResponseWriter, r *http.Request) {
		handleWorkerHeartbeat(w, r, store, VerbRegister)
	})
	mux.HandleFunc("/v1/workers/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		handleWorkerHeartbeat(w, r, store, VerbHeartbeat)
	})
	mux.HandleFunc("/v1/assignments", func(w http.ResponseWriter, r *http.Request) {
		handleAssignments(w, r, store)
	})
	mux.HandleFunc("/v1/assignments/", func(w http.ResponseWriter, r *http.Request) {
		handleAssignment(w, r, store)
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

func handleAudit(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "audit limit must be non-negative")
			return
		}
		limit = n
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": store.ListAudit(limit)})
}

func handleServiceAccounts(w http.ResponseWriter, r *http.Request, store *Store) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"service_accounts": store.ListServiceAccounts()})
	case http.MethodPost:
		var req ServiceAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode service account: %v", err))
			return
		}
		result, err := store.UpsertServiceAccountActor(actorFromRequest(r, store), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleServiceAccount(w http.ResponseWriter, r *http.Request, store *Store) {
	name, err := nameFromPath(r.URL.Path, "/v1/service-accounts/", "service account")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch r.Method {
	case http.MethodDelete:
		result, err := store.DeleteServiceAccountActor(actorFromRequest(r, store), name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
		if !reconcile(w, store) {
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
	case "cordon":
		handleWorkerCordon(w, r, store, id)
	case "reports":
		handleWorkerReports(w, r, store, id)
	case "uncordon":
		handleWorkerUncordon(w, r, store, id)
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

func handleImagePrepare(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req ImagePrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode image prepare: %v", err))
		return
	}
	result, err := store.PrepareImageActor(actorFromRequest(r, store), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleImageGC(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req ImageGCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode image gc: %v", err))
		return
	}
	result, err := store.PushImageGCActor(actorFromRequest(r, store), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleLifecyclePolicy(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req LifecyclePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode lifecycle policy: %v", err))
		return
	}
	result, err := store.PushLifecyclePolicyActor(actorFromRequest(r, store), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleStorageBudget(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req StorageBudgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode storage budget: %v", err))
		return
	}
	result, err := store.PushStorageBudgetActor(actorFromRequest(r, store), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleStoragePrune(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req StoragePruneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode storage prune: %v", err))
		return
	}
	result, err := store.PushStoragePruneActor(actorFromRequest(r, store), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handlePlacementPlan(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req PlacementPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode placement plan: %v", err))
		return
	}
	plan, err := store.PlanAssignment(req.Assignment, req.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func handleWarmPools(w http.ResponseWriter, r *http.Request, store *Store) {
	switch r.Method {
	case http.MethodGet:
		if !reconcile(w, store) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"warm_pools": store.ListWarmPools()})
	case http.MethodPost:
		var req WarmPoolRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode warm pool: %v", err))
			return
		}
		result, err := store.EnsureWarmPoolActor(actorFromRequest(r, store), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleWarmPool(w http.ResponseWriter, r *http.Request, store *Store) {
	name, err := warmPoolNameFromPath(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !reconcile(w, store) {
			return
		}
		status, ok := store.GetWarmPool(name)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("warm pool %q not found", name))
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodDelete:
		result, err := store.DeleteWarmPoolActor(actorFromRequest(r, store), name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func warmPoolNameFromPath(path string) (string, error) {
	return nameFromPath(path, "/v1/warm-pools/", "warm pool")
}

func nameFromPath(path, prefix, label string) (string, error) {
	raw := strings.TrimPrefix(path, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return "", fmt.Errorf("%s name required", label)
	}
	name, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("decode %s name: %w", label, err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%s name required", label)
	}
	return name, nil
}

func actorFromRequest(r *http.Request, store *Store) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "bearer "
	if len(auth) >= len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		token := strings.TrimSpace(auth[len(prefix):])
		if account, ok := store.AuthenticateServiceAccount(token); ok {
			return "service-account:" + account.Name
		}
	}
	return "controller"
}

func handleWarmPoolClaim(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req WarmPoolClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode warm pool claim: %v", err))
		return
	}
	result, err := store.ClaimWarmPoolActor(actorFromRequest(r, store), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleWorkerCordon(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var lifecycle WorkerLifecycle
	if err := json.NewDecoder(r.Body).Decode(&lifecycle); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode worker lifecycle: %v", err))
		return
	}
	record, err := store.CordonWorkerActor(actorFromRequest(r, store), id, lifecycle.Reason)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func handleWorkerUncordon(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	record, err := store.UncordonWorkerActor(actorFromRequest(r, store), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func handleAssignments(w http.ResponseWriter, r *http.Request, store *Store) {
	switch r.Method {
	case http.MethodGet:
		if !reconcile(w, store) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"assignments": store.ListAssignments()})
	case http.MethodPost:
		var assignment Assignment
		if err := json.NewDecoder(r.Body).Decode(&assignment); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assignment: %v", err))
			return
		}
		created, err := store.CreateAssignmentActor(actorFromRequest(r, store), assignment)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, created)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleAssignment(w http.ResponseWriter, r *http.Request, store *Store) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/assignments/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !reconcile(w, store) {
		return
	}
	assignment, ok := store.GetAssignment(id)
	if !ok {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	writeJSON(w, http.StatusOK, assignment)
}

func reconcile(w http.ResponseWriter, store *Store) bool {
	if _, err := store.Reconcile(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
