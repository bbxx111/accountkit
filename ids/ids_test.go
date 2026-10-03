package ids_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/ids"
)

func TestNewHasPrefixAndLength(t *testing.T) {
	for _, k := range []ids.Kind{ids.User, ids.Identity, ids.Session, ids.AuditEvent} {
		id, err := ids.New(k)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(id, string(k)+"_") || len(id) != len(k)+1+ids.TSIDLength {
			t.Fatalf("kind %s: id %q has wrong shape", k, id)
		}
		if !ids.Valid(k, id) {
			t.Fatalf("New output must be Valid: %q", id)
		}
	}
}

func TestValidRejectsWrongKindCaseAndAlphabet(t *testing.T) {
	id, _ := ids.New(ids.User)
	if ids.Valid(ids.Session, id) {
		t.Fatal("u_ id must not be valid as a session id")
	}
	if ids.Valid(ids.User, strings.ToUpper(id)) {
		t.Fatal("uppercase must be invalid")
	}
	bad := "u_" + strings.Repeat("i", ids.TSIDLength) // i 不在字符集
	if ids.Valid(ids.User, bad) {
		t.Fatal("letters outside Crockford alphabet must be invalid")
	}
	if ids.Valid(ids.User, "u_"+strings.Repeat("0", ids.TSIDLength-1)) || ids.Valid(ids.User, "u_"+strings.Repeat("0", ids.TSIDLength+1)) {
		t.Fatal("wrong length must be invalid")
	}
	if ids.Valid(ids.User, "") || ids.Valid(ids.User, "u_") || ids.Valid(ids.User, "u"+strings.Repeat("0", ids.TSIDLength+1)) {
		t.Fatal("missing body / missing underscore must be invalid")
	}
}

func TestPatternMatchesValidAndIsAnchored(t *testing.T) {
	re := regexp.MustCompile(ids.Pattern(ids.Identity))
	id, _ := ids.New(ids.Identity)
	if !re.MatchString(id) {
		t.Fatalf("pattern %q must match %q", ids.Pattern(ids.Identity), id)
	}
	if re.MatchString(id+"x") || re.MatchString("x"+id) {
		t.Fatal("pattern must be anchored")
	}
	if ids.Pattern(ids.User) != `^u_[0-9a-hjkmnp-tv-z]{13}$` {
		t.Fatalf("Pattern(User) = %q", ids.Pattern(ids.User))
	}
}

func TestNewIsTimeOrderedAcrossMilliseconds(t *testing.T) {
	a, _ := ids.New(ids.User)
	// 13 字符中的前 8 个字符（去掉 "u_" 前缀后 [0:8]）编码的是时间戳的高 38 位，即 ms>>3，
	// 每 8ms 变化一次；睡眠 10ms 保证跨过一个 8ms 边界，因此前缀必须严格递增。
	time.Sleep(10 * time.Millisecond)
	b, _ := ids.New(ids.User)
	ta, tb := a[2:10], b[2:10]
	if !(tb > ta) {
		t.Fatalf("38-bit timestamp prefix must increase after 10ms: %q vs %q", ta, tb)
	}
}

// 同一毫秒内的随机位应当变化。64 次抽样落在 2^22 的随机空间里，出现任一碰撞的概率
// 约为 64·63/2 / 2^22 ≈ 4.8e-4；阈值 60 允许最多 4 次碰撞，只用于捕获"随机位根本不变"
// 这类严重故障。全局唯一性不是生成器的承诺，由主键唯一约束 + 调用方重试保证（设计文档 §3.1）。
func TestNewRandomBitsVary(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		id, err := ids.New(ids.Session)
		if err != nil {
			t.Fatal(err)
		}
		seen[id] = struct{}{}
	}
	if len(seen) < 60 {
		t.Fatalf("only %d distinct ids in 64 draws; random bits are not varying", len(seen))
	}
}
