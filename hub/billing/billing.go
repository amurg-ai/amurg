// Package billing defines the billing interfaces for the hub.
// The community hub provides only the interfaces and plan definitions.
// SaaS implementations (e.g. Stripe) live in the SaaS repository.
package billing

import (
	"context"
	"net/http"

	"github.com/amurg-ai/amurg/hub/store"
)

// Service handles billing operations (checkout, portal, webhooks).
type Service interface {
	HandleWebhook(w http.ResponseWriter, r *http.Request)
	CreateCheckoutSession(ctx context.Context, orgID, priceID, successURL, cancelURL string) (string, error)
	CreatePortalSession(ctx context.Context, orgID, returnURL string) (string, error)
	GetSubscription(ctx context.Context, orgID string) (*store.Subscription, error)
}

// Enforcer checks plan limits before allowing resource creation.
type Enforcer interface {
	CheckTrialExpiry(ctx context.Context, orgID string) error
	CheckSessionLimit(ctx context.Context, orgID string) error
}
