package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenGroupSettingsRoundTripThroughRedisHashCache(t *testing.T) {
	useUserCacheMiniRedis(t)
	token := Token{
		Id:            42,
		UserId:        7,
		Key:           "token-auto-groups-cache-key",
		Name:          "auto-cache",
		Group:         "auto",
		AutoGroups:    `["vip","default"]`,
		AllowedGroups: `["priority"]`,
	}

	require.NoError(t, cacheSetTokenForTest(token))
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.AutoGroups, cached.AutoGroups)
	assert.Equal(t, token.AllowedGroups, cached.AllowedGroups)
	groups, err := cached.GetAutoGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"vip", "default"}, groups)
	allowedGroups, err := cached.GetAllowedGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"priority"}, allowedGroups)
}

func TestTokenCacheRefreshPreservesExistingHash(t *testing.T) {
	useUserCacheMiniRedis(t)
	token := Token{
		Id:        42,
		UserId:    7,
		Key:       "token-cache-refresh-key",
		Name:      "cache-refresh",
		UsedQuota: 0,
	}

	require.NoError(t, cacheSetTokenForTest(token))
	result, err := cacheInitToken(token)
	require.NoError(t, err)
	assert.Equal(t, 2, result, "existing hashes should only have their TTL refreshed")

	_, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err, "refreshing the TTL must not delete the cached token")
}

func TestTokenUpdateSynchronouslyNarrowsPreheatedAutoGroupsCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:          7,
		Key:             "token-auto-groups-update-cache-key",
		Name:            "auto-cache-update",
		Status:          common.TokenStatusEnabled,
		ExpiredTime:     -1,
		UnlimitedQuota:  true,
		Group:           "auto",
		CrossGroupRetry: true,
		AutoGroups:      `["default","vip"]`,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, cacheSetTokenForTest(token))

	preheated, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.JSONEq(t, `["default","vip"]`, preheated.AutoGroups)

	require.NoError(t, token.SetAutoGroups([]string{"vip"}))
	require.NoError(t, token.Update())
	// Update 是限制性变更：写库前删除缓存并设置 fence。缓存不再提供旧的
	// 宽分组值，下一次读取必须看到收紧后的分组。
	_, cacheErr := cacheGetTokenByKey(token.Key)
	require.Error(t, cacheErr, "the pre-update cache entry must be invalidated")
	reloaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.JSONEq(t, `["vip"]`, reloaded.AutoGroups)
}

// cacheSetTokenForTest 以测试身份写入完整 token 缓存（含额度字段），
// 模拟“已水合”的缓存状态。
func cacheSetTokenForTest(token Token) error {
	_, err := cacheInitToken(token)
	return err
}
