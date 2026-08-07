package hub

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	maximumActionInputBody = 8 << 10
	operationsDefaultLimit = 100
	operationsMaximumLimit = 500
)

func (s *Server) agentActions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	agentID, ok := exactQueryValue(r, "agent", true)
	if !ok || len(agentID) > maxAgentIDBytes {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "invalid agent"})
		return
	}
	agent, found := s.agentConfig(agentID)
	if !found {
		writeJSONHub(w, http.StatusNotFound, map[string]string{"error": "unknown agent"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := s.actions.List(ctx, agent)
	if err != nil {
		writeJSONHub(w, http.StatusBadGateway, map[string]string{"error": actionClientErrorCategory(err)})
		return
	}
	if err := validateAgentActions(response); err != nil {
		writeJSONHub(w, http.StatusBadGateway, map[string]string{"error": "agent_invalid_response"})
		return
	}
	writeJSONHub(w, http.StatusOK, AgentActionsResponse{
		AgentID: agent.ID, AgentName: agent.Name, AgentVersion: s.agentVersion(agent.ID),
		Actions: response.Actions,
	})
}

func (s *Server) executeAction(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if r.URL.RawQuery != "" {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "query_not_allowed"})
		return
	}
	if !hasJSONContentType(r) {
		writeJSONHub(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type 必须是 application/json"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumActionInputBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input ActionExecutionInput
	decodeErr := decoder.Decode(&input)
	eofErr := error(nil)
	if decodeErr == nil {
		eofErr = ensureJSONEOF(decoder)
	}
	if decodeErr != nil || eofErr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(decodeErr, &tooLarge) || errors.As(eofErr, &tooLarge) {
			writeJSONHub(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body_too_large"})
			return
		}
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "bad_json"})
		return
	}
	if input.AgentID == "" || input.AgentID != strings.TrimSpace(input.AgentID) || len(input.AgentID) > maxAgentIDBytes ||
		input.ActionID == "" || input.ActionID != strings.TrimSpace(input.ActionID) || len(input.ActionID) > 320 ||
		len(input.Confirmation) > 512 {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "invalid_action_request"})
		return
	}
	agent, found := s.agentConfig(input.AgentID)
	if !found {
		writeJSONHub(w, http.StatusNotFound, map[string]string{"error": "unknown_agent"})
		return
	}
	listContext, cancelList := context.WithTimeout(r.Context(), 5*time.Second)
	actions, err := s.actions.List(listContext, agent)
	cancelList()
	if err != nil {
		writeJSONHub(w, http.StatusBadGateway, map[string]string{"error": actionClientErrorCategory(err)})
		return
	}
	if err := validateAgentActions(actions); err != nil {
		writeJSONHub(w, http.StatusBadGateway, map[string]string{"error": "agent_invalid_response"})
		return
	}
	definition, found := findAgentAction(actions.Actions, input.ActionID)
	if !found {
		writeJSONHub(w, http.StatusNotFound, map[string]string{"error": "action_not_approved"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(input.Confirmation), []byte(definition.Confirmation)) != 1 {
		writeJSONHub(w, http.StatusUnprocessableEntity, map[string]string{"error": "confirmation_mismatch"})
		return
	}
	if !s.actionMu.TryLock() {
		writeJSONHub(w, http.StatusConflict, map[string]string{"error": "action_in_progress"})
		return
	}
	defer s.actionMu.Unlock()

	requestedAt := s.now().UTC()
	operationID, err := newOperationID()
	if err != nil {
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "operation_id_unavailable"})
		return
	}
	operation := domain.Operation{
		ID: operationID, HostID: domain.HostID(agent.ID), WorkloadKey: definition.TargetKey,
		Kind: operationKind(definition.Kind), State: domain.OperationRequested,
		RequestedBy: s.operationRequester(r), RequestedAt: requestedAt,
	}
	operationContext, cancelOperation := context.WithTimeout(context.Background(), actionExecutionBudget)
	defer cancelOperation()
	if err := s.store.CreateOperation(operationContext, operation); err != nil {
		log.Printf("lodge hub create operation audit: %v", err)
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "operation_persistence_failed"})
		return
	}
	startedAt := s.now().UTC()
	if startedAt.Before(requestedAt) {
		startedAt = requestedAt
	}
	running, found, err := s.store.StartOperation(operationContext, operation.ID, startedAt)
	if err != nil || !found {
		log.Printf("lodge hub start operation audit: found=%v err=%v", found, err)
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "operation_persistence_failed"})
		return
	}

	// Reserve part of the operation budget for durable finalization even when
	// the Agent consumes its entire network deadline.
	executeContext, cancelExecute := context.WithTimeout(operationContext, agentActionTimeout)
	result, executeErr := s.actions.Execute(executeContext, agent, definition)
	cancelExecute()
	if executeErr == nil {
		executeErr = validateAgentActionResult(result, definition)
		if executeErr != nil {
			executeErr = actionClientError("agent_invalid_response")
		}
	}
	finishedState, summary, errorKind := domain.OperationSucceeded, result.Summary, ""
	if executeErr != nil {
		finishedState, summary, errorKind = domain.OperationFailed, "", actionClientErrorCategory(executeErr)
	} else if !result.OK {
		finishedState, summary, errorKind = domain.OperationFailed, result.Summary, result.ErrorKind
	}
	finishedAt := s.now().UTC()
	if running.StartedAt != nil && finishedAt.Before(*running.StartedAt) {
		finishedAt = *running.StartedAt
	}
	finished, found, finishErr := s.store.FinishOperation(operationContext, running.ID, finishedState, finishedAt, summary, errorKind)
	if finishErr != nil || !found {
		log.Printf("lodge hub finish operation audit: found=%v err=%v", found, finishErr)
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "operation_persistence_failed"})
		return
	}
	response := ActionExecutionResponse{Operation: operationView(finished), ErrorKind: errorKind}
	if executeErr == nil {
		response.Result = &result
	}
	if executeErr != nil {
		writeJSONHub(w, http.StatusBadGateway, response)
		return
	}
	writeJSONHub(w, http.StatusOK, response)
}

func (s *Server) operations(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if !queryKeysAllowed(r, "agent", "limit") {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "invalid_query"})
		return
	}
	agentID := r.URL.Query().Get("agent")
	if len(r.URL.Query()["agent"]) > 1 || len(r.URL.Query()["limit"]) > 1 || len(agentID) > maxAgentIDBytes {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "invalid_agent"})
		return
	}
	if agentID != "" && !s.hasAgent(agentID) {
		writeJSONHub(w, http.StatusNotFound, map[string]string{"error": "unknown_agent"})
		return
	}
	limit, ok := boundedQueryLimit(w, r, operationsDefaultLimit, operationsMaximumLimit, "operation")
	if !ok {
		return
	}
	operations, err := s.store.Operations(r.Context(), domain.HostID(agentID), limit)
	if err != nil {
		log.Printf("lodge hub read operation audit: %v", err)
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "operation_persistence_failed"})
		return
	}
	response := OperationsResponse{AgentID: agentID, Operations: make([]OperationView, 0, len(operations))}
	for _, operation := range operations {
		response.Operations = append(response.Operations, operationView(operation))
	}
	writeJSONHub(w, http.StatusOK, response)
}

func (s *Server) agentConfig(id string) (AgentConfig, bool) {
	for _, agent := range s.store.Agents() {
		if agent.ID == id {
			return agent, true
		}
	}
	return AgentConfig{}, false
}

func (s *Server) agentVersion(id string) string {
	for _, snapshot := range s.store.Snapshot() {
		if snapshot.ID == id {
			return snapshot.AgentVer
		}
	}
	return ""
}

func findAgentAction(actions []shared.ActionDefinition, id string) (shared.ActionDefinition, bool) {
	for _, action := range actions {
		if action.ID == id {
			return action, true
		}
	}
	return shared.ActionDefinition{}, false
}

func operationKind(kind shared.ActionKind) domain.OperationKind {
	switch kind {
	case shared.ActionStart:
		return domain.OperationStart
	case shared.ActionStop:
		return domain.OperationStop
	case shared.ActionRestart:
		return domain.OperationRestart
	case shared.ActionLogs:
		return domain.OperationLogs
	default:
		return ""
	}
}

func operationView(operation domain.Operation) OperationView {
	view := OperationView{
		ID: operation.ID, AgentID: string(operation.HostID), TargetKey: operation.WorkloadKey,
		Kind: operation.Kind, State: operation.State, RequestedBy: operation.RequestedBy,
		RequestedAt:   operation.RequestedAt.UTC().Format(time.RFC3339Nano),
		ResultSummary: operation.ResultSummary, ErrorKind: operation.Error,
	}
	if operation.StartedAt != nil {
		view.StartedAt = operation.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if operation.FinishedAt != nil {
		view.FinishedAt = operation.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	return view
}

func newOperationID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "op_" + hex.EncodeToString(random[:]), nil
}

func exactQueryValue(r *http.Request, key string, required bool) (string, bool) {
	if !queryKeysAllowed(r, key) {
		return "", false
	}
	values := r.URL.Query()[key]
	if len(values) > 1 || (required && len(values) != 1) {
		return "", false
	}
	if len(values) == 0 {
		return "", !required
	}
	return values[0], values[0] != "" || !required
}

func queryKeysAllowed(r *http.Request, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range r.URL.Query() {
		if _, ok := set[key]; !ok {
			return false
		}
	}
	return true
}
