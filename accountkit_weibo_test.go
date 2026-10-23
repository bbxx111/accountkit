package accountkit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestWeiboProviderAssembly(t *testing.T) {
	providers, err := identityProviders(Config{WeiboAppIDs: []string{"1234567890"}}, Deps{})
	if err != nil || len(providers) != 1 {
		t.Fatalf("configured Weibo provider not assembled: %d %v", len(providers), err)
	}
	if _, ok := providers[0].(*idp.Weibo); !ok || providers[0].Kind() != enum.IdentityWeibo {
		t.Fatal("wrong provider")
	}
	if _, err := providers[0].Verify(context.Background(), json.RawMessage(`{"app_id":"1234567899","access_token":"t"}`)); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("provider configuration not used: %v", err)
	}
	if _, err := identityProviders(Config{WeiboAppIDs: []string{"bad"}}, Deps{}); err == nil {
		t.Fatal("assembly ignored invalid Weibo configuration")
	}
	if providers, err := identityProviders(Config{}, Deps{}); err != nil || len(providers) != 0 {
		t.Fatal("unconfigured Weibo should stay disabled")
	}
}
