package healing

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/RajNair06/atlas/gateway/errors"
)

// HealingHandler handles HTTP requests for the healing endpoint
type HealingHandler struct {
	errorStore     *errors.Store
	decisionEngine *DecisionEngine
}

// NewHealingHandler creates a new healing handler
func NewHealingHandler(errorStore *errors.Store, decisionEngine *DecisionEngine) *HealingHandler {
	return &HealingHandler{
		errorStore:     errorStore,
		decisionEngine: decisionEngine,
	}
}

// ServeHTTP handles requests to /healing/{request_id}
func (h *HealingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract request ID from path: /healing/{request_id}
	path := strings.TrimPrefix(r.URL.Path, "/healing/")
	requestID := strings.TrimSpace(path)

	if requestID == "" {
		http.Error(w, "Request ID is required", http.StatusBadRequest)
		return
	}

	slog.Info("healing analysis requested", "request_id", requestID)

	// Look up the failed request
	failedReq, exists := h.errorStore.Get(requestID)
	if !exists {
		http.Error(w, "Failed request not found", http.StatusNotFound)
		return
	}

	// Analyze the error with Gemini
	suggestion, err := h.decisionEngine.AnalyzeError(failedReq)
	if err != nil {
		slog.Error("healing analysis failed",
			"request_id", requestID,
			"error", err,
		)
		http.Error(w, "Healing analysis failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("healing analysis complete",
		"request_id", requestID,
		"action", suggestion.Action,
		"reasoning", suggestion.Reasoning,
	)

	// Return the suggestion as JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(suggestion); err != nil {
		slog.Error("failed to encode healing response", "error", err)
	}
}
