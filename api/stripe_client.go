package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const defaultStripeAPIBaseURL = "https://api.stripe.com"

type StripeClient struct {
	SecretKey string
	BaseURL   string
	Client    *http.Client
}

type CreateStripeCheckoutSessionParams struct {
	PriceID    string
	SuccessURL string
	CancelURL  string
	Email      string
	Plan       string
	AccountID  string
	AppVersion string
}

type StripeCheckoutSession struct {
	ID                string                `json:"id"`
	URL               string                `json:"url"`
	Customer          string                `json:"customer"`
	CustomerEmail     string                `json:"customer_email"`
	CustomerDetails   StripeCustomerDetails `json:"customer_details"`
	Subscription      string                `json:"subscription"`
	PaymentStatus     string                `json:"payment_status"`
	Status            string                `json:"status"`
	ClientReferenceID string                `json:"client_reference_id"`
	Metadata          map[string]string     `json:"metadata"`
}

type StripeCustomerDetails struct {
	Email string `json:"email"`
}

type StripePortalSession struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func (c StripeClient) CreateSubscriptionCheckoutSession(ctx context.Context, params CreateStripeCheckoutSessionParams) (StripeCheckoutSession, error) {
	values := url.Values{}
	values.Set("line_items[0][price]", params.PriceID)
	values.Set("line_items[0][quantity]", "1")
	values.Set("mode", "subscription")
	values.Set("success_url", params.SuccessURL)
	values.Set("cancel_url", params.CancelURL)
	values.Set("metadata[plan]", params.Plan)
	values.Set("subscription_data[metadata][plan]", params.Plan)
	if params.AccountID != "" {
		values.Set("client_reference_id", params.AccountID)
		values.Set("metadata[account_id]", params.AccountID)
		values.Set("subscription_data[metadata][account_id]", params.AccountID)
	}
	if params.AppVersion != "" {
		values.Set("metadata[app_version]", params.AppVersion)
	}
	if strings.TrimSpace(params.Email) != "" {
		values.Set("customer_email", strings.TrimSpace(params.Email))
	}

	var session StripeCheckoutSession
	if err := c.postForm(ctx, "/v1/checkout/sessions", values, &session); err != nil {
		return session, err
	}
	if session.ID == "" || session.URL == "" {
		return session, fmt.Errorf("stripe checkout session response missing id or url")
	}
	return session, nil
}

func (c StripeClient) GetCheckoutSession(ctx context.Context, sessionID string) (StripeCheckoutSession, error) {
	var session StripeCheckoutSession
	err := c.get(ctx, "/v1/checkout/sessions/"+url.PathEscape(sessionID), nil, &session)
	return session, err
}

func (c StripeClient) CreatePortalSession(ctx context.Context, customerID string, returnURL string) (StripePortalSession, error) {
	values := url.Values{}
	values.Set("customer", customerID)
	values.Set("return_url", returnURL)
	var session StripePortalSession
	if err := c.postForm(ctx, "/v1/billing_portal/sessions", values, &session); err != nil {
		return session, err
	}
	if session.URL == "" {
		return session, fmt.Errorf("stripe portal session response missing url")
	}
	return session, nil
}

func (c StripeClient) postForm(ctx context.Context, path string, values url.Values, out any) error {
	return c.do(ctx, http.MethodPost, path, values, out)
}

func (c StripeClient) get(ctx context.Context, path string, values url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, values, out)
}

func (c StripeClient) do(ctx context.Context, method string, path string, values url.Values, out any) error {
	secret := strings.TrimSpace(c.SecretKey)
	if secret == "" {
		return fmt.Errorf("stripe secret key is not configured")
	}
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultStripeAPIBaseURL
	}
	target := baseURL + path
	var body io.Reader
	if method == http.MethodGet {
		if encoded := values.Encode(); encoded != "" {
			target += "?" + encoded
		}
	} else {
		body = strings.NewReader(values.Encode())
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(secret, "")
	req.Header.Set("Accept", "application/json")
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var stripeErr struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &stripeErr)
		message := strings.TrimSpace(stripeErr.Error.Message)
		if message == "" {
			message = strings.TrimSpace(string(responseBody))
		}
		return fmt.Errorf("stripe request failed: status=%d type=%s code=%s message=%s", resp.StatusCode, stripeErr.Error.Type, stripeErr.Error.Code, message)
	}
	return json.Unmarshal(responseBody, out)
}
