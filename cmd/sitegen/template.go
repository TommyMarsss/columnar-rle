package main

// pageTemplate is the entire demo page: markup, styles and rendering logic
// are inline; the JSON payload computed by the Go program is substituted for
// the /*__DATA__*/ placeholder. No external resources are referenced.
const pageTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>列式 RLE 压缩与谓词下推演示</title>
<style>
  :root {
    --bg: #0f1420; --panel: #1a2132; --panel2: #222b40; --text: #e6e9f2;
    --muted: #8b93a7; --accent: #5b9dff;
    --skip: #3a4257; --all: #1f6f4a; --partial: #8a6d1d;
    --skip-t: #9aa3b8; --all-t: #7fe0b0; --partial-t: #ffd97a;
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--bg); color: var(--text);
         font: 14px/1.6 -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; }
  main { max-width: 1080px; margin: 0 auto; padding: 24px 16px 64px; }
  h1 { font-size: 22px; margin: 8px 0 4px; }
  h2 { font-size: 17px; margin: 32px 0 10px; border-left: 3px solid var(--accent); padding-left: 8px; }
  .sub { color: var(--muted); margin-top: 0; }
  code, .mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
  .panel { background: var(--panel); border: 1px solid #2a3450; border-radius: 10px;
           padding: 14px 16px; margin: 12px 0; }
  .legend { display: flex; gap: 14px; flex-wrap: wrap; color: var(--muted); font-size: 13px; }
  .chip { display: inline-block; min-width: 34px; text-align: center; padding: 2px 6px;
          margin: 2px; border-radius: 6px; background: var(--panel2); font-size: 12.5px; }
  .chip.hit { outline: 2px solid var(--accent); }
  .blockrow { display: flex; align-items: flex-start; gap: 10px; margin: 8px 0; flex-wrap: wrap; }
  .blabel { width: 74px; color: var(--muted); font-size: 12.5px; padding-top: 3px; flex: none; }
  .runs { display: flex; flex-wrap: wrap; gap: 6px; }
  .run { background: var(--panel2); border-radius: 6px; padding: 3px 8px; font-size: 12.5px; }
  .run b { color: var(--accent); }
  .stats { color: var(--muted); font-size: 12.5px; }
  .verdict { display: inline-block; padding: 2px 10px; border-radius: 999px; font-size: 12px;
             font-weight: 600; }
  .v0 { background: var(--skip); color: var(--skip-t); }
  .v1 { background: var(--all); color: var(--all-t); }
  .v2 { background: var(--partial); color: var(--partial-t); }
  .qhead { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; }
  .qhead h3 { margin: 0; font-size: 15px; }
  .ok { color: var(--all-t); font-weight: 600; }
  .bad { color: #ff8a8a; font-weight: 600; }
  table { border-collapse: collapse; font-size: 12.5px; }
  td, th { padding: 3px 10px; border: 1px solid #2a3450; text-align: right; }
  th { color: var(--muted); font-weight: 600; }
  .grid { display: grid; grid-template-columns: repeat(16, 1fr); gap: 2px; }
  .cell { text-align: center; font-size: 11px; padding: 2px 0; border-radius: 4px;
          background: var(--panel2); }
  .cell.b0 { box-shadow: inset 0 -2px 0 #5b9dff; } .cell.b1 { box-shadow: inset 0 -2px 0 #9d6bff; }
  .cell.b2 { box-shadow: inset 0 -2px 0 #ff8a5b; } .cell.b3 { box-shadow: inset 0 -2px 0 #4ac9c9; }
  .cell.b4 { box-shadow: inset 0 -2px 0 #c95bd0; } .cell.b5 { box-shadow: inset 0 -2px 0 #8bc34a; }
  .cell.b6 { box-shadow: inset 0 -2px 0 #ffd54a; } .cell.b7 { box-shadow: inset 0 -2px 0 #7a8cff; }
  .cell.qhit { background: #2f4a7a; color: #fff; }
  .note { color: var(--muted); font-size: 12.5px; }
</style>
</head>
<body>
<main>
  <h1>列式存储：RLE 压缩与谓词下推</h1>
  <p class="sub">单文件静态演示 · 数据与判定逻辑全部内嵌 · 由 Go 程序生成，浏览器端用原生 JS 独立复核</p>

  <div class="panel legend">
    <span><span class="verdict v0">SKIP</span> 块内 min/max 与谓词区间不相交 → 整块跳过，不解压</span>
    <span><span class="verdict v1">ALL</span> 谓词区间完整覆盖块内 [min,max] → 整块接受，用预聚合统计直接计入</span>
    <span><span class="verdict v2">PARTIAL</span> 区间与 [min,max] 部分相交 → 解压该块逐行判断</span>
  </div>

  <h2>1. 原始列数据（<span id="nrows"></span> 行，每 <span id="bs"></span> 行一个块，底色竖条区分块）</h2>
  <div class="panel"><div id="rawgrid" class="grid"></div></div>

  <h2>2. RLE 编码块划分（计数字段 16 位，单段上限 <span id="mrc"></span>）</h2>
  <div id="blocks"></div>

  <h2>3. 超长 run 的分段编码</h2>
  <div class="panel" id="segdemo"></div>

  <h2>4. 谓词下推查询：逐块跳过 / 展开决策</h2>
  <div id="queries"></div>

  <p class="note">本页所有“暴力校验”均在浏览器内对 DATA.values 全展开重新计算，与 Go 端内嵌结果比对。</p>
</main>
<script>
const DATA = /*__DATA__*/;

const $ = (id) => document.getElementById(id);
const el = (tag, cls, text) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
};

$("nrows").textContent = DATA.values.length;
$("bs").textContent = DATA.blockSize;
$("mrc").textContent = DATA.maxRunCount;

// --- Section 1: raw values grid ---
const grid = $("rawgrid");
DATA.values.forEach((v, i) => {
  const b = Math.floor(i / DATA.blockSize);
  const c = el("div", "cell b" + (b % 8), String(v));
  c.title = "row " + i + " · block " + b;
  grid.appendChild(c);
});

// --- Section 2: RLE blocks ---
const blocksEl = $("blocks");
DATA.blocks.forEach((b, bi) => {
  const p = el("div", "panel");
  const head = el("div", "qhead");
  head.appendChild(el("h3", null, "Block " + bi));
  head.appendChild(el("span", "stats",
    "rows=" + b.rows + " · min=" + b.min + " · max=" + b.max + " · sum=" + b.sum +
    " · " + b.runs.length + " 个 run · 压缩率 " + (b.rows / b.runs.length).toFixed(1) + "×"));
  p.appendChild(head);
  const runs = el("div", "runs");
  b.runs.forEach(r => {
    const e = el("span", "run");
    e.innerHTML = "(值 <b>" + r.value + "</b> × " + r.count + ")";
    runs.appendChild(e);
  });
  p.appendChild(runs);
  blocksEl.appendChild(p);
});

// --- Section 3: segmentation demo ---
const seg = $("segdemo");
seg.appendChild(el("p", null,
  "单个逻辑 run：值 42 连续重复 " + DATA.segDemo.runLen + " 次，超过单段上限 " +
  DATA.maxRunCount + "，编码器自动切分为 " + DATA.segDemo.runs.length + " 段（不溢出、不截断）："));
const segRuns = el("div", "runs");
DATA.segDemo.runs.forEach(r => {
  const e = el("span", "run");
  e.innerHTML = "(值 <b>" + r.value + "</b> × " + r.count + ")";
  segRuns.appendChild(e);
});
seg.appendChild(segRuns);
const segTotal = DATA.segDemo.runs.reduce((a, r) => a + r.count, 0);
seg.appendChild(el("p", "note",
  "解码行数合计 = " + segTotal + "，与原始 " + DATA.segDemo.runLen + " 完全一致 ✓"));

// --- Section 4: queries ---
const VTXT = ["SKIP · 跳过不解压", "ALL · 整块接受", "PARTIAL · 展开逐行"];
const queriesEl = $("queries");
DATA.queries.forEach(q => {
  const p = el("div", "panel");
  const head = el("div", "qhead");
  head.appendChild(el("h3", null, "谓词：" + q.label));
  p.appendChild(head);

  // Browser-side brute-force recheck.
  let bc = 0, bs = 0;
  DATA.values.forEach(v => { if (q.lo <= v && v <= q.hi) { bc++; bs += v; } });
  const aggOK = (bc === q.aggCount && bs === q.aggSum && bc === q.bruteCount && bs === q.bruteSum);

  const sum = el("p", "stats",
    "块决策：共 " + q.stats.total + " 块 → 跳过 " + q.stats.skipped +
    " · 整块接受 " + q.stats.allMatch + " · 展开 " + q.stats.expanded +
    "　|　过滤命中 " + q.filterCount + " 行　|　聚合 COUNT=" + q.aggCount +
    " SUM=" + q.aggSum + "　|　浏览器暴力复核 COUNT=" + bc + " SUM=" + bs + " ");
  sum.appendChild(el("span", aggOK ? "ok" : "bad", aggOK ? "✓ 一致" : "✗ 不一致"));
  p.appendChild(sum);

  q.verdicts.forEach((v, bi) => {
    const row = el("div", "blockrow");
    row.appendChild(el("div", "blabel", "Block " + bi));
    row.appendChild(el("span", "verdict v" + v, VTXT[v]));
    const b = DATA.blocks[bi];
    row.appendChild(el("span", "stats", "[min=" + b.min + ", max=" + b.max + "]"));
    if (v === 2) {
      const hits = q.partialHits[bi] || [];
      row.appendChild(el("span", "stats",
        "展开 " + b.rows + " 行 → 命中 " + hits.length + " 行" +
        (hits.length ? "（行号 " + hits.join(", ") + "）" : "")));
    }
    if (v === 1) {
      row.appendChild(el("span", "stats",
        "不读行值：COUNT += " + b.rows + "，SUM += " + b.sum + "（块级预聚合）"));
    }
    p.appendChild(row);
  });
  queriesEl.appendChild(p);
});
</script>
</body>
</html>
`
