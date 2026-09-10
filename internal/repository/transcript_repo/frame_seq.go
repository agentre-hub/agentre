package transcript_repo

import (
	"context"
	"fmt"
	"sort"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/pkg/transcript"
)

// FrameKey 定位一个持久帧在转录里的位置：它由哪条消息的哪个块的第几帧投影而来。
//
// 它就是 transcript.FrameKey：位置由「块 → 帧」的投影器自己给出，台账只负责给这个
// 位置记一个号。别名而不是第二个同形结构体 —— 两份定义一旦有人多加一格，编译器不会
// 说任何话（规格 2026-09-05「复用边界」）。
type FrameKey = transcript.FrameKey

// FrameSeqRow 是台账里的一行：一次编号分配。
type FrameSeqRow struct {
	SessionID int64 `gorm:"column:session_id;type:bigint;not null"`
	MessageID int64 `gorm:"column:message_id;type:bigint;not null"`
	BlockIdx  int   `gorm:"column:block_idx;type:int;not null"`
	Ordinal   int   `gorm:"column:ordinal;type:int;not null"`
	Seq       int64 `gorm:"column:seq;type:bigint;not null"`
}

// TableName 绑定表名。
func (*FrameSeqRow) TableName() string { return "chat_frame_seqs" }

//go:generate mockgen -source frame_seq.go -destination mock_transcript_repo/mock_frame_seq.go

// FrameSeqRepo 是对端帧编号的台账（决策 3）。
//
// 分配与落库不可分：Allocate 是唯一的取号入口，它在一个事务里读出当前末尾、写下新
// 的分配行；写失败即没有分配，调用方据此**不发布**这一帧 —— 否则对端会持有一个宿主
// 认不回来的号。
type FrameSeqRepo interface {
	// Load 取回这条对话已分配过的全部编号，按帧位置归并。
	//
	// 同一个位置被原地修补过多次时留下多行，交回 seq 最大的那一行：它才是该位置**当前
	// 内容**的号，也正是对端最后一次实时收到的那个号。
	Load(ctx context.Context, sessionID int64) (map[FrameKey]int64, error)
	// Allocate 依次为 keys 取下一个号并落库，返回与 keys 一一对应的编号。
	// keys 为空时不发查询。
	Allocate(ctx context.Context, sessionID int64, keys []FrameKey) ([]int64, error)
	// DeleteBySession 清掉这条会话的整本台账，返回删除行数。
	//
	// 它是会话删除的一部分：转录消失了，给它的每一帧记号的那些行就没有主人了
	// （规格「生命周期与删除」：身份行与它的全部转录一并消失）。
	DeleteBySession(ctx context.Context, sessionID int64) (int64, error)
}

var defaultFrameSeq FrameSeqRepo = NewFrameSeq()

func FrameSeq() FrameSeqRepo             { return defaultFrameSeq }
func RegisterFrameSeq(impl FrameSeqRepo) { defaultFrameSeq = impl }
func NewFrameSeq() FrameSeqRepo          { return &frameSeqRepo{} }

type frameSeqRepo struct{}

func (r *frameSeqRepo) Load(ctx context.Context, sessionID int64) (map[FrameKey]int64, error) {
	var rows []*FrameSeqRow
	if err := db.Ctx(ctx).Model(&FrameSeqRow{}).
		Where("session_id = ?", sessionID).
		Order("seq ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[FrameKey]int64, len(rows))
	for _, row := range rows {
		// 按 seq 升序扫描,后写的覆盖先写的 —— 落在同一位置的多次分配里,最后一次胜出。
		out[FrameKey{MessageID: row.MessageID, BlockIdx: row.BlockIdx, Ordinal: row.Ordinal}] = row.Seq
	}
	return out, nil
}

// NumberFrames 给一整条转录的持久帧配上编号，并按 seq 升序重排。
//
// 已有编号的帧原样沿用 —— 这正是「宿主重启后同一份内容仍是同一个 seq」。存量对话
// （台账里一行都没有）在这里**惰性补齐**：按投影顺序依次取号并落库，没被访问过的
// 对话不付出任何代价（规格「帧编号」）。
//
// 两个宿主共用这一份：桌面端的对端发布与 agentred 的补齐读侧都要把同一段内容映到
// 同一串号上，写两遍等于让两台机器各自决定「第几号是什么」。
func NumberFrames(ctx context.Context, sessionID int64, keyed []transcript.KeyedFrame) error {
	ledger, err := FrameSeq().Load(ctx, sessionID)
	if err != nil {
		return err
	}
	missing := make([]FrameKey, 0, len(keyed))
	missingAt := make([]int, 0, len(keyed))
	for index := range keyed {
		if seq, ok := ledger[keyed[index].Key]; ok {
			keyed[index].Frame.Seq = seq
			continue
		}
		missing = append(missing, keyed[index].Key)
		missingAt = append(missingAt, index)
	}
	seqs, err := FrameSeq().Allocate(ctx, sessionID, missing)
	if err != nil {
		return err
	}
	if len(seqs) != len(missing) {
		return fmt.Errorf("frame seq allocation returned %d numbers for %d frames", len(seqs), len(missing))
	}
	for i, index := range missingAt {
		keyed[index].Frame.Seq = seqs[i]
	}
	// 排完序按 seq 走:原地修补新增的那一帧取的是末尾新号,于是它出现在自己的 request
	// 帧之后若干位 —— 与实时流序一致(规格「帧编号」)。
	sort.SliceStable(keyed, func(i, j int) bool { return keyed[i].Frame.Seq < keyed[j].Frame.Seq })
	return nil
}

// PredictLatestSeq 交回「这条转录**如果**现在被编号，末尾会是哪个号」——不写一行。
//
// 分配是 latest+1 … latest+n 的连续取号（见 Allocate），所以未编号帧数加上台账当前
// 的末尾就是答案，与真去分配一次逐字相同。会话清单那条 RPC 规格明写无副作用，而它
// 要报的正是这个高水位：拿 NumberFrames 去回答它，等于让一次只读探测替**每一条**
// 对话补齐编号，「未被访问的对话不付出任何代价」当场破掉（规格「帧编号」）。
func PredictLatestSeq(ctx context.Context, sessionID int64, keyed []transcript.KeyedFrame) (int64, error) {
	ledger, err := FrameSeq().Load(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	unnumbered := 0
	for index := range keyed {
		if _, ok := ledger[keyed[index].Key]; !ok {
			unnumbered++
		}
	}
	// 末尾号取整本台账的 MAX(seq),不是「当前内容里最大的那个」:被顶掉的旧分配行
	// 同样占着号(计数器只增不减,见 Allocate)。只数当前内容会让预测值低于下一次真
	// 分配的结果,而这个值是对端拿来判「我的游标是不是还在宿主的编号宇宙里」的。
	var latest int64
	for _, seq := range ledger {
		if seq > latest {
			latest = seq
		}
	}
	return latest + int64(unnumbered), nil
}

func (r *frameSeqRepo) Allocate(ctx context.Context, sessionID int64, keys []FrameKey) ([]int64, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	seqs := make([]int64, 0, len(keys))
	err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		seqs = seqs[:0]
		var latest int64
		// 计数器没有单独的列:本会话台账的 MAX(seq) 就是它。被顶掉的旧分配行留在表里,
		// 所以它只增不减 —— 一个号一旦发出去就永不重用。
		if err := tx.Model(&FrameSeqRow{}).
			Where("session_id = ?", sessionID).
			Select("COALESCE(MAX(seq), 0)").
			Scan(&latest).Error; err != nil {
			return err
		}
		rows := make([]*FrameSeqRow, 0, len(keys))
		for i, key := range keys {
			seq := latest + int64(i) + 1
			rows = append(rows, &FrameSeqRow{
				SessionID: sessionID, MessageID: key.MessageID,
				BlockIdx: key.BlockIdx, Ordinal: key.Ordinal, Seq: seq,
			})
			seqs = append(seqs, seq)
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	return seqs, nil
}

func (r *frameSeqRepo) DeleteBySession(ctx context.Context, sessionID int64) (int64, error) {
	res := db.Ctx(ctx).Where("session_id = ?", sessionID).Delete(&FrameSeqRow{})
	return res.RowsAffected, res.Error
}
