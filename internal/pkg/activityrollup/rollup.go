// Package activityrollup 把一台机器上的会话压成「按天 × 维度」的计数。
//
// 它是活跃统计上报的**唯一**取数形状:交出去的只有日期、维度和一个计数,没有标题、
// 没有路径、没有对话内容。这条边界不是实现细节 —— 它就是那个开关向用户承诺的东西,
// 所以聚合在这里一次做完,调用方拿不到原始行。
//
// 本包是纯函数:不认识数据库、不认识 wire。桌面端读 chat_sessions、agentred 读
// daemon_sessions,各自把行摊成 Activity 再进来,分桶规则只有这一份。
package activityrollup

import (
	"sort"
	"time"
)

// dayLayout 是日界的字面形式。它同时是上报、落库与热力图格子的键。
const dayLayout = "2006-01-02"

// Activity 是一条会话在计数口径下的全部信息。
//
// 五个维度都是账号级的不透明标识或枚举,没有一个是路径。空串在每一个维度上都是
// **有含义的值**(provider/model 皆空 = 跟随 Agent 绑定;project 空 = 未归属项目),
// 不是「缺失待补」。
type Activity struct {
	// CreatedAt 是这条会话**建立**的时刻(Unix 毫秒),它决定这条会话落在哪一天。
	// 0 = 对端没记建立时刻,这样的会话不计数(算成 1970-01-01 会在格子图最左端凭空
	// 长出一块假数据)。
	CreatedAt int64
	// LastMessageAt 是这条会话最后一次活动的时刻(Unix 毫秒)。它在这里只作**闸门**:
	// 0 = 一轮都没跑过,那不是「一条对话」,不计数。它**不决定**落在哪一天 —— 见
	// Aggregate 的注释。
	LastMessageAt int64
	AgentSyncID   string
	BackendType   string
	ProviderKey   string
	ModelKey      string
	ProjectSyncID string
}

// Bucket 是**那天建立的**、某个维度组合下的会话数。
//
// 一条会话只落进**一个**组合,所以按任意维度子集求和都是对的 —— 服务端因此能用同
// 一张表同时画热力图(只按 Day 求和)和三张分布卡(按各自维度求和)。
type Bucket struct {
	Day           string
	AgentSyncID   string
	BackendType   string
	ProviderKey   string
	ModelKey      string
	ProjectSyncID string
	SessionCount  int32
}

// Aggregate 把 items 压成按 (天 × 维度组合) 的计数。
//
// **「哪一天」是会话建立的那天,不是它最后活动的那天。** 这一条决定了整条通道收不收
// 敛:
//
//   - 按最后活动日分桶,同一条会话每被续一轮就换一天,而增量拉取的下界(since_day)
//     会越过它原来那天再也不回去 —— 一条用了三十天的对话在库里留下三十行、每行 1,
//     而界面上写的是「累计 30 条」。
//   - 反过来,一次性回填只看得见每条会话最后那天。同一台机器、同一份数据,回填与增量
//     给出两份对不上的历史。
//
// 建立时刻不会变,所以两条都不成立:任何一天的计数一旦定下就是终值,回填与增量必然
// 一致。代价是格子图读作「这天开了几条对话」而不是「这天有几条对话在动」—— 一条跨了
// 一周的长对话只在它开始那天亮一格。
//
// 一个已知的边角:一条很久以前建立、直到今天才跑第一轮的会话,会被 sinceDay 挡在外面
// (它的建立日早于下界),从此不计数。这是「过去的日子是终值」的必然代价,而它罕见。
//
// loc 是切日界用的时区:同一个 UTC 时刻在不同时区属于不同的一天,而格子图回答的是
// 「我哪天干了活」。nil 视作 UTC。
//
// sinceDay 是**闭区间**下界(dayLayout 格式),空串表示不设下界。闭区间是必须的:当天
// 的计数在一天之内还会变,排除下界那天会让增量拉取永远少最后一天。
//
// 返回按 (Day, Agent, Backend, Provider, Model, Project) 升序,顺序稳定 —— 上报是
// 幂等 upsert,顺序不该成为两次上报之间的差异来源。
func Aggregate(items []Activity, loc *time.Location, sinceDay string) []Bucket {
	if loc == nil {
		loc = time.UTC
	}
	counts := make(map[Bucket]int32, len(items))
	for _, item := range items {
		if item.CreatedAt <= 0 || item.LastMessageAt <= 0 {
			// 没有建立时刻 = 落不到任何一天;一轮都没跑过 = 建了但没用过,不是一条
			// 对话。两者都不计数。
			continue
		}
		day := time.UnixMilli(item.CreatedAt).In(loc).Format(dayLayout)
		if sinceDay != "" && day < sinceDay {
			// 字符串比较对 YYYY-MM-DD 就是日期序,不必解析回 time。
			continue
		}
		counts[Bucket{
			Day:           day,
			AgentSyncID:   item.AgentSyncID,
			BackendType:   item.BackendType,
			ProviderKey:   item.ProviderKey,
			ModelKey:      item.ModelKey,
			ProjectSyncID: item.ProjectSyncID,
		}]++
	}

	out := make([]Bucket, 0, len(counts))
	for key, count := range counts {
		key.SessionCount = count
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Day != b.Day:
			return a.Day < b.Day
		case a.AgentSyncID != b.AgentSyncID:
			return a.AgentSyncID < b.AgentSyncID
		case a.BackendType != b.BackendType:
			return a.BackendType < b.BackendType
		case a.ProviderKey != b.ProviderKey:
			return a.ProviderKey < b.ProviderKey
		case a.ModelKey != b.ModelKey:
			return a.ModelKey < b.ModelKey
		default:
			return a.ProjectSyncID < b.ProjectSyncID
		}
	})
	return out
}
