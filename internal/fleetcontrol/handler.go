package fleetcontrol

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		if !requireUnscoped(w, r, store) {
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
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		result, err := store.ReconcileActor(actorFromRequest(r, store))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("/v1/audit/verify", func(w http.ResponseWriter, r *http.Request) {
		handleAuditVerify(w, r, store)
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
	mux.HandleFunc("/v1/sandboxes/", func(w http.ResponseWriter, r *http.Request) {
		handleSandbox(w, r, store)
	})
	mux.HandleFunc("/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		handleSandboxes(w, r, store)
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
	return rejectInvalidAuth(mux, store)
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
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	namespace := namespaceFilterFromRequest(r, identity)
	writeJSON(w, http.StatusOK, map[string]any{"events": store.ListAuditNamespace(limit, namespace)})
}

func handleAuditVerify(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	if !requireUnscoped(w, r, store) {
		return
	}
	writeJSON(w, http.StatusOK, store.VerifyAudit())
}

func handleServiceAccounts(w http.ResponseWriter, r *http.Request, store *Store) {
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"service_accounts": store.ListServiceAccountsNamespace(namespaceFilterFromRequest(r, identity))})
	case http.MethodPost:
		if !requireRole(w, identity, ServiceAccountRoleAdmin) {
			return
		}
		var req ServiceAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode service account: %v", err))
			return
		}
		if !applyScopedNamespace(w, identity, &req.Namespace) {
			return
		}
		result, err := store.UpsertServiceAccountActor(identity.Actor, req)
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
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodDelete:
		if !requireRole(w, identity, ServiceAccountRoleAdmin) {
			return
		}
		if identity.Scoped {
			account, ok := serviceAccountByName(store.ListServiceAccountsNamespace(identity.Namespace), name)
			if !ok {
				writeError(w, http.StatusNotFound, "service account not found")
				return
			}
			if !canAccessNamespace(identity, account.Namespace) {
				writeError(w, http.StatusForbidden, "namespace not allowed")
				return
			}
		}
		result, err := store.DeleteServiceAccountActor(identity.Actor, name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleSandboxes(w http.ResponseWriter, r *http.Request, store *Store) {
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		if !reconcile(w, store) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sandboxes": store.ListSandboxesNamespace(namespaceFilterFromRequest(r, identity))})
	case http.MethodPost:
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		var req SandboxRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode sandbox: %v", err))
			return
		}
		if !applyScopedNamespace(w, identity, &req.Namespace) {
			return
		}
		result, err := store.CreateSandboxActor(identity.Actor, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleSandbox(w http.ResponseWriter, r *http.Request, store *Store) {
	id, action, err := sandboxPath(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if action != "" {
		handleSandboxAction(w, r, store, id, action)
		return
	}
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		if !reconcile(w, store) {
			return
		}
		sandbox, ok := store.GetSandbox(id)
		if !ok || !canAccessNamespace(identity, sandbox.Namespace) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeJSON(w, http.StatusOK, sandbox)
	case http.MethodDelete:
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if identity.Scoped {
			sandbox, ok := store.GetSandbox(id)
			if !ok || !canAccessNamespace(identity, sandbox.Namespace) {
				writeError(w, http.StatusNotFound, "sandbox not found")
				return
			}
		}
		result, err := store.DeleteSandboxActor(identity.Actor, id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleSandboxAction(w http.ResponseWriter, r *http.Request, store *Store, id, action string) {
	identity := identityFromRequest(r, store)
	switch action {
	case "start":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !sandboxVisible(w, store, id, identity) {
			return
		}
		result, err := store.StartSandboxActor(identity.Actor, id)
		if err != nil {
			writeError(w, sandboxLifecycleErrorStatus(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "restart":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !sandboxVisible(w, store, id, identity) {
			return
		}
		result, err := store.RestartSandboxActor(identity.Actor, id)
		if err != nil {
			writeError(w, sandboxLifecycleErrorStatus(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "stop":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if identity.Scoped {
			sandbox, ok := store.GetSandbox(id)
			if !ok || !canAccessNamespace(identity, sandbox.Namespace) {
				writeError(w, http.StatusNotFound, "sandbox not found")
				return
			}
		}
		result, err := store.StopSandboxActor(identity.Actor, id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "wait":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		result, ok, err := waitSandbox(r, store, id, identity)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "lease":
		handleSandboxLease(w, r, store, id)
	default:
		writeError(w, http.StatusNotFound, "sandbox route not found")
	}
}

func handleSandboxLease(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodPost:
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !sandboxVisible(w, store, id, identity) {
			return
		}
		var req SandboxLeaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode sandbox lease: %v", err))
			return
		}
		result, err := store.LeaseSandboxActor(identity.Actor, id, req)
		if err != nil {
			writeError(w, sandboxLeaseErrorStatus(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodDelete:
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !sandboxVisible(w, store, id, identity) {
			return
		}
		holder := strings.TrimSpace(r.URL.Query().Get("holder"))
		var req SandboxLeaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode sandbox lease: %v", err))
			return
		}
		if holder == "" {
			holder = req.Holder
		}
		result, err := store.ReleaseSandboxLeaseActor(identity.Actor, id, holder)
		if err != nil {
			writeError(w, sandboxLeaseErrorStatus(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func sandboxVisible(w http.ResponseWriter, store *Store, id string, identity requestIdentity) bool {
	if !identity.Scoped {
		return true
	}
	sandbox, ok := store.GetSandbox(id)
	if !ok || !canAccessNamespace(identity, sandbox.Namespace) {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return false
	}
	return true
}

func sandboxLeaseErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	msg := err.Error()
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "lease held by") {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func sandboxLifecycleErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if strings.Contains(err.Error(), "not found") {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func sandboxPath(path string) (id, action string, err error) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/sandboxes/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		return "", "", fmt.Errorf("sandbox name required")
	}
	id, err = url.PathUnescape(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("decode sandbox name: %w", err)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("sandbox name required")
	}
	if len(parts) == 2 {
		action = strings.TrimSpace(parts[1])
		if action == "" {
			return "", "", fmt.Errorf("sandbox action required")
		}
	}
	return id, action, nil
}

func waitSandbox(r *http.Request, store *Store, id string, identity requestIdentity) (SandboxWaitResult, bool, error) {
	timeout := 30 * time.Second
	if raw := strings.TrimSpace(r.URL.Query().Get("timeout")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < 0 {
			return SandboxWaitResult{}, false, fmt.Errorf("sandbox wait timeout must be a non-negative duration")
		}
		timeout = parsed
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := store.WaitSandbox(id)
		if err != nil || !canAccessNamespace(identity, result.Sandbox.Namespace) {
			return SandboxWaitResult{}, false, nil
		}
		if result.Done || timeout == 0 {
			return result, true, nil
		}
		select {
		case <-r.Context().Done():
			return result, true, nil
		case <-deadline.C:
			return result, true, nil
		case <-ticker.C:
		}
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
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		if !requireUnscoped(w, r, store) {
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
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		handleWorkerCordon(w, r, store, id)
	case "reports":
		handleWorkerReports(w, r, store, id)
	case "uncordon":
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
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
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleOperator) {
		return
	}
	if !applyScopedNamespace(w, identity, &req.Namespace) {
		return
	}
	result, err := store.PrepareImageActor(identity.Actor, req)
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
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleOperator) {
		return
	}
	if !applyScopedNamespace(w, identity, &req.Namespace) {
		return
	}
	result, err := store.PushImageGCActor(identity.Actor, req)
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
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleOperator) {
		return
	}
	if !applyScopedNamespace(w, identity, &req.Namespace) {
		return
	}
	result, err := store.PushLifecyclePolicyActor(identity.Actor, req)
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
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleOperator) {
		return
	}
	if !applyScopedNamespace(w, identity, &req.Namespace) {
		return
	}
	result, err := store.PushStorageBudgetActor(identity.Actor, req)
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
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleOperator) {
		return
	}
	if !applyScopedNamespace(w, identity, &req.Namespace) {
		return
	}
	result, err := store.PushStoragePruneActor(identity.Actor, req)
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
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	if !applyScopedNamespace(w, identity, &req.Assignment.Namespace) {
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
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		if !reconcile(w, store) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"warm_pools": store.ListWarmPoolsNamespace(namespaceFilterFromRequest(r, identity))})
	case http.MethodPost:
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		var req WarmPoolRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode warm pool: %v", err))
			return
		}
		if !applyScopedNamespace(w, identity, &req.Namespace) {
			return
		}
		result, err := store.EnsureWarmPoolActor(identity.Actor, req)
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
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		if !reconcile(w, store) {
			return
		}
		status, ok := store.GetWarmPool(name)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("warm pool %q not found", name))
			return
		}
		if !canAccessNamespace(identity, status.Namespace) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("warm pool %q not found", name))
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodDelete:
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		status, ok := store.GetWarmPool(name)
		if !ok || !canAccessNamespace(identity, status.Namespace) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("warm pool %q not found", name))
			return
		}
		result, err := store.DeleteWarmPoolActor(identity.Actor, name)
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

type requestIdentity struct {
	Actor     string
	Namespace string
	Role      string
	Scoped    bool
	Invalid   bool
}

func actorFromRequest(r *http.Request, store *Store) string {
	return identityFromRequest(r, store).Actor
}

func identityFromRequest(r *http.Request, store *Store) requestIdentity {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "bearer "
	if len(auth) >= len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		token := strings.TrimSpace(auth[len(prefix):])
		if account, ok := store.AuthenticateServiceAccount(token); ok {
			namespace := normalizeNamespace(account.Namespace)
			return requestIdentity{
				Actor:     "service-account:" + account.Name,
				Namespace: namespace,
				Role:      account.Role,
				Scoped:    namespace != "",
			}
		}
		return requestIdentity{Invalid: true}
	}
	return requestIdentity{Actor: "controller", Role: ServiceAccountRoleAdmin}
}

func rejectInvalidAuth(next http.Handler, store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if identityFromRequest(r, store).Invalid {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func namespaceFilterFromRequest(r *http.Request, identity requestIdentity) string {
	if identity.Scoped {
		return identity.Namespace
	}
	return normalizeNamespace(r.URL.Query().Get("namespace"))
}

func applyScopedNamespace(w http.ResponseWriter, identity requestIdentity, namespace *string) bool {
	current := normalizeNamespace(*namespace)
	if identity.Scoped {
		if current != "" && current != identity.Namespace {
			writeError(w, http.StatusForbidden, "namespace not allowed")
			return false
		}
		*namespace = identity.Namespace
		return true
	}
	*namespace = current
	return true
}

func requireRole(w http.ResponseWriter, identity requestIdentity, role string) bool {
	if serviceAccountRoleRank(identity.Role) < serviceAccountRoleRank(role) {
		writeError(w, http.StatusForbidden, "role not allowed")
		return false
	}
	return true
}

func serviceAccountRoleRank(role string) int {
	switch role {
	case ServiceAccountRoleViewer:
		return 1
	case ServiceAccountRoleOperator:
		return 2
	case ServiceAccountRoleAdmin:
		return 3
	default:
		return 0
	}
}

func canAccessNamespace(identity requestIdentity, namespace string) bool {
	return !identity.Scoped || normalizeNamespace(namespace) == identity.Namespace
}

func requireUnscoped(w http.ResponseWriter, r *http.Request, store *Store) bool {
	if identityFromRequest(r, store).Scoped {
		writeError(w, http.StatusForbidden, "namespace-scoped service account cannot access fleet-global endpoint")
		return false
	}
	return true
}

func serviceAccountByName(accounts []ServiceAccount, name string) (ServiceAccount, bool) {
	name = strings.TrimSpace(name)
	for _, account := range accounts {
		if account.Name == name {
			return account, true
		}
	}
	return ServiceAccount{}, false
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
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleOperator) {
		return
	}
	if !applyScopedNamespace(w, identity, &req.Namespace) {
		return
	}
	result, err := store.ClaimWarmPoolActor(identity.Actor, req)
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
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		if !reconcile(w, store) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"assignments": store.ListAssignmentsNamespace(namespaceFilterFromRequest(r, identity))})
	case http.MethodPost:
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		var assignment Assignment
		if err := json.NewDecoder(r.Body).Decode(&assignment); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assignment: %v", err))
			return
		}
		if !applyScopedNamespace(w, identity, &assignment.Namespace) {
			return
		}
		created, err := store.CreateAssignmentActor(identity.Actor, assignment)
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
	if !requireRole(w, identityFromRequest(r, store), ServiceAccountRoleViewer) {
		return
	}
	if !reconcile(w, store) {
		return
	}
	assignment, ok := store.GetAssignment(id)
	if !ok || !canAccessNamespace(identityFromRequest(r, store), assignment.Namespace) {
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
