// Package lemonsqueezy is a Lemon Squeezy driver for togo payment. Blank-import
// it and set PAYMENT_DRIVER=lemonsqueezy, LEMONSQUEEZY_API_KEY, LEMONSQUEEZY_STORE_ID,
// LEMONSQUEEZY_WEBHOOK_SECRET. Implements hosted checkouts (/v1/checkouts),
// customers, subscription checkouts and HMAC webhook verification.
// See https://docs.lemonsqueezy.com/api.
package lemonsqueezy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/togo-framework/payment"
	"github.com/togo-framework/togo"
)

// api is the Lemon Squeezy base URL (a var so tests can point it at a mock server).
var api = "https://api.lemonsqueezy.com"

func init() {
	payment.RegisterDriver("lemonsqueezy", func(k *togo.Kernel) (payment.PaymentProvider, error) {
		key := os.Getenv("LEMONSQUEEZY_API_KEY")
		store := os.Getenv("LEMONSQUEEZY_STORE_ID")
		if key == "" || store == "" {
			return nil, errors.New("payment-lemonsqueezy: set LEMONSQUEEZY_API_KEY and LEMONSQUEEZY_STORE_ID")
		}
		return &provider{key: key, store: store, secret: os.Getenv("LEMONSQUEEZY_WEBHOOK_SECRET"), hc: &http.Client{Timeout: 20 * time.Second}}, nil
	})
}

type provider struct {
	key, store, secret string
	hc                 *http.Client
}

func (p *provider) post(ctx context.Context, path string, payload any) (map[string]any, error) {
	buf, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Accept", "application/vnd.api+json")
	req.Header.Set("Content-Type", "application/vnd.api+json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if resp.StatusCode >= 300 {
		return m, fmt.Errorf("payment-lemonsqueezy: %s %d: %s", path, resp.StatusCode, string(b))
	}
	return m, nil
}

// variant resolves the Lemon Squeezy variant id from the request (a variant is a
// purchasable product/plan — required for a checkout).
func variant(meta map[string]string, plan string) string {
	if plan != "" {
		return plan
	}
	return meta["variant_id"]
}

func (p *provider) checkout(ctx context.Context, variantID, email string, custom map[string]string) (*payment.CheckoutSession, error) {
	if variantID == "" {
		return nil, errors.New("payment-lemonsqueezy: a variant_id is required (set Metadata[\"variant_id\"] or PlanID)")
	}
	cd := map[string]any{}
	if email != "" {
		cd["email"] = email
	}
	if len(custom) > 0 {
		cd["custom"] = custom
	}
	body := map[string]any{"data": map[string]any{
		"type":       "checkouts",
		"attributes": map[string]any{"checkout_data": cd},
		"relationships": map[string]any{
			"store":   map[string]any{"data": map[string]any{"type": "stores", "id": p.store}},
			"variant": map[string]any{"data": map[string]any{"type": "variants", "id": variantID}},
		},
	}}
	m, err := p.post(ctx, "/v1/checkouts", body)
	if err != nil {
		return nil, err
	}
	data, _ := m["data"].(map[string]any)
	id, _ := data["id"].(string)
	attrs, _ := data["attributes"].(map[string]any)
	u, _ := attrs["url"].(string)
	return &payment.CheckoutSession{ID: id, URL: u}, nil
}

func (p *provider) CreateCheckoutSession(ctx context.Context, r payment.CheckoutRequest) (*payment.CheckoutSession, error) {
	return p.checkout(ctx, variant(r.Metadata, ""), r.Customer.Email, r.Metadata)
}

func (p *provider) CreateSubscription(ctx context.Context, r payment.SubscriptionRequest) (*payment.Subscription, error) {
	cs, err := p.checkout(ctx, variant(r.Metadata, r.PlanID), r.Customer.Email, r.Metadata)
	if err != nil {
		return nil, err
	}
	// LS subscriptions are created when the customer completes the checkout; the
	// returned id is the checkout to redirect to.
	return &payment.Subscription{ID: cs.ID, Status: "pending", PlanID: r.PlanID, Provider: "lemonsqueezy"}, nil
}

func (p *provider) CreateCustomer(ctx context.Context, c payment.Customer) (string, error) {
	body := map[string]any{"data": map[string]any{
		"type":       "customers",
		"attributes": map[string]any{"name": c.Name, "email": c.Email},
		"relationships": map[string]any{
			"store": map[string]any{"data": map[string]any{"type": "stores", "id": p.store}},
		},
	}}
	m, err := p.post(ctx, "/v1/customers", body)
	if err != nil {
		return "", err
	}
	data, _ := m["data"].(map[string]any)
	id, _ := data["id"].(string)
	return id, nil
}

func (p *provider) CreateCharge(context.Context, payment.ChargeRequest) (*payment.Charge, error) {
	return nil, errors.New("payment-lemonsqueezy: no direct charges — use CreateCheckoutSession (Lemon Squeezy is checkout-based)")
}

func (p *provider) Refund(context.Context, payment.RefundRequest) error {
	return errors.New("payment-lemonsqueezy: refunds are issued from the Lemon Squeezy dashboard / order, not this API surface")
}

// HandleWebhook verifies the X-Signature header (hex HMAC-SHA256 of the raw body
// with LEMONSQUEEZY_WEBHOOK_SECRET) and normalizes the event.
func (p *provider) HandleWebhook(_ context.Context, headers map[string]string, body []byte) (*payment.WebhookEvent, error) {
	if p.secret != "" {
		sig := header(headers, "X-Signature")
		mac := hmac.New(sha256.New, []byte(p.secret))
		mac.Write(body)
		if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			return nil, errors.New("payment-lemonsqueezy: invalid webhook signature")
		}
	}
	var ev map[string]any
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, err
	}
	typ := ""
	if meta, ok := ev["meta"].(map[string]any); ok {
		typ, _ = meta["event_name"].(string)
	}
	id := ""
	if data, ok := ev["data"].(map[string]any); ok {
		id, _ = data["id"].(string)
	}
	return &payment.WebhookEvent{Type: typ, ID: id, Provider: "lemonsqueezy", Raw: ev}, nil
}

func header(h map[string]string, k string) string {
	for key, v := range h {
		if strings.EqualFold(key, k) {
			return v
		}
	}
	return ""
}
