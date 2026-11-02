package salesforce

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Provider bootstraps a Token from the sf CLI.
type Provider struct {
	Runner Runner
	Bin    string
	Org    string
}

// Token implements the token provider contract used by Client.
func (p Provider) Token(ctx context.Context) (Token, error) {
	return Bootstrap(ctx, p.Runner, p.Bin, p.Org)
}

type runResult struct {
	out []byte
	err error
}

// Bootstrap runs two sf commands in parallel: one for the access token and one
// for the instance URL. It is the only place the clone needs the sf CLI.
func Bootstrap(ctx context.Context, runner Runner, bin, org string) (Token, error) {
	if runner == nil {
		runner = ExecRunner{}
	}

	if strings.TrimSpace(org) == "" {
		return Token{}, fmt.Errorf("org is required")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	accessCh := make(chan runResult, 1)
	displayCh := make(chan runResult, 1)

	go func() {
		out, err := runner.Run(ctx, bin, "org", "auth", "show-access-token", "--target-org", org, "--json")
		accessCh <- runResult{out: out, err: err}
	}()

	go func() {
		out, err := runner.Run(ctx, bin, "org", "display", "--target-org", org, "--json")
		displayCh <- runResult{out: out, err: err}
	}()

	access := <-accessCh
	display := <-displayCh

	if access.err != nil {
		return Token{}, fmt.Errorf("read access token: %w", access.err)
	}

	var accessResp cliResponse[accessTokenResult]

	if err := json.Unmarshal(access.out, &accessResp); err != nil {
		return Token{}, fmt.Errorf("parse access token response: %w", err)
	}

	if accessResp.Status != 0 || accessResp.Result.AccessToken == "" {
		return Token{}, fmt.Errorf("access token response: %s", messageOr(accessResp.Message, "empty token"))
	}

	if display.err != nil {
		return Token{}, fmt.Errorf("read org display: %w", display.err)
	}

	var displayResp cliResponse[orgDisplayResult]

	if err := json.Unmarshal(display.out, &displayResp); err != nil {
		return Token{}, fmt.Errorf("parse org display response: %w", err)
	}

	if displayResp.Status != 0 || displayResp.Result.InstanceURL == "" {
		return Token{}, fmt.Errorf("org display response: %s", messageOr(displayResp.Message, "empty instance URL"))
	}

	instanceURL := strings.TrimRight(displayResp.Result.InstanceURL, "/")

	if _, err := url.ParseRequestURI(instanceURL); err != nil {
		return Token{}, fmt.Errorf("invalid instance URL %q: %w", instanceURL, err)
	}

	apiVersion := displayResp.Result.APIVersion
	if apiVersion == "" {
		apiVersion = "68.0"
	}

	return Token{
		AccessToken: accessResp.Result.AccessToken,
		InstanceURL: instanceURL,
		APIVersion:  apiVersion,
	}, nil
}

func messageOr(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	
	return message
}
