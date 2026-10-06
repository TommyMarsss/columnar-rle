// Package rle 在单列 int64 数据上实现游程编码（Run-Length Encoding），
// 并支持在编码块（block，若干连续 run 打包成的定长行块）上直接做谓词
// 下推，而不必先把数据解压。
//
// 两层结构：
//   - Run：(Value, Count) 对，是最小的压缩单位，块内同值；
//   - Block：按固定目标行数切分的编码块，内部包含若干 run，
//     并预计算 Min/Max/Sum 等统计信息，是谓词下推裁块的单位。
//
// 超长游程在两个维度上都会被分段：计数字段容量（MaxCount）与块边界
// （BlockRows），详见 EncodeBlocked。
package rle

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
)

// MaxCountDefault 是默认的单个计数字段可表示的最大重复次数（uint32 上限）。
// 超过该长度的同值游程必须分段存储，详见 EncodeWithMax。
const MaxCountDefault int64 = math.MaxUint32

// Run 是一个 RLE 编码段：Count 个连续相同的 Value。
//
// 当逻辑游程长度超过列的 MaxCount，或它跨越块边界时，编码器会把它拆成
// 多个 Run（相邻 Run 的 Value 可能相同），解码时自动还原。
type Run struct {
	Value int64 `json:"value"`
	Count int64 `json:"count"`
}

// Decision 是谓词对一个编码块的三分类结论。
type Decision uint8

const (
	// NoneMatch 块内没有任何值满足谓词，整块跳过，不读取/不还原其值。
	NoneMatch Decision = iota
	// AllMatch 块内所有值都满足谓词，整块直接接受，用块统计直接聚合。
	AllMatch
	// PartialMatch 块内部分值满足谓词，需要展开块内的 run 继续判断。
	PartialMatch
)

func (d Decision) String() string {
	switch d {
	case NoneMatch:
		return "none"
	case AllMatch:
		return "all"
	case PartialMatch:
		return "partial"
	default:
		return "unknown"
	}
}

// Label 返回中文决策名称，用于报告展示。
func (d Decision) Label() string {
	switch d {
	case NoneMatch:
		return "跳过"
	case AllMatch:
		return "整块命中"
	case PartialMatch:
		return "展开判断"
	default:
		return "未知"
	}
}

// Block 是一个定长行块（最后一块可能更短）的压缩表示与统计信息。
// RunStart/RunEnd 指向 Column.runs 中的半开区间。
type Block struct {
	Index    int   `json:"index"`
	RunStart int   `json:"runStart"`
	RunEnd   int   `json:"runEnd"`
	RowStart int64 `json:"rowStart"`
	RowCount int64 `json:"rowCount"`
	Min      int64 `json:"min"`
	Max      int64 `json:"max"`
	Sum      int64 `json:"sum"`
	// SumValid 表示 Sum 是否可用；值*计数或求和溢出 int64 时为 false，
	// 此时聚合回退到逐段/逐行计算。
	SumValid bool `json:"sumValid"`
}

// Runs 返回该块包含的 run（复制，避免调用方修改内部表示）。
func (b *Block) Runs(all []Run) []Run {
	return append([]Run(nil), all[b.RunStart:b.RunEnd]...)
}

// Column 是一整列的压缩表示：只保存编码 run 与块统计，不保留原始数据。
type Column struct {
	maxCount  int64
	blockRows int64
	runs      []Run
	blocks    []Block
	// blockSumsBig[i] 是第 i 块所有值之和的精确值（不受 int64 溢出影响）。
	blockSumsBig []*big.Int
	rowCount     int64
}

// Encode 使用默认计数字段宽度（uint32）压缩一整列，不做块统计。
func Encode(values []int64) []Run {
	return EncodeWithMax(values, MaxCountDefault)
}

// EncodePaged 以指定计数字段上限压缩一整列，并把 runs 按 runsPerBlock
// 个一组打包成块（不按行数对齐，块边界可能落在游程中间之前的 run 边界）。
func EncodePaged(values []int64, maxCount int64, runsPerBlock int) (*Column, error) {
	return NewColumn(EncodeWithMax(values, maxCount), maxCount, runsPerBlock)
}

// EncodeBlocked 是推荐入口：按 blockRows 行一块切分原始列，每块独立做
// RLE 编码，计数字段容量为 maxCount。
//
// 分段规则（两个维度都保证不溢出、不截断）：
//  1. 块边界：长度超过块边界的游程在边界处切开，因此一个逻辑游程可能
//     在相邻块中各有一段；块大小固定为 blockRows 行，便于下推裁块。
//  2. 计数字段：块内游程长度 n > maxCount 时，拆成 ceil(n/maxCount) 个
//     run，前面的 Count 均为 maxCount，最后一个为余数。
func EncodeBlocked(values []int64, maxCount, blockRows int64) (*Column, error) {
	if maxCount < 1 {
		return nil, errors.New("rle: maxCount must be >= 1")
	}
	if blockRows < 1 {
		return nil, errors.New("rle: blockRows must be >= 1")
	}
	c := &Column{maxCount: maxCount, blockRows: blockRows}
	for off := 0; int64(off) < int64(len(values)); off += int(blockRows) {
		end := int64(off) + blockRows
		if end > int64(len(values)) {
			end = int64(len(values))
		}
		chunk := values[off:end]
		chunkRuns := EncodeWithMax(chunk, maxCount)
		c.addBlock(chunk, chunkRuns)
	}
	return c, nil
}

// NewColumn 校验并包装一段已编码的 run，按 runsPerBlock 个 run 一组打包
// 成块。runs 允许出现相邻同值 run（超长游程的分段）。
func NewColumn(runs []Run, maxCount int64, runsPerBlock int) (*Column, error) {
	if maxCount < 1 {
		return nil, errors.New("rle: maxCount must be >= 1")
	}
	if runsPerBlock < 1 {
		return nil, errors.New("rle: runsPerBlock must be >= 1")
	}
	c := &Column{maxCount: maxCount, blockRows: -1}
	var rowCursor int64
	for start := 0; start < len(runs); start += runsPerBlock {
		end := start + runsPerBlock
		if end > len(runs) {
			end = len(runs)
		}
		group := runs[start:end]
		var n int64
		for i, r := range group {
			if r.Count < 1 {
				return nil, fmt.Errorf("rle: run %d has non-positive count %d", start+i, r.Count)
			}
			if r.Count > maxCount {
				return nil, fmt.Errorf("rle: run %d count %d exceeds maxCount %d", start+i, r.Count, maxCount)
			}
			if n > math.MaxInt64-r.Count {
				return nil, errors.New("rle: total row count overflows int64")
			}
			n += r.Count
		}
		b := Block{
			Index:    len(c.blocks),
			RunStart: start,
			RunEnd:   end,
			RowStart: rowCursor,
			RowCount: n,
			Min:      group[0].Value,
			Max:      group[0].Value,
			SumValid: true,
		}
		c.blockSumsBig = append(c.blockSumsBig, accumulateStats(&b, group))
		c.blocks = append(c.blocks, b)
		c.runs = append(c.runs, group...)
		c.rowCount += n
		rowCursor += n
	}
	return c, nil
}

// addBlock 追加一个块，行数取自原始 chunk（仅用于统计与校验，不保留）。
func (c *Column) addBlock(chunk []int64, chunkRuns []Run) {
	b := Block{
		Index:    len(c.blocks),
		RunStart: len(c.runs),
		RunEnd:   len(c.runs) + len(chunkRuns),
		RowStart: c.rowCount,
		RowCount: int64(len(chunk)),
		SumValid: true,
	}
	if len(chunkRuns) > 0 {
		b.Min = chunkRuns[0].Value
		b.Max = chunkRuns[0].Value
		c.blockSumsBig = append(c.blockSumsBig, accumulateStats(&b, chunkRuns))
	} else {
		c.blockSumsBig = append(c.blockSumsBig, new(big.Int))
	}
	c.runs = append(c.runs, chunkRuns...)
	c.blocks = append(c.blocks, b)
	c.rowCount += b.RowCount
}

// accumulateStats 在已有 Min/Max 初值上累加块的 Max/Sum 统计，
// 返回始终精确的块和（big.Int）。
func accumulateStats(b *Block, group []Run) *big.Int {
	sumBig := new(big.Int)
	for _, r := range group {
		if r.Value < b.Min {
			b.Min = r.Value
		}
		if r.Value > b.Max {
			b.Max = r.Value
		}
		// 值 * 重复次数 或块内累加可能溢出 int64；int64 和只作快速路径，
		// 精确和始终用 big.Int 保存。
		prod, ok := mulInt64(r.Value, r.Count)
		sum, ok2 := addInt64(b.Sum, prod)
		if ok && ok2 {
			b.Sum = sum
		} else {
			b.SumValid = false
		}
		sumBig.Add(sumBig, new(big.Int).Mul(big.NewInt(r.Value), big.NewInt(r.Count)))
	}
	if !b.SumValid {
		b.Sum = 0
	}
	return sumBig
}

// MaxCount 返回该列单个计数字段的最大表示范围。
func (c *Column) MaxCount() int64 { return c.maxCount }

// BlockRows 返回编码时的目标块行数；NewColumn 包装的列返回 -1。
func (c *Column) BlockRows() int64 { return c.blockRows }

// Runs 返回编码 run 的只读副本。
func (c *Column) Runs() []Run { return append([]Run(nil), c.runs...) }

// Blocks 返回块统计的只读副本。
func (c *Column) Blocks() []Block { return append([]Block(nil), c.blocks...) }

// RowCount 返回解码后的总行数。
func (c *Column) RowCount() int64 { return c.rowCount }

// EncodeWithMax 把相邻相同值合并为 (Value, Count) 对。
//
// 分段规则：当一个同值游程长度 n > maxCount 时，拆成
// ceil(n/maxCount) 个 run，前面的 run Count 均为 maxCount，
// 最后一个为余数（余数为 0 时不产生空 run）。只依赖整除/取余，
// 任何计数字段都不会溢出或被截断。
func EncodeWithMax(values []int64, maxCount int64) []Run {
	if maxCount < 1 {
		panic("rle: maxCount must be >= 1")
	}
	var runs []Run
	for i := 0; i < len(values); {
		v := values[i]
		j := i + 1
		for j < len(values) && values[j] == v {
			j++
		}
		n := int64(j - i)
		for n > 0 {
			cnt := n
			if cnt > maxCount {
				cnt = maxCount
			}
			runs = append(runs, Run{Value: v, Count: cnt})
			n -= cnt
		}
		i = j
	}
	return runs
}

// segmentCounts 是分段规则的纯函数形式：返回长度为 n 的游程在给定
// maxCount 下每个分段的计数序列（不构造数据，便于对超大 n 做边界验证）。
func segmentCounts(n, maxCount int64) []int64 {
	if n < 0 || maxCount < 1 {
		panic("rle: invalid segmentCounts arguments")
	}
	var out []int64
	for n > 0 {
		cnt := n
		if cnt > maxCount {
			cnt = maxCount
		}
		out = append(out, cnt)
		n -= cnt
	}
	return out
}

// TotalCount 返回编码 run 解码后的总行数。
func TotalCount(runs []Run) (int64, error) {
	var total int64
	for i, r := range runs {
		if r.Count < 1 {
			return 0, fmt.Errorf("rle: run %d has non-positive count %d", i, r.Count)
		}
		if total > math.MaxInt64-r.Count {
			return 0, errors.New("rle: total row count overflows int64")
		}
		total += r.Count
	}
	return total, nil
}

// Decode 把任意合法 run 序列还原为原始列。
// 相邻同值 run（超长游程或跨块的分段）自动衔接还原。
func Decode(runs []Run) ([]int64, error) {
	total, err := TotalCount(runs)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, total)
	for _, r := range runs {
		for k := int64(0); k < r.Count; k++ {
			out = append(out, r.Value)
		}
	}
	return out, nil
}

// DecodeColumn 还原整列。
func (c *Column) DecodeColumn() ([]int64, error) {
	return Decode(c.runs)
}

// MergeRuns 把分段存储产生的相邻同值 run 合并回逻辑游程。
func MergeRuns(runs []Run) []Run {
	var merged []Run
	for _, r := range runs {
		if n := len(merged); n > 0 && merged[n-1].Value == r.Value {
			merged[n-1].Count += r.Count
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

// LogicalRuns 返回合并分段后的逻辑游程（用于展示“逻辑游程 vs 物理分段”）。
func (c *Column) LogicalRuns() []Run { return MergeRuns(c.runs) }

// CountFieldWidth 返回存储计数所需的字节宽度（1/2/4/8），
// 由该列的 MaxCount 决定，用于估算压缩后体积。
func CountFieldWidth(maxCount int64) int {
	switch {
	case maxCount <= 0xff:
		return 1
	case maxCount <= 0xffff:
		return 2
	case maxCount <= math.MaxUint32:
		return 4
	default:
		return 8
	}
}

// EncodedSize 估算压缩表示占用的字节数：16 字节列头
// （maxCount、run 数各 8 字节）+ 每个 run 的 8 字节值 + 计数字段，
// 另加每块 40 字节统计（min/max/sum/行数/run 下标等）。
func (c *Column) EncodedSize() int {
	width := CountFieldWidth(c.maxCount)
	return 16 + len(c.runs)*(8+width) + len(c.blocks)*40
}

func formatInt(v int64) string { return strconv.FormatInt(v, 10) }
