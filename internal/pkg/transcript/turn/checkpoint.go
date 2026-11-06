package turn

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
)

// 轮内增量 checkpoint。
//
// 一轮里每个 ToolResult 之后都要 checkpoint 一次。整份做法(SetBlocks(Snapshot()) 再由仓储
// 解开新旧两份正文求差分)每次都是 O(消息大小),一轮 O(N²),而且跑在持 turn 锁的事件
// 循环上。块是追加为主的:已经落库的块只有经 Mutate / AdoptMutateKey / MutateBlock 才会
// 被改,末尾还在长的正文 / 思考尾巴每次重新比较。于是累加器记下「上次提交时每个块编出
// 的 JSON 元素」与「之后被改过的下标」,一次 checkpoint 只编码新增、被改动与尾巴这几块。
//
// 正文 BlocksJSON 仍要每次交出(对端帧从它投影),它由缓存的元素直接拼出来:一次按总长
// 分配,不做任何 JSON 编码,与 json.Marshal(EncodeAll(Snapshot())) 逐字节相等 ——
// encoding/json 编切片就是 "[" + 逐元素编码以 "," 连接 + "]"。

// EncodedBlock 是一次 checkpoint 里内容变了(或新增)的一个块:Idx 是它在正文数组里的
// 下标,Block 是它的落库形态(与 cagoblocks.Encode 同口径)。
type EncodedBlock struct {
	Idx   int
	Block cagoblocks.StoredBlock
}

// Checkpoint 是 PrepareCheckpoint / PrepareFinalCheckpoint 交出的写集合:把已提交的那版正文
// 推到这一版所需的最小块集合。写库成功后必须交回 CommitCheckpoint,否则下一次仍会交出这些块。
type Checkpoint struct {
	// Changed 按 Idx 升序,只含内容与上一次提交不同(或新增)的块。
	Changed []EncodedBlock
	// TruncateFrom >= 0 表示新版比上一次提交短,idx >= TruncateFrom 的残块要删掉;-1 不截断。
	TruncateFrom int
	// BlocksJSON 是这一版的整份正文,与 Message.SetBlocks(Snapshot()) 逐字节相等(收尾那一次
	// 则与 SetBlocks(final) 相等)。
	BlocksJSON string

	gen      uint64
	length   int
	finalLen int
	elems    [][]byte // 与 Changed 一一对应的 JSON 元素
	captured map[int]uint64
}

// checkpointState 是最近一次提交的 checkpoint 留下的状态。
type checkpointState struct {
	seeded   bool
	gen      uint64
	elems    [][]byte // 每个块提交时的 JSON 元素(json.Marshal(StoredBlock))
	finalLen int      // 提交时 finalBlocks 的长度;更靠后的下标一律重新比较
	json     string
}

// PrepareCheckpoint 编码自上一次提交以来新增、被改动的块与末尾还在长的尾巴,交出写集合
// 与整份正文。除了第一次记下基准(见 seedLocked)不改动已提交状态:写库失败时丢掉返回值
// 即可,下一次照样交出这些块。
//
// baseline 是消息此刻在库里的正文,只在这个累加器第一次 checkpoint 时作为差分基准
// (消息刚建时是 "[]" / 空串);之后以累加器自己提交过的状态为准。
func (a *Accumulator) PrepareCheckpoint(baseline string) (*Checkpoint, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.seedLocked(baseline); err != nil {
		return nil, err
	}

	tail := a.tailBlocksLocked()
	length := len(a.finalBlocks) + len(tail)
	baseFinal := a.ckpt.finalLen
	var candidates []int
	for idx := range a.dirty {
		if idx < baseFinal {
			candidates = append(candidates, idx)
		}
	}
	sort.Ints(candidates)
	for idx := baseFinal; idx < length; idx++ {
		candidates = append(candidates, idx)
	}
	return a.prepareLocked(func(idx int) cagoblocks.ContentBlock {
		return tailOr(a.finalBlocks, tail, idx)
	}, length, len(a.finalBlocks), candidates)
}

// PrepareFinalCheckpoint 是收尾那一次(轮末 / 中止 / 插话分段)的 checkpoint:final 是这一段
// 要落库的整份正文 —— Finalize 交回的块(必须是它的前缀),外加收尾时追加在末尾的 notice。
//
// 与 PrepareCheckpoint 不同,它把 final 的每一块都重新编码、与已提交版本逐块比较:收尾时
// 有不经累加器的就地改写(MarkRunningSubagentsCancelled / MarkUnansweredUserAsksExpired
// 直接改 Finalize 交回的块指针),只看脏标记会漏掉它们。一轮只做一次,编码代价与原先
// SetBlocks(final) 相同;省下的是写:只交出内容变了的块,不再整条删了重插。
//
// baseline 与提交语义同 PrepareCheckpoint。
func (a *Accumulator) PrepareFinalCheckpoint(baseline string, final []cagoblocks.ContentBlock) (*Checkpoint, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.seedLocked(baseline); err != nil {
		return nil, err
	}
	candidates := make([]int, len(final))
	for idx := range candidates {
		candidates[idx] = idx
	}
	return a.prepareLocked(func(idx int) cagoblocks.ContentBlock {
		return final[idx]
	}, len(final), min(len(final), len(a.finalBlocks)), candidates)
}

// seedLocked 在第一次 checkpoint 时把 baseline 记为已提交的基准。baseline 描述的是库里此刻
// 的那份正文 —— 无论这一次写库成败它都成立,所以准备时就记下,而不是等提交:调用方在写库
// 前就把内存正文换成了新版,写库失败后下一次交来的 baseline 已经不是库里那份。
func (a *Accumulator) seedLocked(baseline string) error {
	if a.ckpt.seeded {
		return nil
	}
	seed, err := splitBlocksJSONElems(baseline)
	if err != nil {
		return err
	}
	a.ckpt = checkpointState{
		seeded: true,
		gen:    a.ckpt.gen,
		elems:  seed,
		json:   joinElems(seed, &Checkpoint{length: len(seed)}),
	}
	return nil
}

// prepareLocked 编码 candidates(升序)处的块,与已提交版本比较,交出写集合。length 是这一版
// 的块数,finalLen 是其中已落成块的前缀长度(提交后更靠后的下标一律重新比较)。
func (a *Accumulator) prepareLocked(
	blockAt func(int) cagoblocks.ContentBlock, length, finalLen int, candidates []int,
) (*Checkpoint, error) {
	base := a.ckpt.elems
	cp := &Checkpoint{TruncateFrom: -1, gen: a.ckpt.gen, finalLen: finalLen, length: length}
	if len(a.dirty) > 0 {
		cp.captured = make(map[int]uint64, len(a.dirty))
		for idx, stamp := range a.dirty {
			cp.captured[idx] = stamp
		}
	}
	for _, idx := range candidates {
		sb, err := cagoblocks.Encode(blockAt(idx))
		if err != nil {
			return nil, err
		}
		elem, err := json.Marshal(sb)
		if err != nil {
			return nil, err
		}
		if idx < len(base) && bytes.Equal(base[idx], elem) {
			continue
		}
		cp.Changed = append(cp.Changed, EncodedBlock{Idx: idx, Block: sb})
		cp.elems = append(cp.elems, elem)
	}
	if cp.length < len(base) {
		cp.TruncateFrom = cp.length
	}

	if len(cp.Changed) == 0 && cp.length == len(base) {
		cp.BlocksJSON = a.ckpt.json
	} else {
		cp.BlocksJSON = joinElems(base, cp)
	}
	return cp, nil
}

// CommitCheckpoint 在写集合落库成功后把它记为已提交。准备之后被改动的块保持脏;
// 准备之后已有别的 checkpoint 提交过时,这次提交过期,被忽略(下一次仍以较新的状态求差分)。
func (a *Accumulator) CommitCheckpoint(cp *Checkpoint) {
	if cp == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if cp.gen != a.ckpt.gen {
		return
	}
	elems := a.ckpt.elems
	if cp.length < len(elems) {
		clear(elems[cp.length:])
		elems = elems[:cp.length]
	}
	for len(elems) < cp.length {
		elems = append(elems, nil)
	}
	for i, c := range cp.Changed {
		elems[c.Idx] = cp.elems[i]
	}
	a.ckpt = checkpointState{
		seeded:   true,
		gen:      cp.gen + 1,
		elems:    elems,
		finalLen: cp.finalLen,
		json:     cp.BlocksJSON,
	}
	for idx, stamp := range cp.captured {
		if a.dirty[idx] == stamp {
			delete(a.dirty, idx)
		}
	}
}

// MutateBlock 对一个经 AddBlock 推入的块(必须是当时推入的那个指针)在累加器锁内调 fn,
// 并把它记为已改动,下一次 checkpoint 会重新编码它。给持有块指针、不走 mutate key 的
// 调用方用(如审批卡)。b 不在本累加器里时不调 fn、交回 false。
func (a *Accumulator) MutateBlock(b cagoblocks.ContentBlock, fn func()) bool {
	if b == nil || reflect.ValueOf(b).Kind() != reflect.Pointer {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, blk := range a.finalBlocks {
		if blk == b {
			fn()
			a.markDirtyLocked(i)
			return true
		}
	}
	return false
}

// markDirtyLocked 记下 idx 处的块被就地改过。每次打一个新戳:提交只清掉准备时看到的
// 那个戳,准备与提交之间的改动不会被吞掉。
func (a *Accumulator) markDirtyLocked(idx int) {
	if a.dirty == nil {
		a.dirty = map[int]uint64{}
	}
	a.stamp++
	a.dirty[idx] = a.stamp
}

// tailBlocksLocked 是 Snapshot 末尾那段还没落成块的尾巴(thinking → text)。
func (a *Accumulator) tailBlocksLocked() []cagoblocks.ContentBlock {
	var tail []cagoblocks.ContentBlock
	if a.thinkingBuf.Len() > 0 {
		tail = append(tail, &cagoblocks.ThinkingBlock{Text: a.thinkingBuf.String()})
	}
	if a.textBuf.Len() > 0 {
		tail = append(tail, &cagoblocks.TextBlock{Text: a.textBuf.String()})
	}
	return tail
}

func tailOr(final, tail []cagoblocks.ContentBlock, idx int) cagoblocks.ContentBlock {
	if idx < len(final) {
		return final[idx]
	}
	return tail[idx-len(final)]
}

// splitBlocksJSONElems 把一份正文拆成逐块的 JSON 元素(与 checkpoint 缓存同口径)。
func splitBlocksJSONElems(blocksJSON string) ([][]byte, error) {
	if blocksJSON == "" {
		return [][]byte{}, nil
	}
	var stored []cagoblocks.StoredBlock
	if err := json.Unmarshal([]byte(blocksJSON), &stored); err != nil {
		return nil, err
	}
	elems := make([][]byte, len(stored))
	for i := range stored {
		elem, err := json.Marshal(stored[i])
		if err != nil {
			return nil, err
		}
		elems[i] = elem
	}
	return elems, nil
}

// joinElems 用已提交的元素与这次变了的元素拼出整份正文:一次按总长分配。
func joinElems(base [][]byte, cp *Checkpoint) string {
	elemAt := func(idx, changed int) ([]byte, int) {
		if changed < len(cp.Changed) && cp.Changed[changed].Idx == idx {
			return cp.elems[changed], changed + 1
		}
		return base[idx], changed
	}
	size := 2
	for idx, changed := 0, 0; idx < cp.length; idx++ {
		var elem []byte
		elem, changed = elemAt(idx, changed)
		size += len(elem) + 1
	}
	var sb strings.Builder
	sb.Grow(size)
	sb.WriteByte('[')
	for idx, changed := 0, 0; idx < cp.length; idx++ {
		var elem []byte
		elem, changed = elemAt(idx, changed)
		if idx > 0 {
			sb.WriteByte(',')
		}
		sb.Write(elem)
	}
	sb.WriteByte(']')
	return sb.String()
}
