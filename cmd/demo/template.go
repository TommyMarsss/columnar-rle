package main

// reportHTML 是单一静态报告的模板。所有数据通过 /*__DATA__*/ 占位符
// 以内联 JSON 注入；不引用任何外部脚本、样式、字体或图形库，
// 浏览器直接打开 file:// 即可完整运行。
const reportHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>RLE 列式压缩与谓词下推报告</title>
<style>
  :root {
    color-scheme: light;
    --page: #f9f9f7;
    --surface: #fcfcfb;
    --surface-2: #f3f2ee;
    --ink: #0b0b0b;
    --ink-2: #52514e;
    --muted: #898781;
    --grid: #e1e0d9;
    --border: rgba(11,11,11,0.10);
    --accent: #2a78d6;
    --accent-contrast: #ffffff;
    --accent-soft: #e4effc;
    --accent-ink: #184f95;
    --good: #0ca30c;
    --good-ink: #006300;
    --good-soft: #e3f4e3;
    --skip: #e9e8e2;
    --skip-ink: #5d5c58;
    --critical: #d03b3b;
    --critical-soft: #fbe7e7;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      color-scheme: dark;
      --page: #0d0d0d;
      --surface: #1a1a19;
      --surface-2: #222220;
      --ink: #ffffff;
      --ink-2: #c3c2b7;
      --muted: #898781;
      --grid: #2c2c2a;
      --border: rgba(255,255,255,0.10);
      --accent: #3987e5;
      --accent-contrast: #ffffff;
      --accent-soft: #122a45;
      --accent-ink: #86b6ef;
      --good: #0ca30c;
      --good-ink: #4fd14f;
      --good-soft: #103310;
      --skip: #2c2c2a;
      --skip-ink: #c3c2b7;
      --critical: #e66767;
      --critical-soft: #3d1a1a;
    }
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--page); color: var(--ink);
    font-family: system-ui, -apple-system, "Segoe UI", sans-serif;
    line-height: 1.55; font-size: 15px;
  }
  .wrap { max-width: 1100px; margin: 0 auto; padding: 32px 20px 80px; }
  header h1 { font-size: 24px; margin: 0 0 4px; }
  header .sub { color: var(--ink-2); font-size: 13px; margin-bottom: 24px; }
  h2 { font-size: 17px; margin: 36px 0 4px; }
  h2 .num {
    display: inline-block; width: 22px; height: 22px; border-radius: 50%;
    background: var(--accent); color: var(--accent-contrast);
    font-size: 13px; text-align: center; line-height: 22px; margin-right: 8px;
    vertical-align: 1px;
  }
  .sec-desc { color: var(--ink-2); font-size: 13px; margin: 0 0 14px; }
  .card {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: 10px; padding: 16px; margin-top: 12px;
  }
  .tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 10px; }
  .tile {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: 10px; padding: 12px 14px;
  }
  .tile .k { font-size: 12px; color: var(--muted); }
  .tile .v { font-size: 22px; margin-top: 2px; }
  .tile .v small { font-size: 13px; color: var(--ink-2); }
  .badge {
    display: inline-flex; align-items: center; gap: 6px;
    font-size: 12px; font-weight: 600; padding: 3px 10px; border-radius: 999px;
  }
  .badge.ok  { background: var(--good-soft); color: var(--good-ink); }
  .badge.bad { background: var(--critical-soft); color: var(--critical); }
  .badge.skipb { background: var(--skip); color: var(--skip-ink); }
  .badge.hitb  { background: var(--good-soft); color: var(--good-ink); }
  .badge.partb { background: var(--accent-soft); color: var(--accent-ink); }

  /* ---- 单元格 / 轨道 ---- */
  .track { display: flex; flex-wrap: wrap; gap: 4px; }
  .cell {
    min-width: 26px; height: 26px; padding: 0 5px;
    display: inline-flex; align-items: center; justify-content: center;
    border-radius: 5px; font-variant-numeric: tabular-nums; font-size: 12px;
    background: var(--surface-2); border: 1px solid var(--border);
  }
  .cell.dim { opacity: .32; }
  .cell.match {
    background: var(--accent-soft); border-color: var(--accent);
    color: var(--accent-ink); font-weight: 700;
  }

  /* ---- 块与 run ---- */
  .block-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 10px; }
  .blk {
    border: 1px solid var(--border); border-radius: 8px; padding: 10px;
    background: var(--surface); cursor: pointer; text-align: left; font: inherit; color: inherit;
  }
  .blk:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  .blk .bh { display: flex; justify-content: space-between; align-items: center; gap: 6px; }
  .blk .bid { font-weight: 700; font-size: 13px; }
  .blk .bmeta { color: var(--muted); font-size: 11px; margin-top: 2px; font-variant-numeric: tabular-nums; }
  .runs { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 8px; }
  .rchip {
    font-size: 11px; border-radius: 5px; padding: 2px 7px;
    background: var(--surface-2); border: 1px solid var(--border);
    font-variant-numeric: tabular-nums;
  }
  .rchip b { font-weight: 700; }

  .blk.none      { background: var(--skip); }
  .blk.none .bid, .blk.none .bmeta { color: var(--skip-ink); }
  .blk.all       { background: var(--good-soft); border-color: var(--good); }
  .blk.partial   { background: var(--accent-soft); border-color: var(--accent); }
  .blk.all .rchip { background: rgba(255,255,255,.55); }
  @media (prefers-color-scheme: dark) { .blk.all .rchip { background: rgba(255,255,255,.06); } }

  .decision-track { display: flex; gap: 4px; margin-top: 6px; flex-wrap: wrap; }
  .dtag {
    flex: 1 1 40px; min-width: 42px; text-align: center; border-radius: 6px;
    padding: 6px 2px; font-size: 12px; font-weight: 700; cursor: pointer;
    border: 1px solid transparent; font-variant-numeric: tabular-nums;
  }
  .dtag small { display: block; font-weight: 400; font-size: 10px; opacity: .8; }
  .dtag.none { background: var(--skip); color: var(--skip-ink); }
  .dtag.all { background: var(--good-soft); color: var(--good-ink); border-color: var(--good); }
  .dtag.partial { background: var(--accent-soft); color: var(--accent-ink); border-color: var(--accent); }
  .dtag.sel { outline: 2px solid var(--ink); outline-offset: 1px; }

  /* ---- 查询选择 ---- */
  .qtabs { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
  .qtab {
    border: 1px solid var(--border); background: var(--surface); color: var(--ink-2);
    border-radius: 999px; padding: 6px 14px; font-size: 13px; cursor: pointer; font: inherit;
  }
  .qtab.active { background: var(--accent); color: var(--accent-contrast); border-color: var(--accent); }
  .qhead { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; margin: 14px 0 6px; }
  .qhead .pred { font-size: 16px; font-weight: 700; }
  .reason { font-size: 12.5px; color: var(--ink-2); margin: 4px 0 0; }

  table { border-collapse: collapse; width: 100%; font-size: 13px; margin-top: 8px; }
  th, td { text-align: left; padding: 6px 10px; border-bottom: 1px solid var(--grid); vertical-align: top; }
  th { color: var(--muted); font-weight: 600; font-size: 12px; }
  td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }

  .legend { display: flex; flex-wrap: wrap; gap: 14px; font-size: 12.5px; color: var(--ink-2); margin-top: 10px; }
  .legend span { display: inline-flex; align-items: center; gap: 6px; }
  .sw { width: 12px; height: 12px; border-radius: 3px; display: inline-block; }
  .sw.skip { background: var(--skip); border: 1px solid var(--border); }
  .sw.hit  { background: var(--good-soft); border: 1px solid var(--good); }
  .sw.part { background: var(--accent-soft); border: 1px solid var(--accent); }

  .detail-runs { margin-top: 8px; }
  .drun {
    display: flex; align-items: center; gap: 8px; padding: 6px 10px;
    border: 1px solid var(--border); border-radius: 6px; margin-top: 6px; font-size: 13px;
    background: var(--surface);
  }
  .drun.match { background: var(--accent-soft); border-color: var(--accent); }
  .drun.skip  { background: var(--skip); }
  .drun .grow { flex: 1; }
  .mono { font-variant-numeric: tabular-nums; }
  .note {
    border-left: 3px solid var(--accent); padding: 8px 12px; margin-top: 12px;
    background: var(--surface-2); border-radius: 0 8px 8px 0; font-size: 13px; color: var(--ink-2);
  }
  code { background: var(--surface-2); border: 1px solid var(--border); border-radius: 4px; padding: 0 5px; font-size: 12.5px; }
  .footer { margin-top: 48px; color: var(--muted); font-size: 12px; }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1>RLE 列式压缩与谓词下推 — 执行报告</h1>
    <div class="sub">生成时间 <span id="genat"></span> · 仅 Go 标准库生成，纯原生 HTML/CSS/JavaScript，数据与逻辑全部内嵌</div>
  </header>

  <section class="tiles" id="summary"></section>

  <h2><span class="num">1</span>原始列数据</h2>
  <p class="sec-desc">未压缩的 int64 列，共 <b class="mono" id="rowcount"></b> 行；下方每格一个行值，行号从 0 开始。</p>
  <div class="card"><div class="track" id="rawtrack"></div></div>

  <h2><span class="num">2</span>RLE 编码与块划分</h2>
  <p class="sec-desc">
    物理 run = <code>(值, 重复次数)</code>。两层分段：长度超过块大小（<b id="br"></b> 行/块）的游程在<b>块边界</b>切开；
    块内长度超过计数字段容量（<b id="mc"></b>）的游程再按 <code>maxCount + … + 余数</code> 拆分，不溢出、不截断。
  </p>
  <div class="card">
    <div style="font-size:13px;font-weight:600;margin-bottom:6px;">编码块（下推裁块单位，块头携带 min/max/sum 统计）</div>
    <div class="block-grid" id="blockgrid"></div>
    <div class="legend">
      <span><i class="sw part"></i>块内包含多个不同 run（异构块）</span>
      <span style="color:var(--muted)">每个块标注值域 [min, max]、行数、和，以及物理 run 切分</span>
    </div>
  </div>

  <h2><span class="num">3</span>谓词查询与块级决策</h2>
  <p class="sec-desc">
    查询不调用整列解压：<b>跳过</b>的块不读取其 run、不还原任何值；<b>整块命中</b>用块头行数/和直接聚合；
    仅<b>展开</b>的块读取 run，而同值 run 对比较谓词只有“整段命中 / 整段跳过”，仍不需逐行还原。
    右侧“全展开扫描”是对原始数据逐行计算的参照结果。
  </p>
  <div class="qtabs" id="qtabs"></div>
  <div class="card">
    <div class="qhead">
      <span class="pred" id="qpred"></span>
      <span class="badge" id="qbadge"></span>
    </div>
    <div class="tiles" id="qtiles" style="margin-top:10px;"></div>
    <div style="font-size:13px;font-weight:600;margin-top:16px;">逐块决策轨道（点击任一块查看 run 级判定）</div>
    <div class="decision-track" id="dectrack"></div>
    <div class="legend">
      <span><i class="sw skip"></i>跳过：不读 run、不还原值</span>
      <span><i class="sw hit"></i>整块命中：用块头统计直接聚合</span>
      <span><i class="sw part"></i>展开：读取块内 run 逐个判断</span>
    </div>
    <div id="blockdetail" style="margin-top:14px;"></div>
    <div style="font-size:13px;font-weight:600;margin-top:18px;">命中行在原始列上的位置（蓝色）</div>
    <div class="track" id="matchtrack" style="margin-top:8px;"></div>
  </div>

  <h2 style="margin-top:28px;">决策规则速查（块值域 [min, max]，谓词值 v，区间 [lo, hi]）</h2>
  <div class="card">
    <table>
      <thead><tr><th>谓词</th><th>整块跳过（不解压）</th><th>整块命中（用块头聚合）</th><th>否则展开到 run</th></tr></thead>
      <tbody>
        <tr><td class="mono">value = v</td><td>v &lt; min 或 v &gt; max</td><td>min = max = v</td><td>v ∈ [min,max] 且块内有其他值</td></tr>
        <tr><td class="mono">value != v</td><td>min = max = v</td><td>v &lt; min 或 v &gt; max</td><td>v 落在值域内部</td></tr>
        <tr><td class="mono">value &lt; v</td><td>min ≥ v</td><td>max &lt; v</td><td>min &lt; v ≤ max</td></tr>
        <tr><td class="mono">value ≤ v</td><td>min &gt; v</td><td>max ≤ v</td><td>min ≤ v &lt; max</td></tr>
        <tr><td class="mono">value &gt; v</td><td>max ≤ v</td><td>min &gt; v</td><td>max ≥ v &gt; min</td></tr>
        <tr><td class="mono">value ≥ v</td><td>max &lt; v</td><td>min ≥ v</td><td>max ≥ v &gt; min</td></tr>
        <tr><td class="mono">lo ≤ value ≤ hi</td><td>max &lt; lo 或 min &gt; hi</td><td>min ≥ lo 且 max ≤ hi</td><td>区间与块值域部分重叠（含端点相切）</td></tr>
      </tbody>
    </table>
    <div class="note">
      注意 <b>=</b> 的经典边界坑：目标值 v 落在块值域 [min,max] 内部时，即使块内实际没有 v（例如块 {1,5} 查 =3），
      仅凭 min/max <b>不能</b>跳过该块——必须展开到 run 级；而 run 同值，到 run 级即可整段跳过，仍无需逐行还原。
      本报告“跳过块数”与按此规则独立推导出的理论值完全一致：既不多跳过（避免漏结果），也不少跳过（避免无谓解压）。
    </div>
  </div>

  <div class="footer">
    压缩体积估算 = 16 字节列头 + 每 run（8 字节值 + 计数字段宽度）+ 每块 40 字节统计；原始体积按每值 8 字节计。
  </div>
</div>

<script>
"use strict";
var DATA = /*__DATA__*/;
var DEC = ["跳过", "整块命中", "展开"];
var DECCLASS = ["none", "all", "partial"];
var state = { query: 0, block: 0 };
(function readHash() {
  var m = location.hash.match(/q=(\d+)/);
  if (m) state.query = Math.max(0, Math.min(DATA.queries.length - 1, parseInt(m[1], 10)));
  var mb = location.hash.match(/b=(\d+)/);
  if (mb) state.block = parseInt(mb[1], 10);
})();
window.addEventListener("hashchange", function () { location.reload(); });

function el(tag, cls, text) {
  var e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined && text !== null) e.textContent = text;
  return e;
}
function fmt(n) { return String(n); }

function renderSummary() {
  var cfg = DATA.config;
  document.getElementById("genat").textContent = DATA.generatedAt;
  document.getElementById("rowcount").textContent = DATA.rowCount;
  document.getElementById("mc").textContent = cfg.maxCount;
  document.getElementById("br").textContent = cfg.blockRows;
  var ratio = (100 * DATA.encodedBytes / DATA.rawBytes).toFixed(1);
  var tiles = [
    ["总行数", fmt(DATA.rowCount)],
    ["物理 run 数", fmt(DATA.runs.length), "逻辑游程 " + DATA.logicalRuns.length],
    ["编码块数", fmt(DATA.blocks.length), cfg.blockRows + " 行/块"],
    ["计数字段宽度", cfg.countFieldWidth + " 字节", "maxCount=" + cfg.maxCount],
    ["原始 / 压缩体积", DATA.rawBytes + " B", DATA.encodedBytes + " B（" + ratio + "%）"],
  ];
  var box = document.getElementById("summary");
  tiles.forEach(function (t) {
    var tile = el("div", "tile");
    tile.appendChild(el("div", "k", t[0]));
    var v = el("div", "v", t[1]);
    if (t[2]) v.appendChild(el("small", "", "  " + t[2]));
    tile.appendChild(v);
    box.appendChild(tile);
  });
  var rt = el("div", "tile");
  rt.appendChild(el("div", "k", "编码→解码自检"));
  var b = el("div", "v");
  b.appendChild(el("span", "badge " + (DATA.roundTripOK ? "ok" : "bad"),
    DATA.roundTripOK ? "✓ 与原始数据完全一致" : "✗ 不一致"));
  rt.appendChild(b);
  box.appendChild(rt);
}

function valueCell(v, cls) {
  var c = el("span", "cell " + (cls || ""), fmt(v));
  c.title = "值 " + v;
  return c;
}

function renderRaw() {
  var box = document.getElementById("rawtrack");
  DATA.values.forEach(function (v) { box.appendChild(valueCell(v)); });
}

function renderBlocks() {
  var grid = document.getElementById("blockgrid");
  DATA.blocks.forEach(function (b) {
    var constant = b.min === b.max; // 多个 run 也可能同值（超长游程被计数字段分段）
    var d = el("button", "blk" + (constant ? "" : " partial"));
    var bh = el("div", "bh");
    bh.appendChild(el("span", "bid", "块 " + b.index));
    bh.appendChild(el("span", "badge " + (constant ? "hitb" : "partb"),
      constant
        ? (b.runs.length > 1 ? "同值，" + b.runs.length + " 段（计数字段分段）" : "同值 1 run")
        : "异构 " + b.runs.length + " runs"));
    d.appendChild(bh);
    d.appendChild(el("div", "bmeta",
      "行 " + b.rowStart + "–" + (b.rowStart + b.rowCount - 1) +
      " · " + b.rowCount + " 行 · [" + b.min + ", " + b.max + "] · 和 " + b.sum));
    var runs = el("div", "runs");
    b.runs.forEach(function (r) {
      runs.appendChild(el("span", "rchip", ""));
      var chip = runs.lastChild;
      chip.appendChild(el("b", "", fmt(r.value)));
      chip.appendChild(document.createTextNode("×" + r.count));
    });
    d.appendChild(runs);
    d.title = "物理 run #" + b.runs.map(function (r) { return r.index; }).join(", #");
    grid.appendChild(d);
  });
}

function renderQueryTabs() {
  var box = document.getElementById("qtabs");
  DATA.queries.forEach(function (q, i) {
    var t = el("button", "qtab" + (i === state.query ? " active" : ""), q.text);
    t.addEventListener("click", function () {
      state.query = i; state.block = 0;
      history.replaceState(null, "", "#q=" + i + "&b=0");
      Array.prototype.forEach.call(box.children, function (c, j) {
        c.className = "qtab" + (j === i ? " active" : "");
      });
      renderQuery();
    });
    box.appendChild(t);
  });
}

function tile(k, v, sub, cls) {
  var t = el("div", "tile");
  t.appendChild(el("div", "k", k));
  var dv = el("div", "v");
  if (cls) { var s = el("span", cls, v); dv.appendChild(s); }
  else { dv.textContent = v; }
  if (sub) dv.appendChild(el("small", "", "  " + sub));
  t.appendChild(dv);
  return t;
}

function renderQuery() {
  var q = DATA.queries[state.query];
  if (state.block < 0 || state.block >= q.blocks.length) state.block = 0;
  document.getElementById("qpred").textContent = q.text;
  var same = (q.matchCount === q.bruteCount && q.sum === q.bruteSum);
  var badge = document.getElementById("qbadge");
  badge.className = "badge " + (same ? "ok" : "bad");
  badge.textContent = same ? "✓ 下推结果 = 全展开扫描结果" : "✗ 结果不一致";

  var tiles = document.getElementById("qtiles");
  tiles.innerHTML = "";
  tiles.appendChild(tile("命中行数", fmt(q.matchCount), "全展开 " + q.bruteCount));
  tiles.appendChild(tile("命中值之和", q.sum, "全展开 " + q.bruteSum));
  tiles.appendChild(tile("整块跳过", fmt(q.skippedBlocks), "不读 run / 不还原值", "badge skipb"));
  tiles.appendChild(tile("整块命中", fmt(q.wholeHitBlocks), "块头统计直接聚合", "badge hitb"));
  tiles.appendChild(tile("展开块数", fmt(q.partialBlocks), "读取 " + q.drilledRuns + " 个 run", "badge partb"));
  tiles.appendChild(tile("run 级跳过 / 命中行数", fmt(q.runSkippedRows) + " / " + fmt(q.runAcceptedRows),
    "展开块内部仍不逐行还原"));

  // 决策轨道
  var track = document.getElementById("dectrack");
  track.innerHTML = "";
  q.blocks.forEach(function (b, i) {
    var tag = el("button", "dtag " + DECCLASS[b.decision] + (i === state.block ? " sel" : ""));
    tag.appendChild(document.createTextNode("#" + b.blockIndex));
    tag.appendChild(el("small", "", DEC[b.decision]));
    tag.title = b.reason;
    tag.addEventListener("click", function () {
      state.block = i;
      history.replaceState(null, "", "#q=" + state.query + "&b=" + i);
      Array.prototype.forEach.call(track.children, function (c, j) {
        c.className = "dtag " + DECCLASS[q.blocks[j].decision] + (j === i ? " sel" : "");
      });
      renderBlockDetail(q);
    });
    track.appendChild(tag);
  });
  renderBlockDetail(q);

  // 命中行轨道：按 run 顺序重建部分块内命中行的行号（仅用于前端着色，
  // 后端本身从不逐行还原；整块命中直接涂满整块，跳过块保持暗淡）。
  var mt = document.getElementById("matchtrack");
  mt.innerHTML = "";
  q.blocks.forEach(function (b) {
    var matchRows = {};
    if (b.decision === 1) {
      for (var r = 0; r < b.rowCount; r++) matchRows[b.rowStart + r] = true;
    } else if (b.decision === 2) {
      var off = b.rowStart;
      DATA.blocks[b.blockIndex].runs.forEach(function (rr) {
        var tr = b.runs.filter(function (x) { return x.runIndex === rr.index; })[0];
        if (tr && tr.decision === 1) {
          for (var r2 = 0; r2 < rr.count; r2++) matchRows[off + r2] = true;
        }
        off += rr.count;
      });
    }
    for (var row = b.rowStart; row < b.rowStart + b.rowCount; row++) {
      mt.appendChild(valueCell(DATA.values[row], matchRows[row] ? "match" : "dim"));
    }
  });
}

function renderBlockDetail(q) {
  var b = q.blocks[state.block];
  var box = document.getElementById("blockdetail");
  box.innerHTML = "";
  var head = el("div", "qhead");
  head.appendChild(el("span", "pred", "块 " + b.blockIndex + " 的判定："));
  head.appendChild(el("span", "badge " +
    (b.decision === 0 ? "skipb" : b.decision === 1 ? "hitb" : "partb"), DEC[b.decision]));
  box.appendChild(head);
  box.appendChild(el("p", "reason", b.reason));

  var meta = el("p", "reason");
  meta.textContent = "块头统计：行 " + b.rowStart + "–" + (b.rowStart + b.rowCount - 1) +
    "，" + b.rowCount + " 行，值域 [" + b.min + ", " + b.max + "]";
  box.appendChild(meta);

  if (b.decision !== 2) {
    var note = el("div", "note");
    note.textContent = b.decision === 0
      ? "该块的 run 一次都没有被读取，物理值未被还原——这是下推跳过的核心收益。"
      : "该块用块头的行数与和直接完成聚合，没有读取任何 run。";
    box.appendChild(note);
    return;
  }
  var def = DATA.blocks[b.blockIndex];
  var wrap = el("div", "detail-runs");
  var off = b.rowStart;
  def.runs.forEach(function (rr) {
    var tr = b.runs.filter(function (x) { return x.runIndex === rr.index; })[0];
    var d = el("div", "drun " + (tr && tr.decision === 1 ? "match" : "skip"));
    d.appendChild(el("span", "badge " + (tr && tr.decision === 1 ? "hitb" : "skipb"),
      tr && tr.decision === 1 ? "整段命中" : "整段跳过"));
    var mid = el("span", "grow mono", "run #" + rr.index + "：值 " + rr.value + " × " + rr.count +
      " 行（行 " + off + "–" + (off + rr.count - 1) + "）");
    d.appendChild(mid);
    var why = el("span", "reason", tr ? tr.reason : "");
    d.appendChild(why);
    wrap.appendChild(d);
    off += rr.count;
  });
  box.appendChild(wrap);
}

renderSummary();
renderRaw();
renderBlocks();
renderQueryTabs();
renderQuery();
</script>
</body>
</html>
`
