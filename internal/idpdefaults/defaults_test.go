package idpdefaults

import "testing"

func TestEndpointBytes(t *testing.T) {
	got := []string{AlipayGateway, QQAPI, WeiboAPI, FacebookGraphAPI, FacebookJWKS, GoogleJWKS}
	want := []string{"https://openapi.alipay.com/gateway.do", "https://graph.qq.com", "https://api.weibo.com", "https://graph.facebook.com", "https://www.facebook.com/.well-known/oauth/openid/jwks/", "https://www.googleapis.com/oauth2/v3/certs"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("endpoint %d = %q, want %q", i, got[i], want[i])
		}
	}
}
