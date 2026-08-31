package conversationid_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
)

// Given 同一进程连铸两个新对话 id,When 检查它们,Then 两个都是合法的 UUIDv7 且互不相同。
// v7 的价值就是无需协调即全局唯一(决策 1),所以"能铸出来"与"不撞"是它唯一要证的事。
func TestNew_GivenTwoMints_ThenBothAreDistinctVersion7UUIDs(t *testing.T) {
	first, err := conversationid.New()
	require.NoError(t, err)
	second, err := conversationid.New()
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	for _, id := range []string{first, second} {
		parsed, parseErr := uuid.Parse(id)
		require.NoError(t, parseErr, id)
		assert.Equal(t, uuid.Version(7), parsed.Version(), id)
		assert.NoError(t, conversationid.Validate(id))
	}
}

// Given 决策 2 的一组固定输入向量,When 派生存量对话 id,Then 逐位等于钉死的期望值。
//
// 这是决策 2 确定性的**唯一机械保证**:桌面端 / agentred / server 三个仓库各持一份存量、
// 迁移时互不通信,只有三边独立算出同一个值,镜像存量才不会全体成孤儿。这张表里的
// namespace 与期望输出必须与另外两个仓库逐字相同,改动它等于宣布一次不兼容迁移。
func TestDerive_GivenTheDecisionTwoVectors_ThenMatchesTheCrossRepositoryExpectation(t *testing.T) {
	assert.Equal(t, "44d41290-935a-525a-853c-81d0e171598e", conversationid.Namespace.String())

	cases := []struct {
		fingerprint string
		sessionID   string
		want        string
	}{
		{"sha256:aaaa", "1", "dd5414f5-0877-5e9d-9656-b3b44e49697f"},
		{"sha256:aaaa", "2", "4d7f58e9-9881-5189-a9cd-b62f817db549"},
		{"sha256:bbbb", "1", "88f2b427-8035-57d5-8e8b-64fa700ea77a"},
		// 空指纹是"未认领 daemon / 自己对端"那条路径上的合法输入,它同样必须确定。
		{"", "1", "d7bb9a66-20f7-5477-9ecd-cec26ec3d769"},
	}
	for _, c := range cases {
		got := conversationid.Derive(conversationid.Namespace, c.fingerprint, c.sessionID)
		assert.Equal(t, c.want, got, "%q/%q", c.fingerprint, c.sessionID)
		assert.NoError(t, conversationid.Validate(got))
		assert.Equal(t, uuid.Version(5), uuid.MustParse(got).Version())
	}
}

// Given 两条只在分隔位置不同的输入,When 派生,Then 得到不同的 id ——
// 拼接必须带分隔符,否则 ("ab","1") 与 ("a","b1") 会撞成同一条对话。
func TestDerive_GivenInputsThatOnlyDifferInFieldBoundary_ThenDoesNotCollide(t *testing.T) {
	assert.NotEqual(t,
		conversationid.Derive(conversationid.Namespace, "ab", "1"),
		conversationid.Derive(conversationid.Namespace, "a", "b1"),
	)
}

// Given 各式各样的线上取值,When 校验,Then 只有规范形式的 uuid 通过。
// daemon 侧 7 处 `sessionID <= 0` 的合法性判据整体换成它,所以"旧的 int64 会话号"
// 必须落在拒绝一侧 —— 否则换判据等于没换。
func TestValidate_GivenNonCanonicalValues_ThenRejectsThem(t *testing.T) {
	valid, err := conversationid.New()
	require.NoError(t, err)
	assert.NoError(t, conversationid.Validate(valid))
	assert.NoError(t, conversationid.Validate(conversationid.Derive(conversationid.Namespace, "sha256:aaaa", "1")))

	for _, bad := range []string{
		"",
		"0",
		"42",
		"-1",
		"not-a-uuid",
		"DD5414F5-0877-5E9D-9656-B3B44E49697F",   // 大写:线上只认小写规范形式
		"{dd5414f5-0877-5e9d-9656-b3b44e49697f}", // 花括号变体
		"urn:uuid:dd5414f5-0877-5e9d-9656-b3b44e49697f", // urn 变体
		"dd5414f508775e9d9656b3b44e49697f",              // 无连字符变体
		"dd5414f5-0877-5e9d-9656-b3b44e49697f ",         // 尾随空白
		"00000000-0000-0000-0000-000000000000",          // 全零:能解析,但不指称任何对话
	} {
		assert.Error(t, conversationid.Validate(bad), "%q", bad)
	}
}
