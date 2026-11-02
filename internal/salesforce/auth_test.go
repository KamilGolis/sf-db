package salesforce

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	access  []byte
	display []byte
	err     error
}

func (f fakeRunner) Run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if strings.Contains(strings.Join(args, " "), "show-access-token") {
		return f.access, nil
	}
	return f.display, nil
}

func TestBootstrap(t *testing.T) {
	runner := fakeRunner{
		access:  []byte(`{"status":0,"result":{"accessToken":"token-123"}}`),
		display: []byte(`{"status":0,"result":{"instanceUrl":"https://example.my.salesforce.com/","apiVersion":"68.0"}}`),
	}
	tok, err := Bootstrap(context.Background(), runner, "sf", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "token-123" || tok.InstanceURL != "https://example.my.salesforce.com" || tok.APIVersion != "68.0" {
		t.Fatalf("unexpected token: %+v", tok)
	}
}

func TestBootstrapError(t *testing.T) {
	runner := fakeRunner{
		access:  []byte(`{"status":1,"message":"no org"}`),
		display: []byte(`{"status":0,"result":{"instanceUrl":"https://example.my.salesforce.com","apiVersion":"68.0"}}`),
	}
	if _, err := Bootstrap(context.Background(), runner, "sf", "dev"); err == nil {
		t.Fatal("expected error")
	}
}

func TestBootstrapRunnerError(t *testing.T) {
	runner := fakeRunner{err: errors.New("sf missing")}
	if _, err := Bootstrap(context.Background(), runner, "sf", "dev"); err == nil {
		t.Fatal("expected error")
	}
}
