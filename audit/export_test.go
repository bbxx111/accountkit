package audit

import (
	"context"

	"github.com/bbxx111/accountkit/audit/db"
	"github.com/bbxx111/accountkit/ids"
)

// InsertBatchWithIDs 用给定 id 写入（测试用：制造主键冲突以验证整批回滚与失败计数）。
func InsertBatchWithIDs(ctx context.Context, s *Store, ids []string, events []Event) (int, error) {
	params := make([]db.InsertAuditEventsParams, 0, len(events))
	for i, e := range events {
		params = append(params, toParams(ids[i], e))
	}
	return s.insert(ctx, params)
}

// SetNewIDForTest 临时替换 id 生成的测试替换点 newID，返回一个恢复原实现（ids.New）的
// 函数；调用方应以 t.Cleanup(restore) 保证测试结束后复原，避免污染其他测试。
func SetNewIDForTest(f func(ids.Kind) (string, error)) (restore func()) {
	prev := newID
	newID = f
	return func() { newID = prev }
}
