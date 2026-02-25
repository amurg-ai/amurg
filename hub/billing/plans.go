package billing

// PlanLimits defines the resource limits for a plan tier.
type PlanLimits struct {
	Runtimes        int // max online runtimes per org (0 = unlimited)
	ActiveSessions  int // max active sessions per org (0 = unlimited)
	SessionsPerUser int // max active sessions per user (0 = unlimited)
	RetentionDays   int // transcript retention in days
}

// Plans maps plan names to their limits. All plans currently have unlimited usage;
// the only enforcement is the 14-day trial expiry for the "free" plan.
var Plans = map[string]PlanLimits{
	"free":       {}, // 14-day trial, then locked
	"single":     {}, // single user, unlimited
	"team":       {}, // team, unlimited
	"enterprise": {}, // enterprise, unlimited
}

// GetLimits returns the limits for a plan, defaulting to enterprise (unlimited) if unknown.
func GetLimits(plan string) PlanLimits {
	if l, ok := Plans[plan]; ok {
		return l
	}
	return Plans["enterprise"]
}
