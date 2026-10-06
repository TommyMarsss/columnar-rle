# columnar-rle — 列式 RLE 压缩与谓词下推

用 Go 标准库实现的单列 `int64` 游程编码（Run-Length Encoding）压缩存储，
并在**不整列解压**的前提下执行等值/范围谓词过滤与 `count` / `sum` 聚合。
配套一个纯原生 HTML/CSS/JavaScript（无任何框架或图形库）的单一静态报告，
可视化原始列、RLE 块划分与每次查询的逐块决策过程。

- 仅依赖 Go 标准库（聚合用 `math/big` 防溢出，报告 JSON 用 `encoding/json`）
- 前端零依赖：`go run ./cmd/demo` 生成一个数据与逻辑全内嵌的 `report.html`，浏览器直接打开

## 目录结构

| 文件 | 内容 |
|---|---|
| `rle.go` | RLE 编码/解码、两层分段（计数字段容量 + 块边界）、块统计（min/max/精确 sum） |
| `arith.go` | int64 乘法/加法的溢出安全检测 |
| `predicate.go` | 谓词定义与三分类决策规则（`DecideScalar` / `DecideRange`） |
| `query.go` | 下推查询引擎：跳过 / 整块命中 / 下钻 run，附完整决策轨迹 |
| `rle_test.go` | 边界往返、分段、决策真值表、跳过块数核对、跨块聚合、随机差分测试 |
| `cmd/demo` | 示例数据 → 编码 + 7 组查询 → 内嵌 JSON 的静态报告生成器 |

## RLE 编码格式

逻辑上一列被编码为一串 run：

```
Run { Value int64; Count int64 }   // Count 个连续相同的 Value
```

物理存储分两层：

1. **编码块 Block（下推裁块单位，默认定长 `blockRows` 行）**
   - 块头预计算统计：`RowCount`、`Min`、`Max`、`Sum`（另有始终精确的
     `big.Int` 和，供溢出场景聚合）、块内 run 的下标区间。
   - 跨块边界的游程在边界处切开，因此一个逻辑游程可能在相邻块各有一段。
2. **块内 run（最小压缩单位）**
   - 块内相邻相同值合并为 `(Value, Count)`。

### 计数字段分段（不溢出、不截断）

单个 `Count` 受计数字段容量 `maxCount` 限制（默认 `math.MaxUint32`，
即 4 字节字段；`CountFieldWidth` 按容量映射 1/2/4/8 字节宽度）。
长度为 `n` 的游程编码为 `ceil(n/maxCount)` 个 run：

- 前 `n/maxCount` 段的 `Count = maxCount`（满段）；
- 最后一段 `Count = n mod maxCount`；余数为 0 时不产生空段；
- 只依赖整除/取余，不依赖大整数，任何字段宽度下都不溢出、不丢尾。

例：`maxCount=5` 时 11 个连续的 7 → `(7,5) (7,5) (7,1)`。
解码时 run 顺序展开即还原（同值相邻 run 自动衔接），`MergeRuns` 可把
物理分段合并回逻辑游程。

压缩体积估算（`EncodedSize`）：`16 + runs×(8+计数字段宽度) + blocks×40` 字节。

## 谓词下推决策规则

对块的值域 `[min, max]` 与谓词做三分类：

| 结论 | 含义 | 执行动作 |
|---|---|---|
| **NoneMatch（跳过）** | 块内不可能有命中 | 不读取该块任何 run、不还原值 |
| **AllMatch（整块命中）** | 块内全部满足 | 用块头 `RowCount/Sum` 直接聚合，不读 run |
| **PartialMatch（展开）** | 仅凭块头无法定论 | 读取块内 run 逐个判断 |

标量谓词 `value op v` 的精确规则：

| 谓词 | 整块跳过 | 整块命中 |
|---|---|---|
| `= v` | `v < min` 或 `v > max` | `min = max = v` |
| `!= v` | `min = max = v` | `v < min` 或 `v > max` |
| `< v` | `min ≥ v` | `max < v` |
| `≤ v` | `min > v` | `max ≤ v` |
| `> v` | `max ≤ v` | `min > v` |
| `≥ v` | `max < v` | `min ≥ v` |

闭区间 `lo ≤ value ≤ hi`：

- 跳过：`max < lo` 或 `min > hi`（值域与区间不相交）
- 整块命中：`min ≥ lo` 且 `max ≤ hi`（值域被区间完全包含）
- 否则展开（含端点相切，如块 `[1,10]` 查 `[10,20]`，只有边界值命中）

**等值谓词的经典坑**：`v` 落在 `[min,max]` 内部时，即使块内实际没有 `v`
（如块 `{1,5}` 查 `=3`），仅凭 min/max 也**不能**跳过——必须展开。
但下钻到 run 后，因为 run 内值恒定，单个 run 对任何比较谓词只有
"整段命中 / 整段跳过"，仍按 `Count` 聚合，**不需要逐行还原**。

### 正确性核对方式

`rle_test.go` 用一个独立参考实现（直接读原始数据切块、数实际命中数，
再按上述 min/max 规则推导"可证明的跳过"）逐块比对，并断言：

- 跳过块数 = 理论可跳过块数（不多跳过→不漏结果；不少跳过→无无谓解压）；
- 被跳过 / 整块命中的块绝不出现在"实际读取 run"的集合里
  （引擎用 `AccessedBlocks` 显式记录读过 run 的块）；
- 命中行数与 `sum` 与对全展开原始数据逐行扫描的结果完全一致。

### 跨块边界聚合

区间在某个块中间切开（甚至切在跨块长游程中间）时，两侧块整块
跳过/命中，边缘块展开到 run，只把落在区间内的段计入。测试
`TestRangeCutExactlyAtBlockAndRunBoundaries` 专门覆盖
`[1,1,1,1,1 | 1,1,2,2,2]` 上 `value = 1` 这种切在游程中部的情形。
`sum` 全程用 `big.Int` 累加，`MaxInt64` 重复多次溢出 int64 时结果仍精确
（见 `TestSumOverflowUsesBigInt`）。

## 分段阈值选择

- **`maxCount`（计数字段容量）**：容量越小，长游程被切成越多段，
  编码体积上升（每段都要存一次 8 字节 value），但计数可用更窄字段；
  容量取字段宽度的自然上限（255 / 65535 / 2³²−1 / 2⁶³−1）可在
  "窄计数"与"段数"之间取得平衡。默认 uint32 容量：单列超过 42.9 亿
  个连续同值才会分段，几乎不产生额外段，而计数只占 4 字节。
- **`blockRows`（块行数）**：块越大，块头统计的摊薄成本越低、
  整块跳过/命中的吞吐越高，但一次"部分命中"需要读取的 run 更多、
  裁块粒度更粗；块越小，跳过越精准但块头越多。默认演示取 8 行
  （刻意制造跨块游程以暴露边界问题）；真实场景常取 1k–64k 行并按
  数据值域分布（排序聚簇程度）调整。
- 两个阈值相互独立：超长游程先在块边界切、块内再按 `maxCount` 切，
  两种分段都在测试中覆盖。

## 使用示例

```go
col, err := rle.EncodeBlocked(values, rle.MaxCountDefault, 4096)
if err != nil { /* … */ }

restored, err := col.DecodeColumn()              // 完整往返
q1 := col.QueryScalar(rle.Predicate{Op: rle.OpEq, Value: 9})
q2 := col.QueryRange(rle.RangePredicate{Lo: 4, Hi: 7})

fmt.Println(q2.MatchCount, q2.SkippedBlocks, q2.PartialBlocks, q2.Sum)
for _, t := range q2.Blocks { /* t.Decision / t.Reason / t.Runs 决策轨迹 */ }
```

## 运行

```sh
go test -v ./...          # 全部自动化测试
go run ./cmd/demo         # 生成 ./report.html（.gitignore 已忽略）
open report.html
```

报告内容：原始列单元格轨道、编码块卡片（值域/行数/和/物理 run 切分）、
7 组查询的逐块决策轨道、点击块查看 run 级判定与理由、命中行高亮，
以及每组查询"下推结果 vs 全展开扫描"的 count/sum 一致性徽标。
