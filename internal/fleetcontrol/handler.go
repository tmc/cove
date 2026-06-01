package fleetcontrol

import (
	"context"
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
		filter, err := workerListFilterFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, store.ListWorkersPage(filter))
	})
	mux.HandleFunc("/v1/reconcile/plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
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
		writeJSON(w, http.StatusOK, store.ReconcilePlan())
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
	mux.HandleFunc("/v1/oidc-bindings/", func(w http.ResponseWriter, r *http.Request) {
		handleOIDCBinding(w, r, store)
	})
	mux.HandleFunc("/v1/oidc-bindings", func(w http.ResponseWriter, r *http.Request) {
		handleOIDCBindings(w, r, store)
	})
	mux.HandleFunc("/v1/saml/acs", func(w http.ResponseWriter, r *http.Request) {
		handleSAMLACS(w, r, store)
	})
	mux.HandleFunc("/v1/saml-bindings/", func(w http.ResponseWriter, r *http.Request) {
		handleSAMLBinding(w, r, store)
	})
	mux.HandleFunc("/v1/saml-bindings", func(w http.ResponseWriter, r *http.Request) {
		handleSAMLBindings(w, r, store)
	})
	mux.HandleFunc("/v1/operations/summary", func(w http.ResponseWriter, r *http.Request) {
		handleOperationsSummary(w, r, store)
	})
	mux.HandleFunc("/v1/operations/runs/", func(w http.ResponseWriter, r *http.Request) {
		handleControllerRun(w, r, store)
	})
	mux.HandleFunc("/v1/operations/runs", func(w http.ResponseWriter, r *http.Request) {
		handleControllerRuns(w, r, store)
	})
	mux.HandleFunc("/v1/metering/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		handleSandboxMetering(w, r, store)
	})
	mux.HandleFunc("/v1/sandboxes/", func(w http.ResponseWriter, r *http.Request) {
		handleSandbox(w, r, store)
	})
	mux.HandleFunc("/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		handleSandboxes(w, r, store)
	})
	mux.HandleFunc("/v1/storage/budget/runs/", func(w http.ResponseWriter, r *http.Request) {
		handleStorageBudgetRun(w, r, store)
	})
	mux.HandleFunc("/v1/storage/budget/runs", func(w http.ResponseWriter, r *http.Request) {
		handleStorageBudgetRuns(w, r, store)
	})
	mux.HandleFunc("/v1/storage/budget", func(w http.ResponseWriter, r *http.Request) {
		handleStorageBudget(w, r, store)
	})
	mux.HandleFunc("/v1/storage/prune/runs/", func(w http.ResponseWriter, r *http.Request) {
		handleStoragePruneRun(w, r, store)
	})
	mux.HandleFunc("/v1/storage/prune/runs", func(w http.ResponseWriter, r *http.Request) {
		handleStoragePruneRuns(w, r, store)
	})
	mux.HandleFunc("/v1/storage/prune", func(w http.ResponseWriter, r *http.Request) {
		handleStoragePrune(w, r, store)
	})
	mux.HandleFunc("/v1/images/gc/runs/", func(w http.ResponseWriter, r *http.Request) {
		handleImageGCRun(w, r, store)
	})
	mux.HandleFunc("/v1/images/gc/runs", func(w http.ResponseWriter, r *http.Request) {
		handleImageGCRuns(w, r, store)
	})
	mux.HandleFunc("/v1/images/gc", func(w http.ResponseWriter, r *http.Request) {
		handleImageGC(w, r, store)
	})
	mux.HandleFunc("/v1/images/preparations/", func(w http.ResponseWriter, r *http.Request) {
		handleImagePreparation(w, r, store)
	})
	mux.HandleFunc("/v1/images/preparations", func(w http.ResponseWriter, r *http.Request) {
		handleImagePreparations(w, r, store)
	})
	mux.HandleFunc("/v1/images/prepare", func(w http.ResponseWriter, r *http.Request) {
		handleImagePrepare(w, r, store)
	})
	mux.HandleFunc("/v1/policies/lifecycle/runs/", func(w http.ResponseWriter, r *http.Request) {
		handleLifecyclePolicyRun(w, r, store)
	})
	mux.HandleFunc("/v1/policies/lifecycle/runs", func(w http.ResponseWriter, r *http.Request) {
		handleLifecyclePolicyRuns(w, r, store)
	})
	mux.HandleFunc("/v1/policies/lifecycle", func(w http.ResponseWriter, r *http.Request) {
		handleLifecyclePolicy(w, r, store)
	})
	mux.HandleFunc("/v1/placements/plans/", func(w http.ResponseWriter, r *http.Request) {
		handlePlacementPlanRecord(w, r, store)
	})
	mux.HandleFunc("/v1/placements/plans", func(w http.ResponseWriter, r *http.Request) {
		handlePlacementPlans(w, r, store)
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

func workerListFilterFromRequest(r *http.Request) (WorkerListFilter, error) {
	query := r.URL.Query()
	manifestDigest := strings.TrimSpace(query.Get("source_manifest_digest"))
	if manifestDigest == "" {
		manifestDigest = strings.TrimSpace(query.Get("image_manifest_digest"))
	}
	labels, err := labelFiltersFromQuery(query["label"])
	if err != nil {
		return WorkerListFilter{}, err
	}
	filter := WorkerListFilter{
		Status:               strings.TrimSpace(query.Get("status")),
		Host:                 strings.TrimSpace(query.Get("host")),
		Version:              strings.TrimSpace(query.Get("version")),
		ImageRef:             strings.TrimSpace(query.Get("image_ref")),
		SourceManifestDigest: manifestDigest,
		Labels:               labels,
		Capabilities:         sortedUniqueStrings(query["capability"]),
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return WorkerListFilter{}, fmt.Errorf("worker limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return WorkerListFilter{}, fmt.Errorf("worker offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func labelFiltersFromQuery(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	labels := make(map[string]string)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if !ok || key == "" {
			return nil, fmt.Errorf("worker label filter must be key=value")
		}
		labels[key] = val
	}
	if len(labels) == 0 {
		return nil, nil
	}
	return labels, nil
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

func handleOperationsSummary(w http.ResponseWriter, r *http.Request, store *Store) {
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
	writeJSON(w, http.StatusOK, store.OperationsSummary(namespaceFilterFromRequest(r, identity)))
}

func handleControllerRuns(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	filter, err := controllerRunListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListControllerRunsPage(filter))
}

func handleControllerRun(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := nameFromPath(r.URL.Path, "/v1/operations/runs/", "controller run")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	detail, ok := store.GetControllerRun(id)
	if !ok || !canAccessNamespace(identity, detail.Summary.Namespace) {
		writeError(w, http.StatusNotFound, "controller run not found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func controllerRunListFilterFromRequest(r *http.Request, namespace string) (ControllerRunListFilter, error) {
	filter := ControllerRunListFilter{
		Namespace:           namespace,
		Kind:                strings.TrimSpace(r.URL.Query().Get("kind")),
		TargetType:          strings.TrimSpace(r.URL.Query().Get("target_type")),
		TargetID:            strings.TrimSpace(r.URL.Query().Get("target_id")),
		SourceRef:           strings.TrimSpace(r.URL.Query().Get("source_ref")),
		ImageRef:            strings.TrimSpace(r.URL.Query().Get("image_ref")),
		ImageManifestDigest: strings.TrimSpace(r.URL.Query().Get("image_manifest_digest")),
		ImageDigestRef:      strings.TrimSpace(r.URL.Query().Get("image_digest_ref")),
		ImagePlatform:       strings.TrimSpace(r.URL.Query().Get("image_platform")),
		RequiredCapability:  strings.TrimSpace(r.URL.Query().Get("required_capability")),
		AssignmentID:        strings.TrimSpace(r.URL.Query().Get("assignment_id")),
		AssignmentStatus:    strings.TrimSpace(r.URL.Query().Get("assignment_status")),
		WorkerID:            strings.TrimSpace(r.URL.Query().Get("worker_id")),
		CandidateWorkerID:   strings.TrimSpace(r.URL.Query().Get("candidate_worker_id")),
		SkippedWorkerID:     strings.TrimSpace(r.URL.Query().Get("skipped_worker_id")),
		SkipReason:          strings.TrimSpace(r.URL.Query().Get("skip_reason")),
	}
	hasActiveAssignments, err := boolFilterFromRequest(r, "controller runs", "has_active_assignments")
	if err != nil {
		return ControllerRunListFilter{}, err
	}
	filter.HasActiveAssignments = hasActiveAssignments
	hasSkips, err := boolFilterFromRequest(r, "controller runs", "has_skips")
	if err != nil {
		return ControllerRunListFilter{}, err
	}
	filter.HasSkips = hasSkips
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return ControllerRunListFilter{}, fmt.Errorf("controller runs limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return ControllerRunListFilter{}, fmt.Errorf("controller runs offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func handleAudit(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	namespace := namespaceFilterFromRequest(r, identity)
	filter, err := auditListFilterFromRequest(r, namespace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListAuditPage(filter))
}

func auditListFilterFromRequest(r *http.Request, namespace string) (AuditListFilter, error) {
	filter := AuditListFilter{
		Namespace:    namespace,
		Actor:        strings.TrimSpace(r.URL.Query().Get("actor")),
		Action:       strings.TrimSpace(r.URL.Query().Get("action")),
		TargetType:   strings.TrimSpace(r.URL.Query().Get("target_type")),
		TargetID:     strings.TrimSpace(r.URL.Query().Get("target_id")),
		WorkerID:     strings.TrimSpace(r.URL.Query().Get("worker_id")),
		AssignmentID: strings.TrimSpace(r.URL.Query().Get("assignment_id")),
		SandboxID:    strings.TrimSpace(r.URL.Query().Get("sandbox_id")),
	}
	if filter.SandboxID == "" {
		filter.SandboxID = strings.TrimSpace(r.URL.Query().Get("sandbox"))
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return AuditListFilter{}, fmt.Errorf("audit limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return AuditListFilter{}, fmt.Errorf("audit offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
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

func handleOIDCBindings(w http.ResponseWriter, r *http.Request, store *Store) {
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"oidc_bindings": store.ListOIDCBindingsNamespace(namespaceFilterFromRequest(r, identity))})
	case http.MethodPost:
		if !requireRole(w, identity, ServiceAccountRoleAdmin) {
			return
		}
		var req OIDCBindingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode oidc binding: %v", err))
			return
		}
		if !applyScopedNamespace(w, identity, &req.Namespace) {
			return
		}
		result, err := store.UpsertOIDCBindingActor(identity.Actor, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleOIDCBinding(w http.ResponseWriter, r *http.Request, store *Store) {
	name, err := nameFromPath(r.URL.Path, "/v1/oidc-bindings/", "oidc binding")
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
			binding, ok := oidcBindingByName(store.ListOIDCBindingsNamespace(identity.Namespace), name)
			if !ok {
				writeError(w, http.StatusNotFound, "oidc binding not found")
				return
			}
			if !canAccessNamespace(identity, binding.Namespace) {
				writeError(w, http.StatusForbidden, "namespace not allowed")
				return
			}
		}
		result, err := store.DeleteOIDCBindingActor(identity.Actor, name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleSAMLBindings(w http.ResponseWriter, r *http.Request, store *Store) {
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"saml_bindings": store.ListSAMLBindingsNamespace(namespaceFilterFromRequest(r, identity))})
	case http.MethodPost:
		if !requireRole(w, identity, ServiceAccountRoleAdmin) {
			return
		}
		var req SAMLBindingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode saml binding: %v", err))
			return
		}
		if !applyScopedNamespace(w, identity, &req.Namespace) {
			return
		}
		result, err := store.UpsertSAMLBindingActor(identity.Actor, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleSAMLACS(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	req, err := samlSessionRequestFromHTTP(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := store.CreateSAMLSession(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func samlSessionRequestFromHTTP(r *http.Request) (SAMLSessionRequest, error) {
	contentType := strings.ToLower(r.Header.Get("content-type"))
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			return SAMLSessionRequest{}, fmt.Errorf("decode saml acs form: %w", err)
		}
		return samlSessionRequestFromValues(r.PostForm), nil
	}
	if strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return SAMLSessionRequest{}, fmt.Errorf("decode saml acs form: %w", err)
		}
		return samlSessionRequestFromValues(r.PostForm), nil
	}
	var req SAMLSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return SAMLSessionRequest{}, fmt.Errorf("decode saml acs: %w", err)
	}
	return req, nil
}

func samlSessionRequestFromValues(values url.Values) SAMLSessionRequest {
	return SAMLSessionRequest{
		SAMLResponse:  values.Get("SAMLResponse"),
		SAMLAssertion: values.Get("SAMLAssertion"),
		RelayState:    values.Get("RelayState"),
		TTL:           firstValue(values, "ttl", "TTL"),
	}
}

func firstValue(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := values.Get(key); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func handleSAMLBinding(w http.ResponseWriter, r *http.Request, store *Store) {
	if name, ok, err := samlBindingSubresourcePath(r.URL.Path, "refresh"); ok || err != nil {
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		handleSAMLBindingRefresh(w, r, store, name)
		return
	}
	if name, ok, err := samlBindingSubresourcePath(r.URL.Path, "login"); ok || err != nil {
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		handleSAMLBindingLogin(w, r, store, name)
		return
	}
	if name, ok, err := samlBindingMetadataPath(r.URL.Path); ok || err != nil {
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		handleSAMLBindingMetadata(w, r, store, name)
		return
	}
	name, err := nameFromPath(r.URL.Path, "/v1/saml-bindings/", "saml binding")
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
			binding, ok := samlBindingByName(store.ListSAMLBindingsNamespace(identity.Namespace), name)
			if !ok {
				writeError(w, http.StatusNotFound, "saml binding not found")
				return
			}
			if !canAccessNamespace(identity, binding.Namespace) {
				writeError(w, http.StatusForbidden, "namespace not allowed")
				return
			}
		}
		result, err := store.DeleteSAMLBindingActor(identity.Actor, name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func samlBindingMetadataPath(path string) (string, bool, error) {
	return samlBindingSubresourcePath(path, "metadata")
}

func samlBindingSubresourcePath(path, subresource string) (string, bool, error) {
	raw := strings.TrimPrefix(path, "/v1/saml-bindings/")
	parts := strings.Split(raw, "/")
	if len(parts) != 2 || parts[1] != subresource {
		return "", false, nil
	}
	name, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", true, fmt.Errorf("decode saml binding name: %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", true, fmt.Errorf("saml binding name required")
	}
	return name, true, nil
}

func handleSAMLBindingRefresh(w http.ResponseWriter, r *http.Request, store *Store, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleAdmin) {
		return
	}
	if identity.Scoped {
		binding, ok := samlBindingByName(store.ListSAMLBindingsNamespace(identity.Namespace), name)
		if !ok {
			writeError(w, http.StatusNotFound, "saml binding not found")
			return
		}
		if !canAccessNamespace(identity, binding.Namespace) {
			writeError(w, http.StatusForbidden, "namespace not allowed")
			return
		}
	}
	result, err := store.RefreshSAMLBindingMetadataActor(identity.Actor, name)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleSAMLBindingLogin(w http.ResponseWriter, r *http.Request, store *Store, name string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	namespace := ""
	if identity.Scoped {
		namespace = identity.Namespace
	}
	result, err := store.SAMLAuthnRequestNamespace(name, namespace, firstValue(r.URL.Query(), "relay_state", "RelayState"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if redirectRequested(r.URL.Query()) {
		http.Redirect(w, r, result.RedirectURL, http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func redirectRequested(values url.Values) bool {
	raw := strings.TrimSpace(values.Get("redirect"))
	return raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes")
}

func handleSAMLBindingMetadata(w http.ResponseWriter, r *http.Request, store *Store, name string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	namespace := ""
	if identity.Scoped {
		namespace = identity.Namespace
	}
	metadata, err := store.SAMLMetadataNamespace(name, namespace)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("content-type", "application/samlmetadata+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(metadata)
}

func handleSandboxes(w http.ResponseWriter, r *http.Request, store *Store) {
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		filter, err := sandboxListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !reconcile(w, store) {
			return
		}
		writeJSON(w, http.StatusOK, store.ListSandboxesPage(filter))
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
		if err := resolveSandboxManifestBundle(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := store.CreateSandboxActor(identity.Actor, req)
		if err != nil {
			writeSandboxAdmissionError(w, store, req, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func sandboxListFilterFromRequest(r *http.Request, namespace string) (SandboxListFilter, error) {
	filter := SandboxListFilter{
		Namespace:           namespace,
		Status:              strings.TrimSpace(r.URL.Query().Get("status")),
		WorkerID:            strings.TrimSpace(r.URL.Query().Get("worker_id")),
		ImageRef:            strings.TrimSpace(r.URL.Query().Get("image_ref")),
		ImageManifestDigest: strings.TrimSpace(r.URL.Query().Get("image_manifest_digest")),
		ImageDigestRef:      strings.TrimSpace(r.URL.Query().Get("image_digest_ref")),
		ImagePlatform:       strings.TrimSpace(r.URL.Query().Get("image_platform")),
		RequiredCapability:  strings.TrimSpace(r.URL.Query().Get("required_capability")),
	}
	hasOpen, err := sandboxListBoolFilterFromRequest(r, "sandbox")
	if err != nil {
		return SandboxListFilter{}, err
	}
	filter.HasOpenAssignments = hasOpen
	retrying, err := sandboxRetryingFilterFromRequest(r, "sandbox")
	if err != nil {
		return SandboxListFilter{}, err
	}
	filter.Retrying = retrying
	hasCleanup, err := sandboxCleanupFilterFromRequest(r, "sandbox")
	if err != nil {
		return SandboxListFilter{}, err
	}
	filter.HasCleanup = hasCleanup
	hasLease, err := sandboxLeaseFilterFromRequest(r, "sandbox")
	if err != nil {
		return SandboxListFilter{}, err
	}
	filter.HasLease = hasLease
	filter.LeaseHolder = strings.TrimSpace(r.URL.Query().Get("lease_holder"))
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return SandboxListFilter{}, fmt.Errorf("sandbox limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return SandboxListFilter{}, fmt.Errorf("sandbox offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func writeSandboxAdmissionError(w http.ResponseWriter, store *Store, req SandboxRequest, err error) {
	if isSandboxCapAdmissionError(err) {
		active := activeSandboxAdmissionDiagnostics(store, req.Namespace)
		writeJSON(w, http.StatusBadRequest, SandboxAdmissionError{
			Error:              err.Error(),
			MaxActiveSandboxes: req.MaxActiveSandboxes,
			ActiveCount:        len(active),
			ActiveSandboxes:    active,
		})
		return
	}
	if !isPlacementAdmissionError(err) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, planErr := store.PlanAssignment(sandboxPlacementAssignment(req), DefaultPlacementPlanLimit)
	if planErr != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusBadRequest, SandboxAdmissionError{
		Error:         err.Error(),
		PlacementPlan: &plan,
	})
}

func isSandboxCapAdmissionError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "max_active_sandboxes")
}

func activeSandboxAdmissionDiagnostics(store *Store, namespace string) []SandboxStatus {
	list := store.ListSandboxesFiltered(SandboxListFilter{Namespace: namespace})
	active := make([]SandboxStatus, 0, len(list))
	for _, sandbox := range list {
		if !sandboxTerminalStatus(sandbox.Status) {
			active = append(active, sandbox)
		}
	}
	return active
}

func isPlacementAdmissionError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no ready worker matches assignment")
}

func sandboxPlacementAssignment(req SandboxRequest) Assignment {
	return Assignment{
		Namespace:            normalizeNamespace(req.Namespace),
		Policy:               strings.TrimSpace(req.Policy),
		ImageRef:             strings.TrimSpace(req.ImageRef),
		ImageManifestDigest:  strings.TrimSpace(req.ImageManifestDigest),
		ImageDigestRef:       strings.TrimSpace(req.ImageDigestRef),
		ImagePlatform:        strings.TrimSpace(req.ImagePlatform),
		RequiredLabels:       cloneLabels(req.RequiredLabels),
		RequiredCapabilities: sortedUniqueStrings(req.RequiredCapabilities),
		AntiAffinityKey:      strings.TrimSpace(req.AntiAffinityKey),
		Resources:            req.Resources,
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
		req, err := sandboxMutationRequestFromRequest(r, "delete")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := store.DeleteSandboxActor(identity.Actor, id, req)
		if err != nil {
			writeError(w, sandboxLifecycleErrorStatus(err), err.Error())
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
		req, err := sandboxMutationRequestFromRequest(r, "start")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := store.StartSandboxActor(identity.Actor, id, req)
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
		req, err := sandboxMutationRequestFromRequest(r, "restart")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := store.RestartSandboxActor(identity.Actor, id, req)
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
		req, err := sandboxMutationRequestFromRequest(r, "stop")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := store.StopSandboxActor(identity.Actor, id, req)
		if err != nil {
			writeError(w, sandboxLifecycleErrorStatus(err), err.Error())
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
	case "exec":
		handleSandboxExec(w, r, store, id, identity)
	case "control":
		handleSandboxControl(w, r, store, id, identity)
	case "metering":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		if !sandboxVisible(w, store, id, identity) {
			return
		}
		writeJSON(w, http.StatusOK, store.ListSandboxMetering(namespaceFilterFromRequest(r, identity), id))
	case "events":
		handleSandboxEvents(w, r, store, id, identity)
	case "reports":
		handleSandboxReports(w, r, store, id, identity)
	default:
		writeError(w, http.StatusNotFound, "sandbox route not found")
	}
}

func handleSandboxExec(w http.ResponseWriter, r *http.Request, store *Store, id string, identity requestIdentity) {
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
	var req SandboxExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode sandbox exec: %v", err))
		return
	}
	timeout, err := sandboxExecTimeout(r, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Timeout = sandboxRunTimeout(timeout)
	result, err := store.ExecSandboxActor(identity.Actor, id, req)
	if err != nil {
		writeError(w, sandboxLifecycleErrorStatus(err), err.Error())
		return
	}
	result, ok := waitSandboxExec(r, store, result, timeout)
	if !ok {
		writeError(w, http.StatusNotFound, "sandbox exec assignment not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleSandboxControl(w http.ResponseWriter, r *http.Request, store *Store, id string, identity requestIdentity) {
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
	var req SandboxControlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode sandbox control: %v", err))
		return
	}
	timeout, err := sandboxControlTimeout(r, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Timeout = sandboxRunTimeout(timeout)
	result, err := store.ControlSandboxActor(identity.Actor, id, req)
	if err != nil {
		writeError(w, sandboxLifecycleErrorStatus(err), err.Error())
		return
	}
	result, ok := waitSandboxControl(r, store, result, timeout)
	if !ok {
		writeError(w, http.StatusNotFound, "sandbox control assignment not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleSandboxEvents(w http.ResponseWriter, r *http.Request, store *Store, id string, identity requestIdentity) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	sandbox, ok := store.GetSandbox(id)
	if !ok || !canAccessNamespace(identity, sandbox.Namespace) {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	filter, err := sandboxEventsFilterFromRequest(r, sandbox.Namespace, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListSandboxEventsPage(sandbox.Namespace, id, filter))
}

func sandboxEventsFilterFromRequest(r *http.Request, namespace, id string) (AuditListFilter, error) {
	filter := AuditListFilter{
		Namespace: namespace,
		SandboxID: id,
		Actor:     strings.TrimSpace(r.URL.Query().Get("actor")),
		Action:    strings.TrimSpace(r.URL.Query().Get("action")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return AuditListFilter{}, fmt.Errorf("sandbox events limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return AuditListFilter{}, fmt.Errorf("sandbox events offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func handleSandboxReports(w http.ResponseWriter, r *http.Request, store *Store, id string, identity requestIdentity) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	sandbox, ok := store.GetSandbox(id)
	if !ok || !canAccessNamespace(identity, sandbox.Namespace) {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	filter, err := sandboxReportsFilterFromRequest(r, sandbox.Namespace, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListSandboxReportsPage(filter))
}

func sandboxReportsFilterFromRequest(r *http.Request, namespace, id string) (SandboxReportFilter, error) {
	filter := SandboxReportFilter{
		Namespace: namespace,
		SandboxID: id,
		Role:      strings.TrimSpace(r.URL.Query().Get("role")),
		Status:    strings.TrimSpace(r.URL.Query().Get("status")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return SandboxReportFilter{}, fmt.Errorf("sandbox reports limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return SandboxReportFilter{}, fmt.Errorf("sandbox reports offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func sandboxMutationRequestFromRequest(r *http.Request, action string) (SandboxMutationRequest, error) {
	var req SandboxMutationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		return SandboxMutationRequest{}, fmt.Errorf("decode sandbox %s: %v", action, err)
	}
	if holder := strings.TrimSpace(r.URL.Query().Get("holder")); holder != "" {
		req.Holder = holder
	}
	req.Holder = strings.TrimSpace(req.Holder)
	return req, nil
}

func handleSandboxMetering(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	namespace := namespaceFilterFromRequest(r, identity)
	sandboxID := strings.TrimSpace(r.URL.Query().Get("sandbox_id"))
	if sandboxID == "" {
		sandboxID = strings.TrimSpace(r.URL.Query().Get("sandbox"))
	}
	writeJSON(w, http.StatusOK, store.ListSandboxMetering(namespace, sandboxID))
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
	msg := err.Error()
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "lease held by") {
		return http.StatusConflict
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
	targetStatus := sandboxWaitTargetStatus(r)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := store.WaitSandboxStatus(id, targetStatus)
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

func sandboxWaitTargetStatus(r *http.Request) string {
	targetStatus := strings.TrimSpace(r.URL.Query().Get("target_status"))
	if targetStatus == "" {
		targetStatus = strings.TrimSpace(r.URL.Query().Get("status"))
	}
	return targetStatus
}

func sandboxExecTimeout(r *http.Request, req SandboxExecRequest) (time.Duration, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("timeout"))
	if raw == "" {
		raw = strings.TrimSpace(req.Timeout)
	}
	if raw == "" {
		return 30 * time.Second, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout < 0 {
		return 0, fmt.Errorf("sandbox exec timeout must be a non-negative duration")
	}
	return timeout, nil
}

func sandboxControlTimeout(r *http.Request, req SandboxControlRequest) (time.Duration, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("timeout"))
	if raw == "" {
		raw = strings.TrimSpace(req.Timeout)
	}
	if raw == "" {
		return 30 * time.Second, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout < 0 {
		return 0, fmt.Errorf("sandbox control timeout must be a non-negative duration")
	}
	return timeout, nil
}

func sandboxRunTimeout(timeout time.Duration) string {
	if timeout <= 0 {
		return ""
	}
	return timeout.String()
}

func waitSandboxExec(r *http.Request, store *Store, result SandboxExecResult, timeout time.Duration) (SandboxExecResult, bool) {
	if timeout == 0 || result.Done {
		return result, true
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		assignment, ok := store.GetAssignment(result.Assignment.ID)
		if !ok {
			return result, false
		}
		result = sandboxExecResult(result.ID, result.VMName, assignment)
		if result.Done {
			return result, true
		}
		select {
		case <-r.Context().Done():
			return result, true
		case <-deadline.C:
			return result, true
		case <-ticker.C:
		}
	}
}

func waitSandboxControl(r *http.Request, store *Store, result SandboxControlResult, timeout time.Duration) (SandboxControlResult, bool) {
	if timeout == 0 || result.Done {
		return result, true
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		assignment, ok := store.GetAssignment(result.Assignment.ID)
		if !ok {
			return result, false
		}
		result = sandboxControlResult(result.ID, result.VMName, result.Type, assignment)
		if result.Done {
			return result, true
		}
		select {
		case <-r.Context().Done():
			return result, true
		case <-deadline.C:
			return result, true
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
	case "events":
		handleWorkerEvents(w, r, store, id)
	case "cordon":
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		handleWorkerCordon(w, r, store, id)
	case "decommission":
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		handleWorkerDecommission(w, r, store, id)
	case "drain":
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		handleWorkerDrain(w, r, store, id)
	case "evacuate":
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		handleWorkerEvacuate(w, r, store, id)
	case "quarantine":
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		handleWorkerQuarantine(w, r, store, id)
	case "reports":
		handleWorkerReports(w, r, store, id)
	case "metering":
		handleWorkerMetering(w, r, store, id)
	case "sandboxes":
		handleWorkerSandboxes(w, r, store, id)
	case "uncordon":
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		handleWorkerUncordon(w, r, store, id)
	case "unquarantine":
		identity := identityFromRequest(r, store)
		if !requireRole(w, identity, ServiceAccountRoleOperator) {
			return
		}
		if !requireUnscoped(w, r, store) {
			return
		}
		handleWorkerUnquarantine(w, r, store, id)
	default:
		writeError(w, http.StatusNotFound, "worker route not found")
	}
}

func handleWorkerEvents(w http.ResponseWriter, r *http.Request, store *Store, id string) {
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
	if _, ok := store.Get(id); !ok {
		writeError(w, http.StatusNotFound, "worker not found")
		return
	}
	filter, err := workerEventsFilterFromRequest(r, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListAuditPage(filter))
}

func workerEventsFilterFromRequest(r *http.Request, id string) (AuditListFilter, error) {
	filter := AuditListFilter{
		WorkerID:   id,
		Actor:      strings.TrimSpace(r.URL.Query().Get("actor")),
		Action:     strings.TrimSpace(r.URL.Query().Get("action")),
		TargetType: strings.TrimSpace(r.URL.Query().Get("target_type")),
		TargetID:   strings.TrimSpace(r.URL.Query().Get("target_id")),
		SandboxID:  strings.TrimSpace(r.URL.Query().Get("sandbox_id")),
	}
	if filter.SandboxID == "" {
		filter.SandboxID = strings.TrimSpace(r.URL.Query().Get("sandbox"))
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return AuditListFilter{}, fmt.Errorf("worker events limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return AuditListFilter{}, fmt.Errorf("worker events offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func handleWorkerMetering(w http.ResponseWriter, r *http.Request, store *Store, id string) {
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
	if _, ok := store.Get(id); !ok {
		writeError(w, http.StatusNotFound, "worker not found")
		return
	}
	namespace := namespaceFilterFromRequest(r, identity)
	sandboxID := strings.TrimSpace(r.URL.Query().Get("sandbox_id"))
	if sandboxID == "" {
		sandboxID = strings.TrimSpace(r.URL.Query().Get("sandbox"))
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	writeJSON(w, http.StatusOK, store.ListWorkerMetering(namespace, id, sandboxID, status))
}

func handleWorkerSandboxes(w http.ResponseWriter, r *http.Request, store *Store, id string) {
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
	filter, err := workerSandboxesFilterFromRequest(r, namespaceFilterFromRequest(r, identity), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !reconcile(w, store) {
		return
	}
	if _, ok := store.Get(id); !ok {
		writeError(w, http.StatusNotFound, "worker not found")
		return
	}
	writeJSON(w, http.StatusOK, store.ListSandboxesPage(filter))
}

func workerSandboxesFilterFromRequest(r *http.Request, namespace, id string) (SandboxListFilter, error) {
	filter := SandboxListFilter{
		Namespace:           namespace,
		Status:              strings.TrimSpace(r.URL.Query().Get("status")),
		WorkerID:            id,
		ImageRef:            strings.TrimSpace(r.URL.Query().Get("image_ref")),
		ImageManifestDigest: strings.TrimSpace(r.URL.Query().Get("image_manifest_digest")),
		ImageDigestRef:      strings.TrimSpace(r.URL.Query().Get("image_digest_ref")),
		ImagePlatform:       strings.TrimSpace(r.URL.Query().Get("image_platform")),
		RequiredCapability:  strings.TrimSpace(r.URL.Query().Get("required_capability")),
	}
	hasOpen, err := sandboxListBoolFilterFromRequest(r, "worker sandboxes")
	if err != nil {
		return SandboxListFilter{}, err
	}
	filter.HasOpenAssignments = hasOpen
	retrying, err := sandboxRetryingFilterFromRequest(r, "worker sandboxes")
	if err != nil {
		return SandboxListFilter{}, err
	}
	filter.Retrying = retrying
	hasCleanup, err := sandboxCleanupFilterFromRequest(r, "worker sandboxes")
	if err != nil {
		return SandboxListFilter{}, err
	}
	filter.HasCleanup = hasCleanup
	hasLease, err := sandboxLeaseFilterFromRequest(r, "worker sandboxes")
	if err != nil {
		return SandboxListFilter{}, err
	}
	filter.HasLease = hasLease
	filter.LeaseHolder = strings.TrimSpace(r.URL.Query().Get("lease_holder"))
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return SandboxListFilter{}, fmt.Errorf("worker sandboxes limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return SandboxListFilter{}, fmt.Errorf("worker sandboxes offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func sandboxListBoolFilterFromRequest(r *http.Request, name string) (*bool, error) {
	return boolFilterFromRequest(r, name, "has_open_assignments", "open_assignments")
}

func sandboxRetryingFilterFromRequest(r *http.Request, name string) (*bool, error) {
	return boolFilterFromRequest(r, name, "retrying", "has_retry")
}

func sandboxCleanupFilterFromRequest(r *http.Request, name string) (*bool, error) {
	return boolFilterFromRequest(r, name, "has_cleanup", "cleanup")
}

func sandboxLeaseFilterFromRequest(r *http.Request, name string) (*bool, error) {
	return boolFilterFromRequest(r, name, "has_lease", "leased")
}

func boolFilterFromRequest(r *http.Request, name, field string, aliases ...string) (*bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(field))
	for _, alias := range aliases {
		if raw != "" {
			break
		}
		raw = strings.TrimSpace(r.URL.Query().Get(alias))
	}
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("%s %s must be true or false", name, field)
	}
	return &value, nil
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
	switch r.Method {
	case http.MethodGet:
		handleWorkerReportHistory(w, r, store, id)
	case http.MethodPost:
		handleWorkerReportStatus(w, r, store, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleWorkerReportStatus(w http.ResponseWriter, r *http.Request, store *Store, id string) {
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

func handleWorkerReportHistory(w http.ResponseWriter, r *http.Request, store *Store, id string) {
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
	if _, ok := store.Get(id); !ok {
		writeError(w, http.StatusNotFound, "worker not found")
		return
	}
	filter, err := workerReportsFilterFromRequest(r, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListAssignmentReportsPage(filter))
}

func workerReportsFilterFromRequest(r *http.Request, id string) (AssignmentReportFilter, error) {
	filter := AssignmentReportFilter{
		WorkerID:     id,
		AssignmentID: strings.TrimSpace(r.URL.Query().Get("assignment_id")),
		Status:       strings.TrimSpace(r.URL.Query().Get("status")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return AssignmentReportFilter{}, fmt.Errorf("worker reports limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return AssignmentReportFilter{}, fmt.Errorf("worker reports offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
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
	if err := resolveImagePrepareManifestBundle(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := store.PrepareImageActor(identity.Actor, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleImagePreparations(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	filter, err := imagePrepareListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListImagePreparationsPage(filter))
}

func handleImagePreparation(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := nameFromPath(r.URL.Path, "/v1/images/preparations/", "image preparation")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	prep, ok := store.GetImagePreparation(id)
	if !ok || !canAccessNamespace(identity, prep.Namespace) {
		writeError(w, http.StatusNotFound, "image preparation not found")
		return
	}
	writeJSON(w, http.StatusOK, prep)
}

func imagePrepareListFilterFromRequest(r *http.Request, namespace string) (ImagePrepareListFilter, error) {
	filter := ImagePrepareListFilter{
		Namespace:           namespace,
		SourceRef:           strings.TrimSpace(r.URL.Query().Get("source_ref")),
		ImageRef:            strings.TrimSpace(r.URL.Query().Get("image_ref")),
		ImageManifestDigest: strings.TrimSpace(r.URL.Query().Get("image_manifest_digest")),
		ImageDigestRef:      strings.TrimSpace(r.URL.Query().Get("image_digest_ref")),
		ImagePlatform:       strings.TrimSpace(r.URL.Query().Get("image_platform")),
		RequiredCapability:  strings.TrimSpace(r.URL.Query().Get("required_capability")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return ImagePrepareListFilter{}, fmt.Errorf("image preparation limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return ImagePrepareListFilter{}, fmt.Errorf("image preparation offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
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

func handleImageGCRuns(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	filter, err := imageGCListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListImageGCRunsPage(filter))
}

func handleImageGCRun(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := nameFromPath(r.URL.Path, "/v1/images/gc/runs/", "image gc run")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	run, ok := store.GetImageGCRun(id)
	if !ok || !canAccessNamespace(identity, run.Namespace) {
		writeError(w, http.StatusNotFound, "image gc run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func imageGCListFilterFromRequest(r *http.Request, namespace string) (ImageGCListFilter, error) {
	filter := ImageGCListFilter{
		Namespace: namespace,
	}
	olderThan, err := normalizeDurationString(r.URL.Query().Get("older_than"), "image gc older_than")
	if err != nil {
		return ImageGCListFilter{}, err
	}
	filter.OlderThan = olderThan
	if raw := strings.TrimSpace(r.URL.Query().Get("apply")); raw != "" {
		apply, err := strconv.ParseBool(raw)
		if err != nil {
			return ImageGCListFilter{}, fmt.Errorf("image gc apply must be true or false")
		}
		filter.Apply = &apply
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return ImageGCListFilter{}, fmt.Errorf("image gc limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return ImageGCListFilter{}, fmt.Errorf("image gc offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
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

func handleLifecyclePolicyRuns(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	filter, err := lifecyclePolicyListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListLifecyclePolicyRunsPage(filter))
}

func handleLifecyclePolicyRun(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := nameFromPath(r.URL.Path, "/v1/policies/lifecycle/runs/", "lifecycle policy run")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	run, ok := store.GetLifecyclePolicyRun(id)
	if !ok || !canAccessNamespace(identity, run.Namespace) {
		writeError(w, http.StatusNotFound, "lifecycle policy run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func lifecyclePolicyListFilterFromRequest(r *http.Request, namespace string) (LifecyclePolicyListFilter, error) {
	filter := LifecyclePolicyListFilter{
		Namespace: namespace,
		VMName:    strings.TrimSpace(r.URL.Query().Get("vm_name")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("clear")); raw != "" {
		clear, err := strconv.ParseBool(raw)
		if err != nil {
			return LifecyclePolicyListFilter{}, fmt.Errorf("lifecycle policy clear must be true or false")
		}
		filter.Clear = &clear
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return LifecyclePolicyListFilter{}, fmt.Errorf("lifecycle policy limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return LifecyclePolicyListFilter{}, fmt.Errorf("lifecycle policy offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
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

func handleStorageBudgetRuns(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	filter, err := storageBudgetListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListStorageBudgetRunsPage(filter))
}

func handleStorageBudgetRun(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := nameFromPath(r.URL.Path, "/v1/storage/budget/runs/", "storage budget run")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	run, ok := store.GetStorageBudgetRun(id)
	if !ok || !canAccessNamespace(identity, run.Namespace) {
		writeError(w, http.StatusNotFound, "storage budget run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func storageBudgetListFilterFromRequest(r *http.Request, namespace string) (StorageBudgetListFilter, error) {
	filter := StorageBudgetListFilter{
		Namespace: namespace,
		Target:    strings.TrimSpace(r.URL.Query().Get("target")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("clear")); raw != "" {
		clear, err := strconv.ParseBool(raw)
		if err != nil {
			return StorageBudgetListFilter{}, fmt.Errorf("storage budget clear must be true or false")
		}
		filter.Clear = &clear
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return StorageBudgetListFilter{}, fmt.Errorf("storage budget limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return StorageBudgetListFilter{}, fmt.Errorf("storage budget offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
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

func handleStoragePruneRuns(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	filter, err := storagePruneListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListStoragePruneRunsPage(filter))
}

func handleStoragePruneRun(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := nameFromPath(r.URL.Path, "/v1/storage/prune/runs/", "storage prune run")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	run, ok := store.GetStoragePruneRun(id)
	if !ok || !canAccessNamespace(identity, run.Namespace) {
		writeError(w, http.StatusNotFound, "storage prune run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func storagePruneListFilterFromRequest(r *http.Request, namespace string) (StoragePruneListFilter, error) {
	filter := StoragePruneListFilter{
		Namespace: namespace,
		Category:  strings.TrimSpace(r.URL.Query().Get("category")),
	}
	olderThan, err := normalizeDurationString(r.URL.Query().Get("older_than"), "storage prune older_than")
	if err != nil {
		return StoragePruneListFilter{}, err
	}
	filter.OlderThan = olderThan
	if raw := strings.TrimSpace(r.URL.Query().Get("apply")); raw != "" {
		apply, err := strconv.ParseBool(raw)
		if err != nil {
			return StoragePruneListFilter{}, fmt.Errorf("storage prune apply must be true or false")
		}
		filter.Apply = &apply
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return StoragePruneListFilter{}, fmt.Errorf("storage prune limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return StoragePruneListFilter{}, fmt.Errorf("storage prune offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
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
	if err := resolveAssignmentManifestBundle(&req.Assignment); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := store.PlanAssignment(req.Assignment, req.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func handlePlacementPlans(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	filter, err := placementPlanListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListPlacementPlansPage(filter))
}

func handlePlacementPlanRecord(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := nameFromPath(r.URL.Path, "/v1/placements/plans/", "placement plan")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	plan, ok := store.GetPlacementPlan(id)
	if !ok || !canAccessNamespace(identity, plan.Namespace) {
		writeError(w, http.StatusNotFound, "placement plan not found")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func placementPlanListFilterFromRequest(r *http.Request, namespace string) (PlacementPlanListFilter, error) {
	filter := PlacementPlanListFilter{
		Namespace:           namespace,
		Policy:              strings.TrimSpace(r.URL.Query().Get("policy")),
		ImageRef:            strings.TrimSpace(r.URL.Query().Get("image_ref")),
		ImageManifestDigest: strings.TrimSpace(r.URL.Query().Get("image_manifest_digest")),
		ImageDigestRef:      strings.TrimSpace(r.URL.Query().Get("image_digest_ref")),
		ImagePlatform:       strings.TrimSpace(r.URL.Query().Get("image_platform")),
		RequiredCapability:  strings.TrimSpace(r.URL.Query().Get("required_capability")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return PlacementPlanListFilter{}, fmt.Errorf("placement plan limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return PlacementPlanListFilter{}, fmt.Errorf("placement plan offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func handleWarmPools(w http.ResponseWriter, r *http.Request, store *Store) {
	identity := identityFromRequest(r, store)
	switch r.Method {
	case http.MethodGet:
		if !requireRole(w, identity, ServiceAccountRoleViewer) {
			return
		}
		filter, err := warmPoolListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !reconcile(w, store) {
			return
		}
		writeJSON(w, http.StatusOK, store.ListWarmPoolsPage(filter))
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
		if err := resolveWarmPoolManifestBundle(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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

func warmPoolListFilterFromRequest(r *http.Request, namespace string) (WarmPoolListFilter, error) {
	filter := WarmPoolListFilter{
		Namespace:           namespace,
		ImageRef:            strings.TrimSpace(r.URL.Query().Get("image_ref")),
		ImageManifestDigest: strings.TrimSpace(r.URL.Query().Get("image_manifest_digest")),
		ImageDigestRef:      strings.TrimSpace(r.URL.Query().Get("image_digest_ref")),
		ImagePlatform:       strings.TrimSpace(r.URL.Query().Get("image_platform")),
		RequiredCapability:  strings.TrimSpace(r.URL.Query().Get("required_capability")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return WarmPoolListFilter{}, fmt.Errorf("warm pool limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return WarmPoolListFilter{}, fmt.Errorf("warm pool offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func handleWarmPool(w http.ResponseWriter, r *http.Request, store *Store) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/warm-pools/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "events" {
		name, err := warmPoolNameFromRaw(parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		handleWarmPoolEvents(w, r, store, name)
		return
	}
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

func handleWarmPoolEvents(w http.ResponseWriter, r *http.Request, store *Store, name string) {
	if name == "" || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	if !reconcile(w, store) {
		return
	}
	status, ok := store.GetWarmPool(name)
	if !ok || !canAccessNamespace(identity, status.Namespace) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("warm pool %q not found", name))
		return
	}
	filter, err := warmPoolEventsFilterFromRequest(r, status.Namespace, name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListAuditPage(filter))
}

func warmPoolEventsFilterFromRequest(r *http.Request, namespace, name string) (AuditListFilter, error) {
	filter := AuditListFilter{
		Namespace:    namespace,
		Actor:        strings.TrimSpace(r.URL.Query().Get("actor")),
		Action:       strings.TrimSpace(r.URL.Query().Get("action")),
		TargetType:   "warm_pool",
		TargetID:     name,
		WorkerID:     strings.TrimSpace(r.URL.Query().Get("worker_id")),
		AssignmentID: strings.TrimSpace(r.URL.Query().Get("assignment_id")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return AuditListFilter{}, fmt.Errorf("warm pool events limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return AuditListFilter{}, fmt.Errorf("warm pool events offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func warmPoolNameFromPath(path string) (string, error) {
	return nameFromPath(path, "/v1/warm-pools/", "warm pool")
}

func warmPoolNameFromRaw(raw string) (string, error) {
	name, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("decode warm pool name: %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("warm pool name required")
	}
	return name, nil
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

type requestIdentityKey struct{}

func actorFromRequest(r *http.Request, store *Store) string {
	return identityFromRequest(r, store).Actor
}

func identityFromRequest(r *http.Request, store *Store) requestIdentity {
	if identity, ok := r.Context().Value(requestIdentityKey{}).(requestIdentity); ok {
		return identity
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "bearer "
	if len(auth) >= len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		token := strings.TrimSpace(auth[len(prefix):])
		if principal, ok := store.AuthenticateBearer(token); ok {
			namespace := normalizeNamespace(principal.Namespace)
			return requestIdentity{
				Actor:     principal.Actor,
				Namespace: namespace,
				Role:      principal.Role,
				Scoped:    namespace != "",
			}
		}
		return requestIdentity{Invalid: true}
	}
	return requestIdentity{Actor: "controller", Role: ServiceAccountRoleAdmin}
}

func rejectInvalidAuth(next http.Handler, store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := identityFromRequest(r, store)
		if identity.Invalid {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		ctx := context.WithValue(r.Context(), requestIdentityKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
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

func oidcBindingByName(bindings []OIDCBinding, name string) (OIDCBinding, bool) {
	name = strings.TrimSpace(name)
	for _, binding := range bindings {
		if binding.Name == name {
			return binding, true
		}
	}
	return OIDCBinding{}, false
}

func samlBindingByName(bindings []SAMLBinding, name string) (SAMLBinding, bool) {
	name = strings.TrimSpace(name)
	for _, binding := range bindings {
		if binding.Name == name {
			return binding, true
		}
	}
	return SAMLBinding{}, false
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

func handleWorkerDrain(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var lifecycle WorkerLifecycle
	if err := json.NewDecoder(r.Body).Decode(&lifecycle); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode worker lifecycle: %v", err))
		return
	}
	result, err := store.DrainWorkerActor(actorFromRequest(r, store), id, lifecycle.Reason)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleWorkerDecommission(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var lifecycle WorkerLifecycle
	if err := json.NewDecoder(r.Body).Decode(&lifecycle); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode worker lifecycle: %v", err))
		return
	}
	result, err := store.DecommissionWorkerActor(actorFromRequest(r, store), id, lifecycle.Reason, lifecycle.Force)
	if err != nil {
		if lifecycle.Force && len(result.Blocked) > 0 {
			writeJSON(w, http.StatusConflict, result)
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleWorkerEvacuate(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req WorkerEvacuationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode worker evacuation: %v", err))
		return
	}
	result, err := store.EvacuateWorkerActor(actorFromRequest(r, store), id, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleWorkerQuarantine(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var lifecycle WorkerLifecycle
	if err := json.NewDecoder(r.Body).Decode(&lifecycle); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode worker lifecycle: %v", err))
		return
	}
	record, err := store.QuarantineWorkerActor(actorFromRequest(r, store), id, lifecycle.Reason)
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

func handleWorkerUnquarantine(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	record, err := store.UnquarantineWorkerActor(actorFromRequest(r, store), id)
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
		filter, err := assignmentListFilterFromRequest(r, namespaceFilterFromRequest(r, identity))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, store.ListAssignmentsPage(filter))
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
		if err := resolveAssignmentManifestBundle(&assignment); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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

func assignmentListFilterFromRequest(r *http.Request, namespace string) (AssignmentListFilter, error) {
	filter := AssignmentListFilter{
		Namespace:           namespace,
		Status:              strings.TrimSpace(r.URL.Query().Get("status")),
		WorkerID:            strings.TrimSpace(r.URL.Query().Get("worker_id")),
		LeasedTo:            strings.TrimSpace(r.URL.Query().Get("leased_to")),
		Verb:                strings.TrimSpace(r.URL.Query().Get("verb")),
		ImageRef:            strings.TrimSpace(r.URL.Query().Get("image_ref")),
		ImageManifestDigest: strings.TrimSpace(r.URL.Query().Get("image_manifest_digest")),
		ImageDigestRef:      strings.TrimSpace(r.URL.Query().Get("image_digest_ref")),
		ImagePlatform:       strings.TrimSpace(r.URL.Query().Get("image_platform")),
		RequiredCapability:  strings.TrimSpace(r.URL.Query().Get("required_capability")),
		SandboxID:           strings.TrimSpace(r.URL.Query().Get("sandbox_id")),
		WarmPool:            strings.TrimSpace(r.URL.Query().Get("warm_pool")),
	}
	if filter.SandboxID == "" {
		filter.SandboxID = strings.TrimSpace(r.URL.Query().Get("sandbox"))
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return AssignmentListFilter{}, fmt.Errorf("assignment limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return AssignmentListFilter{}, fmt.Errorf("assignment offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func handleAssignment(w http.ResponseWriter, r *http.Request, store *Store) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/assignments/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 {
		switch parts[1] {
		case "cancel":
			handleAssignmentCancel(w, r, store, parts[0])
			return
		case "events":
			handleAssignmentEvents(w, r, store, parts[0])
			return
		case "metering":
			handleAssignmentMetering(w, r, store, parts[0])
			return
		case "reports":
			handleAssignmentReports(w, r, store, parts[0])
			return
		case "retry":
			handleAssignmentRetry(w, r, store, parts[0])
			return
		}
	}
	if path == "" || len(parts) != 1 {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	id := parts[0]
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	if !reconcile(w, store) {
		return
	}
	assignment, ok := store.GetAssignment(id)
	if !ok || !canAccessNamespace(identity, assignment.Namespace) {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	writeJSON(w, http.StatusOK, assignment)
}

func handleAssignmentEvents(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if id == "" || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	if !reconcile(w, store) {
		return
	}
	assignment, ok := store.GetAssignment(id)
	if !ok || !canAccessNamespace(identity, assignment.Namespace) {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	filter, err := assignmentEventsFilterFromRequest(r, assignment.Namespace, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListAssignmentEventsPage(assignment.Namespace, id, filter))
}

func assignmentEventsFilterFromRequest(r *http.Request, namespace, id string) (AuditListFilter, error) {
	filter := AuditListFilter{
		Namespace:    namespace,
		AssignmentID: id,
		Actor:        strings.TrimSpace(r.URL.Query().Get("actor")),
		Action:       strings.TrimSpace(r.URL.Query().Get("action")),
		TargetType:   strings.TrimSpace(r.URL.Query().Get("target_type")),
		TargetID:     strings.TrimSpace(r.URL.Query().Get("target_id")),
		WorkerID:     strings.TrimSpace(r.URL.Query().Get("worker_id")),
		SandboxID:    strings.TrimSpace(r.URL.Query().Get("sandbox_id")),
	}
	if filter.SandboxID == "" {
		filter.SandboxID = strings.TrimSpace(r.URL.Query().Get("sandbox"))
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return AuditListFilter{}, fmt.Errorf("assignment events limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return AuditListFilter{}, fmt.Errorf("assignment events offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func handleAssignmentMetering(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if id == "" || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	if !reconcile(w, store) {
		return
	}
	assignment, ok := store.GetAssignment(id)
	if !ok || !canAccessNamespace(identity, assignment.Namespace) {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	writeJSON(w, http.StatusOK, store.ListAssignmentMetering(assignment.Namespace, id, status))
}

func handleAssignmentReports(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if id == "" || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleViewer) {
		return
	}
	if !reconcile(w, store) {
		return
	}
	assignment, ok := store.GetAssignment(id)
	if !ok || !canAccessNamespace(identity, assignment.Namespace) {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	filter, err := assignmentReportsFilterFromRequest(r, assignment.Namespace, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, store.ListAssignmentReportsPage(filter))
}

func assignmentReportsFilterFromRequest(r *http.Request, namespace, id string) (AssignmentReportFilter, error) {
	filter := AssignmentReportFilter{
		Namespace:    namespace,
		AssignmentID: id,
		WorkerID:     strings.TrimSpace(r.URL.Query().Get("worker_id")),
		Status:       strings.TrimSpace(r.URL.Query().Get("status")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return AssignmentReportFilter{}, fmt.Errorf("assignment reports limit must be non-negative")
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return AssignmentReportFilter{}, fmt.Errorf("assignment reports offset must be non-negative")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func handleAssignmentCancel(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if id == "" || r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleOperator) {
		return
	}
	var req AssignmentCancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assignment cancel: %v", err))
		return
	}
	assignment, ok := store.GetAssignment(id)
	if !ok || !canAccessNamespace(identity, assignment.Namespace) {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	result, err := store.CancelAssignmentActor(identity.Actor, id, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleAssignmentRetry(w http.ResponseWriter, r *http.Request, store *Store, id string) {
	if id == "" || r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	identity := identityFromRequest(r, store)
	if !requireRole(w, identity, ServiceAccountRoleOperator) {
		return
	}
	var req AssignmentRetryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assignment retry: %v", err))
		return
	}
	assignment, ok := store.GetAssignment(id)
	if !ok || !canAccessNamespace(identity, assignment.Namespace) {
		writeError(w, http.StatusNotFound, "assignment not found")
		return
	}
	result, err := store.RetryAssignmentActor(identity.Actor, id, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
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
