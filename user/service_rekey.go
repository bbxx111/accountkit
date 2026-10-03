package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/user/db"
)

// KeyBackfillBatchSize 是密钥回填任务每批处理的身份行数。
const KeyBackfillBatchSize = 100

// backfillStep 描述一种回填：如何列出某个旧版本的行、如何改写一行(返回 CAS 命中行数)。
type backfillStep struct {
	name  string
	list  func(ctx context.Context, version uint16) ([]db.Identity, error)
	apply func(ctx context.Context, row db.Identity, now time.Time) (int64, error)
}

// runBackfill 对一个旧版本逐批执行 step：单行失败记 Error 日志(只带 identity_id)、计数、继续；
// 批结束返回首个错误且不再取下一批(坏行留在旧版本，下一轮重试；其余行仍会推进)。
func (s *Service) runBackfill(ctx context.Context, step backfillStep, old uint16) (int, error) {
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		rows, err := step.list(ctx, old)
		if err != nil {
			return total, fmt.Errorf("user: %s: list version %d: %w", step.name, old, err)
		}
		now := s.now()
		var firstErr error
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			n, err := step.apply(ctx, row, now)
			if err != nil {
				s.d.Logger.Error("user: key backfill failed for identity; will retry next round", "task", step.name, "identity_id", row.ID, "err", err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if n > 0 {
				total++
			}
		}
		if firstErr != nil {
			return total, fmt.Errorf("user: %s: %w", step.name, firstErr)
		}
		if len(rows) < KeyBackfillBatchSize {
			return total, nil
		}
	}
}

// decryptSubject 用行自带的加密版本解出锚点明文；缺密文或版本未配置即报错(不含明文)。
func (s *Service) decryptSubject(row db.Identity) (string, error) {
	if row.SubjectCiphertext == nil || row.CipherKeyVersion == nil {
		return "", errors.New("identity has no subject ciphertext")
	}
	return s.d.Cipher.Decrypt(row.SubjectCiphertext, uint16(*row.CipherKeyVersion))
}

// RekeyDigests（维护任务 rekey_digests，§5.6 #4）：把仍用非 active HMAC 版本的活跃锚点身份重算为 active 版本的摘要。
// 查找始终用 AllDigests 覆盖全部已配置版本，因此回填期间登录/绑定不受影响；改写用 CAS，与并发写互不覆盖。
func (s *Service) RekeyDigests(ctx context.Context) (int, error) {
	total := 0
	active := s.d.Digester.Active()
	for _, old := range s.d.Digester.Versions() {
		if old == active {
			continue
		}
		oldV := old
		n, err := s.runBackfill(ctx, backfillStep{
			name: "rekey_digests",
			list: func(ctx context.Context, v uint16) ([]db.Identity, error) {
				return s.d.Repo.Q().ListIdentitiesByDigestKeyVersion(ctx, db.ListIdentitiesByDigestKeyVersionParams{Version: int16(v), BatchSize: KeyBackfillBatchSize})
			},
			apply: func(ctx context.Context, row db.Identity, now time.Time) (int64, error) {
				plain, err := s.decryptSubject(row)
				if err != nil {
					return 0, err
				}
				digest, ver := s.d.Digester.Digest(plain)
				return s.d.Repo.Q().UpdateIdentityDigest(ctx, db.UpdateIdentityDigestParams{ID: row.ID, Digest: digest, Version: int16(ver), OldVersion: int16(oldV), Now: now})
			},
		}, old)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ReencryptSubjects（维护任务 reencrypt_subjects，§5.6 #5）：把仍用非 active 加密版本的活跃锚点身份的密文用 active 版本重新加密。
func (s *Service) ReencryptSubjects(ctx context.Context) (int, error) {
	total := 0
	active := s.d.Cipher.Active()
	for _, old := range s.d.Cipher.Versions() {
		if old == active {
			continue
		}
		oldV := old
		n, err := s.runBackfill(ctx, backfillStep{
			name: "reencrypt_subjects",
			list: func(ctx context.Context, v uint16) ([]db.Identity, error) {
				return s.d.Repo.Q().ListIdentitiesByCipherKeyVersion(ctx, db.ListIdentitiesByCipherKeyVersionParams{Version: int16(v), BatchSize: KeyBackfillBatchSize})
			},
			apply: func(ctx context.Context, row db.Identity, now time.Time) (int64, error) {
				plain, err := s.decryptSubject(row)
				if err != nil {
					return 0, err
				}
				ct, ver, err := s.d.Cipher.Encrypt(plain)
				if err != nil {
					return 0, err
				}
				return s.d.Repo.Q().UpdateIdentityCiphertext(ctx, db.UpdateIdentityCiphertextParams{ID: row.ID, Ciphertext: ct, Version: int16(ver), OldVersion: int16(oldV), Now: now})
			},
		}, old)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// CheckKeyVersions 断言 identity 活跃行使用的每个密钥版本都在配置内(§5.2 轮换第 3 步的硬约束)。
// 违反时返回包装 ErrUnknownKeyVersion 的错误，点名列与版本号；不含任何 PII。
func (s *Service) CheckKeyVersions(ctx context.Context) error {
	q := s.d.Repo.Q()
	digestUsed, err := q.ListActiveDigestKeyVersions(ctx)
	if err != nil {
		return fmt.Errorf("user: list digest key versions: %w", err)
	}
	if bad := unconfigured(digestUsed, s.d.Digester.Versions()); len(bad) > 0 {
		return fmt.Errorf("%w: identity.digest_key_version %v not in configured SubjectHMACKeys %v", ErrUnknownKeyVersion, bad, s.d.Digester.Versions())
	}
	cipherUsed, err := q.ListActiveCipherKeyVersions(ctx)
	if err != nil {
		return fmt.Errorf("user: list cipher key versions: %w", err)
	}
	if bad := unconfigured(cipherUsed, s.d.Cipher.Versions()); len(bad) > 0 {
		return fmt.Errorf("%w: identity.cipher_key_version %v not in configured SubjectCipherKeys %v", ErrUnknownKeyVersion, bad, s.d.Cipher.Versions())
	}
	return nil
}

// unconfigured 返回 used 中不在 configured 里的版本(升序，与查询顺序一致)。
func unconfigured(used []int16, configured []uint16) []int16 {
	set := make(map[uint16]struct{}, len(configured))
	for _, v := range configured {
		set[v] = struct{}{}
	}
	var bad []int16
	for _, v := range used {
		if _, ok := set[uint16(v)]; !ok {
			bad = append(bad, v)
		}
	}
	return bad
}
