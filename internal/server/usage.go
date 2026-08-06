package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
	"github.com/hunterinvariants/promtact/internal/store"
)

func (a *App) recordToolDecision(r *http.Request, verdict domain.GatewayVerdict) {
	a.recordDecisionMetric(verdict)
	if !a.store.HasDirectory() {
		return
	}
	principal := principalFromRequest(r)
	if strings.TrimSpace(principal.Name) == "" {
		return
	}
	_ = a.store.IncrementUsage(r.Context(), tenantForPrincipal(principal), store.UsageToolDecisions, 1)
}
func (a *App) handleAdminTenantUsage(w http.ResponseWriter, r *http.Request, tenant string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	period := time.Now().UTC()
	if value := strings.TrimSpace(r.URL.Query().Get("period")); value != "" {
		parsed, err := time.Parse("2006-01", value)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("period must use YYYY-MM format"))
			return
		}
		period = parsed
	}
	metrics, err := a.store.TenantUsage(r.Context(), tenant, period)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenant": tenant, "period": period.Format("2006-01"), "metrics": metrics})
}
