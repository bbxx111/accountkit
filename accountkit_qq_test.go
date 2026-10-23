package accountkit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestQQProviderAssembly(t *testing.T) {
	providers, err := identityProviders(Config{QQAppIDs: []string{"1100000001"}}, Deps{})
	if err != nil || len(providers) != 1 {
		t.Fatalf("configured QQ provider not assembled: %d %v", len(providers), err)
	}
	if _, ok := providers[0].(*idp.QQ); !ok || providers[0].Kind() != enum.IdentityQQ {
		t.Fatal("wrong provider")
	}
	if _, err := providers[0].Verify(context.Background(), json.RawMessage(`{"app_id":"1100000009","access_token":"t"}`)); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("provider configuration not used: %v", err)
	}
	if _, err := identityProviders(Config{QQAppIDs: []string{"bad"}}, Deps{}); err == nil {
		t.Fatal("assembly ignored invalid QQ configuration")
	}
	if providers, err := identityProviders(Config{}, Deps{}); err != nil || len(providers) != 0 {
		t.Fatal("unconfigured QQ should stay disabled")
	}
}
