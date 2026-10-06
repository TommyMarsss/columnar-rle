package rle

// Op 是谓词比较算子。
type Op uint8

const (
	OpEq Op = iota // =
	OpNe           // !=
	OpLt           // <
	OpLe           // <=
	OpGt           // >
	OpGe           // >=
)

func (op Op) String() string {
	switch op {
	case OpEq:
		return "="
	case OpNe:
		return "!="
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	default:
		return "?"
	}
}

// Predicate 是单列谓词：列值 op Value。
type Predicate struct {
	Op    Op    `json:"op"`
	Value int64 `json:"value"`
}

// Eval 在单个值上判断谓词。
func (p Predicate) Eval(v int64) bool {
	switch p.Op {
	case OpEq:
		return v == p.Value
	case OpNe:
		return v != p.Value
	case OpLt:
		return v < p.Value
	case OpLe:
		return v <= p.Value
	case OpGt:
		return v > p.Value
	case OpGe:
		return v >= p.Value
	default:
		return false
	}
}

func (p Predicate) String() string { return "value " + p.Op.String() + " " + formatInt(p.Value) }

// RangePredicate 是闭区间谓词 lo <= value <= hi（lo<=hi）。
type RangePredicate struct {
	Lo int64 `json:"lo"`
	Hi int64 `json:"hi"`
}

// EvalRow 在单个值上判断区间谓词。
func (r RangePredicate) EvalRow(v int64) bool { return v >= r.Lo && v <= r.Hi }

func (r RangePredicate) String() string {
	return formatInt(r.Lo) + " ≤ value ≤ " + formatInt(r.Hi)
}

// DecideScalar 根据块的 [min,max] 统计对一个比较谓词做三分类。
// 规则对每个算子显式给出，避免“端点不满足即整块不满足”在
// Eq/Ne 上的经典误判（端点不等于 v 不代表内部不等于 v）：
//
//	      整块命中 AllMatch        整块不命中 NoneMatch
//	=  v : min==max==v            v<min 或 v>max
//	!=v : v<min 或 v>max          min==max==v
//	<  v : max <  v               min >= v
//	<=v : max <= v                min >  v
//	>  v : min >  v               max <= v
//	>=v : min >= v                max <  v
//
// 其余情况为 PartialMatch。min>max（空块）一律 NoneMatch。
func DecideScalar(p Predicate, min, max int64) Decision {
	if min > max {
		return NoneMatch
	}
	v := p.Value
	switch p.Op {
	case OpEq:
		switch {
		case min == v && max == v:
			return AllMatch
		case v < min || v > max:
			return NoneMatch
		default:
			return PartialMatch
		}
	case OpNe:
		switch {
		case v < min || v > max:
			return AllMatch
		case min == v && max == v:
			return NoneMatch
		default:
			return PartialMatch
		}
	case OpLt:
		switch {
		case max < v:
			return AllMatch
		case min >= v:
			return NoneMatch
		default:
			return PartialMatch
		}
	case OpLe:
		switch {
		case max <= v:
			return AllMatch
		case min > v:
			return NoneMatch
		default:
			return PartialMatch
		}
	case OpGt:
		switch {
		case min > v:
			return AllMatch
		case max <= v:
			return NoneMatch
		default:
			return PartialMatch
		}
	case OpGe:
		switch {
		case min >= v:
			return AllMatch
		case max < v:
			return NoneMatch
		default:
			return PartialMatch
		}
	default:
		return PartialMatch
	}
}

// DecideRange 根据块值域 [min,max] 与闭区间 [lo,hi] 的相交关系三分类：
//   - max < lo 或 min > hi   → NoneMatch（整块落在区间外，跳过解压）
//   - min >= lo 且 max <= hi → AllMatch（整块落在区间内，直接接受）
//   - 其余（部分重叠）        → PartialMatch（展开块内 run 逐个判断）
func DecideRange(lo, hi, min, max int64) Decision {
	if min > max || lo > hi {
		return NoneMatch
	}
	if max < lo || min > hi {
		return NoneMatch
	}
	if min >= lo && max <= hi {
		return AllMatch
	}
	return PartialMatch
}
