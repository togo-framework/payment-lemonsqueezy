package lemonsqueezy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/togo-framework/payment"
)

func TestCreateCheckoutSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ls_key" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/v1/checkouts" {
			t.Errorf("path: %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": "co_1", "attributes": map[string]any{"url": "https://store.lemonsqueezy.com/checkout/co_1"},
		}})
	}))
	defer ts.Close()
	old := api
	api = ts.URL
	defer func() { api = old }()

	p := &provider{key: "ls_key", store: "42", hc: &http.Client{Timeout: 5 * time.Second}}
	cs, err := p.CreateCheckoutSession(context.Background(), payment.CheckoutRequest{
		Customer: payment.Customer{Email: "a@b.c"}, Metadata: map[string]string{"variant_id": "9001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cs.ID != "co_1" || cs.URL == "" {
		t.Fatalf("unexpected session: %+v", cs)
	}
}

func TestCheckoutRequiresVariant(t *testing.T) {
	p := &provider{key: "k", store: "1", hc: http.DefaultClient}
	if _, err := p.CreateCheckoutSession(context.Background(), payment.CheckoutRequest{}); err == nil {
		t.Fatal("expected error when variant_id is missing")
	}
}

func TestHandleWebhookSignature(t *testing.T) {
	p := &provider{secret: "whsec"}
	body := []byte(`{"meta":{"event_name":"subscription_created"},"data":{"id":"sub_7"}}`)
	mac := hmac.New(sha256.New, []byte(p.secret))
	mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))

	ev, err := p.HandleWebhook(context.Background(), map[string]string{"X-Signature": good}, body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "subscription_created" || ev.ID != "sub_7" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if _, err := p.HandleWebhook(context.Background(), map[string]string{"X-Signature": "bad"}, body); err == nil {
		t.Fatal("expected invalid-signature error")
	}
}

var _ payment.PaymentProvider = (*provider)(nil)
