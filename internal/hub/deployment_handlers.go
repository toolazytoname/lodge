package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

const maximumDeploymentInputBody = 8 << 10

func (s *Server) agentDeployments(w http.ResponseWriter, r *http.Request) {
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
	response, err := s.deployments.List(ctx, agent)
	if err != nil {
		writeJSONHub(w, http.StatusBadGateway, map[string]string{"error": actionClientErrorCategory(err)})
		return
	}
	if err := validateAgentDeployments(response); err != nil {
		writeJSONHub(w, http.StatusBadGateway, map[string]string{"error": "agent_invalid_response"})
		return
	}
	writeJSONHub(w, http.StatusOK, AgentDeploymentsResponse{
		AgentID: agent.ID, AgentName: agent.Name, AgentVersion: s.agentVersion(agent.ID),
		Deployments: response.Deployments,
	})
}

func (s *Server) executeDeployment(w http.ResponseWriter, r *http.Request) {
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
	r.Body = http.MaxBytesReader(w, r.Body, maximumDeploymentInputBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input DeploymentExecutionInput
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
		input.DeploymentID == "" || input.DeploymentID != strings.TrimSpace(input.DeploymentID) || len(input.DeploymentID) > 256 ||
		len(input.Confirmation) > 512 {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "invalid_deployment_request"})
		return
	}
	agent, found := s.agentConfig(input.AgentID)
	if !found {
		writeJSONHub(w, http.StatusNotFound, map[string]string{"error": "unknown_agent"})
		return
	}
	listContext, cancelList := context.WithTimeout(r.Context(), 5*time.Second)
	deployments, err := s.deployments.List(listContext, agent)
	cancelList()
	if err != nil {
		writeJSONHub(w, http.StatusBadGateway, map[string]string{"error": actionClientErrorCategory(err)})
		return
	}
	if err := validateAgentDeployments(deployments); err != nil {
		writeJSONHub(w, http.StatusBadGateway, map[string]string{"error": "agent_invalid_response"})
		return
	}
	definition, found := findAgentDeployment(deployments.Deployments, input.DeploymentID)
	if !found {
		writeJSONHub(w, http.StatusNotFound, map[string]string{"error": "deployment_not_approved"})
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
	locked := true
	defer func() {
		if locked {
			s.actionMu.Unlock()
		}
	}()

	requestedAt := s.now().UTC()
	operationID, err := newOperationID()
	if err != nil {
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "operation_id_unavailable"})
		return
	}
	operation := domain.Operation{
		ID: operationID, HostID: domain.HostID(agent.ID), WorkloadKey: definition.StackKey,
		Kind: deploymentOperationKind(definition.Kind), State: domain.OperationRequested,
		RequestedBy: s.operationRequester(r), RequestedAt: requestedAt,
	}
	auditContext, cancelAudit := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancelAudit()
	if err := s.store.CreateOperation(auditContext, operation); err != nil {
		log.Printf("lodge hub create deployment audit: %v", err)
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "operation_persistence_failed"})
		return
	}
	startedAt := s.now().UTC()
	if startedAt.Before(requestedAt) {
		startedAt = requestedAt
	}
	running, found, err := s.store.StartOperation(auditContext, operation.ID, startedAt)
	if err != nil || !found {
		log.Printf("lodge hub start deployment audit: found=%v err=%v", found, err)
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "operation_persistence_failed"})
		return
	}

	// Ownership of the fleet-wide lock transfers to the bounded background
	// finalizer before the accepted response is returned.
	locked = false
	go s.finishDeploymentOperation(agent, definition, running)
	writeJSONHub(w, http.StatusAccepted, DeploymentExecutionResponse{Operation: operationView(running)})
}

func (s *Server) finishDeploymentOperation(agent AgentConfig, definition shared.DeploymentDefinition, running domain.Operation) {
	defer s.actionMu.Unlock()
	operationContext, cancelOperation := context.WithTimeout(context.Background(), deploymentExecutionBudget)
	defer cancelOperation()
	executeContext, cancelExecute := context.WithTimeout(operationContext, agentDeploymentTimeout)
	result, executeErr := s.deployments.Execute(executeContext, agent, definition)
	cancelExecute()
	if executeErr == nil {
		if err := validateAgentDeploymentResult(result, definition); err != nil {
			executeErr = actionClientError("agent_invalid_response")
		}
	}
	state, summary, errorKind := domain.OperationSucceeded, result.Summary, ""
	if executeErr != nil {
		state, summary, errorKind = domain.OperationFailed, "", actionClientErrorCategory(executeErr)
	} else if !result.OK && result.RollbackPerformed {
		state, summary, errorKind = domain.OperationRolledBack, result.Summary, result.ErrorKind
	} else if !result.OK {
		state, summary, errorKind = domain.OperationFailed, result.Summary, result.ErrorKind
	}
	finishedAt := s.now().UTC()
	if running.StartedAt != nil && finishedAt.Before(*running.StartedAt) {
		finishedAt = *running.StartedAt
	}
	_, found, finishErr := s.store.FinishOperation(operationContext, running.ID, state, finishedAt, summary, errorKind)
	if finishErr != nil || !found {
		log.Printf("lodge hub finish deployment audit: id=%s found=%v err=%v", running.ID, found, finishErr)
		return
	}
}

func findAgentDeployment(deployments []shared.DeploymentDefinition, id string) (shared.DeploymentDefinition, bool) {
	for _, definition := range deployments {
		if definition.ID == id {
			return definition, true
		}
	}
	return shared.DeploymentDefinition{}, false
}

func deploymentOperationKind(kind shared.DeploymentKind) domain.OperationKind {
	if kind == shared.DeploymentRollback {
		return domain.OperationRollback
	}
	return domain.OperationDeploy
}
