package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/user"
)

// configureExtensions 是可选的纯 Go 装配示例；宿主在 New 前显式调用。
func configureExtensions(cfg *accountkit.Config) {
	cfg.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "profile-v1", Schema: json.RawMessage(`{
 "$schema":"https://json-schema.org/draft/2020-12/schema",
 "type":"object","properties":{
   "theme":{"type":"string","enum":["light","dark"]},
   "preferences":{"type":"object","properties":{"newsletter":{"type":"boolean"}},"additionalProperties":false}
 },"additionalProperties":false
 }`)}
}

// setAuthorizedTheme 在宿主已确认目标用户和 theme 字段权限后调用。
func setAuthorizedTheme(ctx context.Context, svc *user.Service, userID string, expected int64, requestID string) (user.ExtensionsView, error) {
	v, err := svc.PatchExtensions(ctx, userID, user.ExtensionsPatch{Document: json.RawMessage(`{"theme":"dark"}`), ExpectedRevision: &expected}, user.Meta{RequestID: requestID})
	if errors.Is(err, user.ErrExtensionsConflict) {
		// 交还宿主读取最新快照、重新决定修改；不能自动覆盖或盲目重放。
		return user.ExtensionsView{}, err
	}
	return v, err
}
