package perfmetrics

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-redis/redis/v8"
)

// Channel usage contains only anonymous health counters, independent of
// consume logging and model performance settings. Ten minute buckets expire.
type ChannelUsage struct {
	Requests       int64
	Successes      int64
	TotalLatencyMS int64
	LastRequestAt  int64
}

type channelUsageKey struct {
	channelID int
	minute    int64
}

var channelUsageMu sync.Mutex
var channelUsageBuckets = map[channelUsageKey]ChannelUsage{}
var channelUsagePrunedAt int64

var channelUsageScript = redis.NewScript(`
redis.call('HINCRBY', KEYS[1], 'requests', 1)
redis.call('HINCRBY', KEYS[1], 'successes', ARGV[1])
redis.call('HINCRBY', KEYS[1], 'latency', ARGV[2])
local previous = tonumber(redis.call('HGET', KEYS[1], 'last') or '0')
if tonumber(ARGV[3]) > previous then redis.call('HSET', KEYS[1], 'last', ARGV[3]) end
redis.call('EXPIRE', KEYS[1], 660)
return 1
`)

// RecordChannelResult records each actual upstream attempt, including retries.
// Probes, client cancellations and business rejections never count as traffic.
func RecordChannelResult(ctx context.Context, channelID int, info *relaycommon.RelayInfo, apiErr *types.NewAPIError, started time.Time) {
	if info == nil || info.IsChannelTest || channelID <= 0 {
		return
	}
	outcome := ClassifyRelayOutcome(ctx, info, apiErr)
	if outcome == OutcomeIgnored {
		return
	}
	recordChannelUsage(channelID, outcome == OutcomeSuccess, time.Since(started).Milliseconds(), time.Now())
}

func recordChannelUsage(channelID int, successful bool, latencyMS int64, now time.Time) {
	if channelID <= 0 {
		return
	}
	stamp := now.Unix()
	minute := stamp - stamp%60
	key := channelUsageKey{channelID: channelID, minute: minute}
	success := int64(0)
	if successful {
		success = 1
	}
	latencyMS = max(latencyMS, 0)
	channelUsageMu.Lock()
	if channelUsagePrunedAt != minute {
		for old := range channelUsageBuckets {
			if old.minute < minute-9*60 {
				delete(channelUsageBuckets, old)
			}
		}
		channelUsagePrunedAt = minute
	}
	usage := channelUsageBuckets[key]
	usage.Requests++
	usage.Successes += success
	usage.TotalLatencyMS += latencyMS
	usage.LastRequestAt = max(usage.LastRequestAt, stamp)
	channelUsageBuckets[key] = usage
	channelUsageMu.Unlock()
	if common.RedisEnabled && common.RDB != nil {
		gopool.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = channelUsageScript.Run(ctx, common.RDB, []string{fmt.Sprintf("channel-status:usage:%d:%d", channelID, minute)}, success, latencyMS, stamp).Err()
		})
	}
}

// RecentChannelUsage uses Redis counters across nodes when available, with a
// local fallback. Only the requested monitored channels can appear in results.
func RecentChannelUsage(channelIDs []int, now time.Time) map[int]ChannelUsage {
	minute := now.Unix() - now.Unix()%60
	cutoff := minute - 9*60
	result := map[int]ChannelUsage{}
	allowed := map[int]bool{}
	for _, id := range channelIDs {
		allowed[id] = true
	}
	channelUsageMu.Lock()
	for key, value := range channelUsageBuckets {
		if key.minute < cutoff {
			delete(channelUsageBuckets, key)
			continue
		}
		if !allowed[key.channelID] || key.minute > minute {
			continue
		}
		usage := result[key.channelID]
		usage.Requests += value.Requests
		usage.Successes += value.Successes
		usage.TotalLatencyMS += value.TotalLatencyMS
		usage.LastRequestAt = max(usage.LastRequestAt, value.LastRequestAt)
		result[key.channelID] = usage
	}
	channelUsageMu.Unlock()
	if !common.RedisEnabled || common.RDB == nil || len(allowed) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pipe := common.RDB.Pipeline()
	queries := map[int][]*redis.StringStringMapCmd{}
	for id := range allowed {
		for stamp := cutoff; stamp <= minute; stamp += 60 {
			queries[id] = append(queries[id], pipe.HGetAll(ctx, fmt.Sprintf("channel-status:usage:%d:%d", id, stamp)))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return result
	}
	merged := map[int]ChannelUsage{}
	for id, commands := range queries {
		for _, command := range commands {
			values := command.Val()
			if len(values) == 0 {
				continue
			}
			requests, _ := strconv.ParseInt(values["requests"], 10, 64)
			successes, _ := strconv.ParseInt(values["successes"], 10, 64)
			latency, _ := strconv.ParseInt(values["latency"], 10, 64)
			last, _ := strconv.ParseInt(values["last"], 10, 64)
			usage := merged[id]
			usage.Requests += requests
			usage.Successes += successes
			usage.TotalLatencyMS += latency
			usage.LastRequestAt = max(usage.LastRequestAt, last)
			merged[id] = usage
		}
	}
	// Writes to Redis are intentionally asynchronous so request latency is not
	// tied to the metrics store. Until that write completes, keep the local
	// sample visible instead of replacing it with an empty Redis result. If
	// Redis is briefly behind the local process, preserve the local counters
	// until the async write catches up.
	for id, local := range result {
		remote, exists := merged[id]
		if !exists || remote.Requests == 0 {
			merged[id] = local
			continue
		}
		if remote.Requests < local.Requests {
			remote.Requests = local.Requests
		}
		// Redis can contain an older sample with the same request count when
		// the local process has just restarted or its async write is pending.
		// Preserve local fields that are ahead without double-counting a remote
		// aggregate that already includes this process.
		remote.Successes = max(remote.Successes, local.Successes)
		remote.TotalLatencyMS = max(remote.TotalLatencyMS, local.TotalLatencyMS)
		remote.LastRequestAt = max(remote.LastRequestAt, local.LastRequestAt)
		merged[id] = remote
	}
	return merged
}
