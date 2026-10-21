package main

import (
	"context"
	"encoding/json"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/user"
)

// configureProfileAttributes 是可选的纯 Go 装配示例；宿主在 New 前显式调用。
func configureProfileAttributes(cfg *accountkit.Config) {
	cfg.ProfileAttributes = &user.ProfileAttributesConfig{SchemaVersion: "profile-v1", Schema: json.RawMessage(`{
 "$schema":"https://json-schema.org/draft/2020-12/schema",
 "type":"object","properties":{
   "theme":{"type":"string","enum":["light","dark"]},
   "preferences":{"type":"object","properties":{"newsletter":{"type":"boolean"}},"additionalProperties":false}
 },"additionalProperties":false
 }`)}
}

// setAuthorizedTheme 在宿主已确认目标用户和 theme 字段权限后调用。
func setAuthorizedTheme(ctx context.Context, svc *user.Service, userID string, requestID string) (user.ProfileAttributesView, error) {
	return svc.PatchProfileAttributes(ctx, userID, user.ProfileAttributesPatch{Document: json.RawMessage(`{"theme":"dark"}`)}, user.Meta{RequestID: requestID})
}
