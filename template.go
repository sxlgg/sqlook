package sqlook

// ── HTML template ─────────────────────────────────────────────────────

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>sqlook &mdash; {{.DBName}}</title>
<script src="https://unpkg.com/htmx.org@2.0.4"></script>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',system-ui,sans-serif;background:#f8f9fa;color:#1a1a1a;display:flex;height:100vh}

/* sidebar */
.sidebar{width:240px;background:#111827;color:#9ca3af;flex-shrink:0;display:flex;flex-direction:column;overflow:hidden}
.sidebar-head{padding:20px;border-bottom:1px solid #1f2937}
.sidebar-head h1{font-size:18px;font-weight:700;color:#f9fafb;letter-spacing:-.5px}
.sidebar-head .db{font-size:12px;color:#6b7280;margin-top:4px;font-family:'SF Mono',Monaco,'Cascadia Code',monospace;word-break:break-all}
.section-label{padding:16px 20px 8px;font-size:11px;text-transform:uppercase;letter-spacing:.05em;color:#6b7280;font-weight:600}
#table-list{overflow-y:auto;flex:1}
.table-btn{display:block;width:100%;text-align:left;background:none;border:none;color:#d1d5db;padding:8px 20px;font-size:13px;cursor:pointer;font-family:'SF Mono',Monaco,monospace;transition:background .1s}
.table-btn:hover{background:#1f2937;color:#f9fafb}
.table-btn.active{background:#1f2937;color:#60a5fa;border-left:2px solid #3b82f6}

/* main layout */
.main{flex:1;display:flex;flex-direction:column;overflow:hidden}

/* query editor */
.query-bar{padding:16px 24px;background:#fff;border-bottom:1px solid #e5e7eb;display:flex;gap:8px;align-items:flex-start}
.editor-wrap{position:relative;flex:1;border:1px solid #d1d5db;border-radius:6px;background:#fff;transition:border-color .15s,box-shadow .15s}
.editor-wrap.focused{border-color:#3b82f6;box-shadow:0 0 0 3px rgba(59,130,246,.1)}
.highlight-layer,.editor-textarea{font-family:'SF Mono',Monaco,'Cascadia Code',monospace;font-size:13px;line-height:1.5;padding:10px 14px;margin:0;border:none;white-space:pre-wrap;word-wrap:break-word;overflow-wrap:break-word}
.highlight-layer{position:absolute;top:0;left:0;right:0;bottom:0;pointer-events:none;overflow:hidden;border-radius:6px}
.highlight-layer code{font-family:inherit;font-size:inherit;line-height:inherit}
.editor-textarea{position:relative;width:100%;min-height:40px;max-height:200px;background:transparent;color:transparent;caret-color:#1a1a1a;resize:vertical;outline:none;display:block}
.editor-textarea::placeholder{color:#9ca3af}
.hl-kw{color:#7c3aed;font-weight:600}
.hl-str{color:#059669}
.hl-num{color:#d97706}
.hl-comment{color:#9ca3af;font-style:italic}

.run-btn{padding:10px 20px;background:#111827;color:#fff;border:none;border-radius:6px;font-size:13px;font-weight:500;cursor:pointer;white-space:nowrap;transition:background .15s}
.run-btn:hover{background:#374151}
.run-btn .kbd{opacity:.5;font-size:11px;margin-left:6px}

/* results area */
#results{flex:1;overflow:auto;padding:24px}

/* table header */
.table-header{display:flex;align-items:baseline;justify-content:space-between;margin-bottom:16px;flex-wrap:wrap;gap:8px}
.table-header div{display:flex;align-items:baseline;gap:8px}
.table-header h2{font-size:18px;font-weight:600}
.table-header .meta{font-size:13px;color:#6b7280}

/* export buttons */
.export-btns{display:flex;gap:6px}
.export-btn{display:inline-block;padding:5px 12px;border:1px solid #d1d5db;border-radius:5px;font-size:12px;color:#374151;text-decoration:none;cursor:pointer;background:#fff;font-family:inherit;transition:all .15s}
.export-btn:hover{background:#f3f4f6;border-color:#9ca3af}

/* search bar */
.search-bar{display:flex;align-items:center;gap:8px;margin-bottom:16px;padding:8px 14px;background:#fff;border:1px solid #e5e7eb;border-radius:8px}
.search-bar svg{flex-shrink:0}
.search-bar input{flex:1;border:none;outline:none;font-size:13px;font-family:inherit;background:transparent}
.search-bar input::placeholder{color:#9ca3af}

/* schema */
.schema-section{margin-bottom:16px;border:1px solid #e5e7eb;border-radius:8px;font-size:13px}
.schema-section summary{padding:10px 16px;cursor:pointer;color:#6b7280;font-weight:500;user-select:none}
.schema-section table{width:100%;border-collapse:collapse;padding:0 16px 16px}
.schema-section th{text-align:left;color:#6b7280;font-weight:500;padding:4px 16px}
.schema-section td{padding:4px 16px;font-family:'SF Mono',Monaco,monospace}
.schema-section a{color:#2563eb;text-decoration:none}
.schema-section a:hover{text-decoration:underline}

/* data table */
.table-scroll{overflow-x:auto;border-radius:8px;border:1px solid #e5e7eb}
.data-table{width:100%;border-collapse:collapse;background:#fff;font-size:13px;table-layout:auto}
.data-table th{position:relative;background:#f9fafb;text-align:left;padding:10px 14px;font-weight:500;color:#374151;border-bottom:1px solid #e5e7eb;font-family:'SF Mono',Monaco,monospace;font-size:12px;white-space:nowrap;user-select:none}
.data-table th.sortable{cursor:pointer;padding-right:28px}
.data-table th.sortable:hover{background:#f3f4f6}
.data-table th.sorted{color:#2563eb;background:#eff6ff}
.sort-arrow{font-size:10px;margin-left:4px;opacity:.7}
.resize-handle{position:absolute;right:0;top:0;bottom:0;width:4px;cursor:col-resize;z-index:1}
.resize-handle:hover,.resize-handle.active{background:#3b82f6}
.data-table td{padding:8px 14px;border-bottom:1px solid #f3f4f6;font-family:'SF Mono',Monaco,monospace;max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.data-table tr:hover td{background:#f9fafb}
.data-table tr.clickable{cursor:pointer}
.data-table tr.clickable:hover td{background:#eff6ff}
.pk-col{color:#2563eb}
.null{color:#9ca3af;font-style:italic}
.fk-inline{color:#2563eb;text-decoration:none;font-weight:600;margin-left:4px;cursor:pointer}
.fk-inline:hover{color:#1d4ed8}

/* per-column filter row */
.col-filter-row th{padding:4px;background:#fff}
.col-filter{width:100%;padding:4px 6px;border:1px solid #e5e7eb;border-radius:4px;font-family:'SF Mono',Monaco,monospace;font-size:11px;outline:none;background:#f9fafb}
.col-filter:focus{border-color:#3b82f6;background:#fff}

/* query result meta */
.result-meta{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;font-size:13px;color:#6b7280}
.warn{color:#d97706}
.explain{background:#111827;color:#e5e7eb;padding:12px 16px;border-radius:8px;font-family:'SF Mono',Monaco,monospace;font-size:12px;line-height:1.6;overflow-x:auto;white-space:pre}

/* pagination */
.pagination{display:flex;align-items:center;justify-content:space-between;margin-top:16px;font-size:13px;color:#6b7280}
.pagination button{padding:6px 14px;border:1px solid #d1d5db;background:#fff;border-radius:6px;cursor:pointer;font-size:13px;transition:all .15s}
.pagination button:hover:not(:disabled){background:#f9fafb;border-color:#9ca3af}
.pagination button:disabled{opacity:.35;cursor:default}

/* row detail drawer */
.drawer{position:fixed;top:0;right:0;height:100vh;width:480px;max-width:90vw;background:#fff;border-left:1px solid #e5e7eb;box-shadow:-4px 0 12px rgba(0,0,0,.06);transform:translateX(100%);transition:transform .18s ease-out;z-index:100;overflow-y:auto}
.drawer.open{transform:translateX(0)}
.drawer-inner{padding:16px 20px}
.drawer-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px}
.drawer-head h3{font-size:15px;font-weight:600;font-family:'SF Mono',Monaco,monospace;word-break:break-all}
.detail-table{width:100%;border-collapse:collapse;font-size:12px}
.detail-table th{text-align:left;font-weight:500;color:#6b7280;padding:8px 12px 8px 0;border-bottom:1px solid #f3f4f6;vertical-align:top;font-family:'SF Mono',Monaco,monospace;white-space:nowrap;width:30%}
.detail-table td{padding:8px 0;border-bottom:1px solid #f3f4f6;font-family:'SF Mono',Monaco,monospace;word-break:break-all;vertical-align:top}
.cell-val{white-space:pre-wrap}
.fk-link{display:inline-block;margin-left:8px;color:#2563eb;text-decoration:none;font-size:11px}
.fk-link:hover{text-decoration:underline}
.copy-btn{margin-left:8px;padding:2px 6px;font-size:10px;background:#f3f4f6;border:1px solid #d1d5db;border-radius:4px;cursor:pointer;color:#6b7280}
.copy-btn:hover{background:#e5e7eb}
.copy-btn.copied{background:#10b981;color:#fff;border-color:#10b981}

/* query history dropdown */
.history-wrap{position:relative}
.history-btn{padding:10px 12px;background:#fff;color:#6b7280;border:1px solid #d1d5db;border-radius:6px;font-size:13px;cursor:pointer}
.history-btn:hover{background:#f3f4f6}
.history-menu{position:absolute;top:100%;right:0;margin-top:4px;background:#fff;border:1px solid #e5e7eb;border-radius:6px;box-shadow:0 4px 12px rgba(0,0,0,.08);min-width:360px;max-width:520px;max-height:360px;overflow-y:auto;z-index:50;display:none}
.history-menu.open{display:block}
.history-item{padding:8px 12px;font-family:'SF Mono',Monaco,monospace;font-size:11px;color:#374151;cursor:pointer;border-bottom:1px solid #f3f4f6;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.history-item:hover{background:#f9fafb}
.history-item:last-child{border-bottom:none}
.history-empty{padding:12px;font-size:12px;color:#9ca3af;text-align:center}

/* misc */
.error{background:#fef2f2;border:1px solid #fecaca;color:#991b1b;padding:12px 16px;border-radius:8px;font-size:13px;font-family:'SF Mono',Monaco,monospace;white-space:pre-wrap}
.empty{text-align:center;padding:48px;color:#9ca3af}
.welcome{display:flex;align-items:center;justify-content:center;height:100%;color:#9ca3af;font-size:14px}
</style>
</head>
<body>
<aside class="sidebar">
  <div class="sidebar-head">
    <h1>sqlook</h1>
    <div class="db">{{.DBName}}</div>
  </div>
  <div class="section-label">Tables</div>
  <div id="table-list" hx-get="/api/tables" hx-trigger="load"></div>
</aside>
<main class="main">
  <div class="query-bar">
    <div class="editor-wrap" id="editor-wrap">
      <pre class="highlight-layer" id="hl-pre"><code id="hl"></code></pre>
      <textarea id="sql" name="query" class="editor-textarea" placeholder="SELECT * FROM ..." spellcheck="false" rows="1"></textarea>
    </div>
    <div class="history-wrap">
      <button type="button" class="history-btn" onclick="toggleHistory()" title="Query history">&#9711;</button>
      <div id="history-menu" class="history-menu"></div>
    </div>
    <button id="run-btn" class="run-btn" hx-post="/api/query" hx-target="#results" hx-include="#sql">Run<span class="kbd">&#8984;&#9166;</span></button>
  </div>
  <div id="results">
    <div class="welcome">Select a table or run a query</div>
  </div>
</main>

<!-- row-detail drawer -->
<div id="drawer" class="drawer" onclick="event.stopPropagation()">
  <div id="drawer-body"></div>
</div>

<script>
/* sidebar activation */
function activateBtn(el){
  document.querySelectorAll('.table-btn').forEach(function(b){b.classList.remove('active')});
  el.classList.add('active');
  var name=el.getAttribute('data-table');
  var sqlEl=document.getElementById('sql');
  sqlEl.value='SELECT * FROM "'+name.replace(/"/g,'""')+'" LIMIT 100';
  updateHighlight();
}

/* ── SQL syntax highlighting (tokenizer-based, safe inside strings) ── */
var SQL_KW=new Set(['SELECT','FROM','WHERE','AND','OR','NOT','IN','LIKE','BETWEEN','IS','NULL',
'AS','ON','JOIN','LEFT','RIGHT','INNER','OUTER','CROSS','FULL','NATURAL','ORDER','BY','GROUP',
'HAVING','LIMIT','OFFSET','UNION','ALL','DISTINCT','EXISTS','CASE','WHEN','THEN','ELSE','END',
'INSERT','INTO','VALUES','UPDATE','SET','DELETE','CREATE','DROP','ALTER','TABLE','VIEW','INDEX',
'PRIMARY','KEY','FOREIGN','REFERENCES','DEFAULT','CHECK','UNIQUE','WITH','RECURSIVE','PRAGMA',
'EXPLAIN','QUERY','PLAN','BEGIN','COMMIT','ROLLBACK','TRANSACTION','TRIGGER','IF','REPLACE',
'ABORT','FAIL','IGNORE','TEMP','TEMPORARY','VIRTUAL','REINDEX','RELEASE','SAVEPOINT','VACUUM',
'ATTACH','DETACH','RENAME','ADD','COLUMN','CASCADE','RESTRICT','CONFLICT','COLLATE','AUTOINCREMENT',
'GLOB','MATCH','REGEXP','ESCAPE','EXCEPT','INTERSECT','USING','INDEXED','CAST','ISNULL','NOTNULL',
'COUNT','SUM','AVG','MIN','MAX','TOTAL','GROUP_CONCAT','ABS','UPPER','LOWER','TRIM','ROUND',
'LENGTH','TYPEOF','COALESCE','IFNULL','NULLIF','SUBSTR','INSTR','HEX','ZEROBLOB',
'RANDOM','RANDOMBLOB','UNICODE','QUOTE','LIKELIHOOD','LIKELY','UNLIKELY','IIF','ASC','DESC',
'ILIKE','INTERVAL','TIMESTAMP','DATE','TIME','TRUE','FALSE']);

function escH(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}

function highlightSQL(text){
  var out='',i=0,len=text.length;
  while(i<len){
    if(text[i]==='-'&&i+1<len&&text[i+1]==='-'){
      var end=text.indexOf('\n',i);if(end===-1)end=len;
      out+='<span class="hl-comment">'+escH(text.substring(i,end))+'</span>';i=end;
    }else if(text[i]==="'"){
      var j=i+1;while(j<len){if(text[j]==="'"&&j+1<len&&text[j+1]==="'")j+=2;else if(text[j]==="'"){j++;break;}else j++;}
      if(j>=len&&text[len-1]!=="'")j=len;
      out+='<span class="hl-str">'+escH(text.substring(i,j))+'</span>';i=j;
    }else if(/[a-zA-Z_]/.test(text[i])){
      var s=i;while(i<len&&/[a-zA-Z0-9_]/.test(text[i]))i++;
      var w=text.substring(s,i);
      out+=SQL_KW.has(w.toUpperCase())?'<span class="hl-kw">'+escH(w)+'</span>':escH(w);
    }else if(/[0-9]/.test(text[i])){
      var s=i;while(i<len&&/[0-9.]/.test(text[i]))i++;
      out+='<span class="hl-num">'+escH(text.substring(s,i))+'</span>';
    }else{out+=escH(text[i]);i++;}
  }
  if(len>0&&text[len-1]==='\n')out+=' ';
  return out;
}

var sqlEl=document.getElementById('sql');
var hlEl=document.getElementById('hl');
var hlPre=document.getElementById('hl-pre');
var wrapEl=document.getElementById('editor-wrap');

function updateHighlight(){hlEl.textContent='';hlEl.insertAdjacentHTML('beforeend',highlightSQL(sqlEl.value));}
sqlEl.addEventListener('input',function(){
  updateHighlight();
  this.style.height='auto';this.style.height=this.scrollHeight+'px';
  hlPre.style.height=this.style.height;
});
sqlEl.addEventListener('scroll',function(){hlPre.scrollTop=this.scrollTop;});
sqlEl.addEventListener('focus',function(){wrapEl.classList.add('focused');});
sqlEl.addEventListener('blur',function(){wrapEl.classList.remove('focused');});

/* keyboard shortcuts */
document.addEventListener('keydown',function(e){
  if((e.metaKey||e.ctrlKey)&&e.key==='Enter'){e.preventDefault();document.getElementById('run-btn').click();}
  if(e.key==='Escape'){closeDrawer();closeHistory();}
});

/* history arrow navigation (only when editor focused and at start/end) */
var historyIdx=-1;
sqlEl.addEventListener('keydown',function(e){
  var h=getHistory();
  if(e.key==='ArrowUp'&&(this.selectionStart===0||this.value==='')){
    if(h.length===0)return;
    e.preventDefault();
    historyIdx=Math.min(historyIdx+1,h.length-1);
    this.value=h[historyIdx];updateHighlight();
  }else if(e.key==='ArrowDown'&&this.selectionStart===this.value.length){
    if(historyIdx<=0){historyIdx=-1;this.value='';updateHighlight();return;}
    e.preventDefault();
    historyIdx--;
    this.value=h[historyIdx]||'';updateHighlight();
  }
});

/* ── query history (localStorage, ring buffer of 50) ── */
var HKEY='sqlook_history';
function getHistory(){try{return JSON.parse(localStorage.getItem(HKEY)||'[]');}catch(e){return[];}}
function pushHistory(q){
  q=q.trim();if(!q)return;
  var h=getHistory();
  h=h.filter(function(x){return x!==q;});
  h.unshift(q);if(h.length>50)h=h.slice(0,50);
  localStorage.setItem(HKEY,JSON.stringify(h));
}
function toggleHistory(){
  var m=document.getElementById('history-menu');
  if(m.classList.contains('open')){closeHistory();return;}
  var h=getHistory();
  if(h.length===0){m.innerHTML='<div class="history-empty">No history yet</div>';}
  else{
    m.innerHTML=h.map(function(q){
      return '<div class="history-item" title="'+escH(q)+'" onclick="useHistory(this)">'+escH(q)+'</div>';
    }).join('');
  }
  m.classList.add('open');
}
function closeHistory(){document.getElementById('history-menu').classList.remove('open');}
function useHistory(el){sqlEl.value=el.getAttribute('title');updateHighlight();closeHistory();sqlEl.focus();}
document.addEventListener('click',function(e){
  if(!e.target.closest('.history-wrap'))closeHistory();
});

/* record on successful query submit */
document.body.addEventListener('htmx:beforeRequest',function(e){
  var path=e.detail.requestConfig.path;
  if(path==='/api/query'||path==='/api/explain'){pushHistory(sqlEl.value);historyIdx=-1;}
});

/* ── column resize ── */
var colWidths={};
function initResize(){
  document.querySelectorAll('.resize-handle').forEach(function(handle){
    handle.addEventListener('mousedown',function(e){
      e.preventDefault();e.stopPropagation();
      var th=this.parentElement;
      var startX=e.pageX;
      var startW=th.offsetWidth;
      this.classList.add('active');
      var self=this;
      function onMove(ev){
        var w=Math.max(50,startW+ev.pageX-startX);
        th.style.width=w+'px';th.style.minWidth=w+'px';
        colWidths[th.getAttribute('data-col')]=w;
      }
      function onUp(){
        document.removeEventListener('mousemove',onMove);
        document.removeEventListener('mouseup',onUp);
        self.classList.remove('active');
      }
      document.addEventListener('mousemove',onMove);
      document.addEventListener('mouseup',onUp);
    });
  });
}

/* double-click a cell to expand full content */
function initCellExpand(){
  document.querySelectorAll('.cell-td').forEach(function(td){
    td.addEventListener('dblclick',function(e){
      e.stopPropagation();
      if(this.classList.contains('expanded')){
        this.classList.remove('expanded');
        this.style.whiteSpace='';this.style.maxWidth='';
      }else{
        this.classList.add('expanded');
        this.style.whiteSpace='pre-wrap';this.style.maxWidth='none';
      }
    });
  });
}

/* restore widths + init resize/expand after htmx swap */
document.addEventListener('htmx:afterSwap',function(e){
  initResize();
  initCellExpand();
  Object.keys(colWidths).forEach(function(key){
    var th=document.querySelector('th[data-col="'+CSS.escape(key)+'"]');
    if(th){th.style.width=colWidths[key]+'px';th.style.minWidth=colWidths[key]+'px';}
  });
});

/* ── drawer ── */
function openDrawer(){document.getElementById('drawer').classList.add('open');}
function closeDrawer(){document.getElementById('drawer').classList.remove('open');}
document.addEventListener('click',function(e){
  var d=document.getElementById('drawer');
  if(!d.classList.contains('open'))return;
  if(e.target.closest('#drawer'))return;
  if(e.target.closest('tr.clickable'))return;
  closeDrawer();
});

/* copy-to-clipboard for drawer cells */
function copyText(btn){
  var txt=btn.getAttribute('data-copy');
  navigator.clipboard.writeText(txt).then(function(){
    btn.textContent='copied';btn.classList.add('copied');
    setTimeout(function(){btn.textContent='copy';btn.classList.remove('copied');},1200);
  });
}

/* export custom query results */
function exportQuery(fmt){
  var q=document.getElementById('sql').value;
  if(!q.trim())return;
  var f=document.createElement('form');
  f.method='POST';f.action='/api/export?format='+encodeURIComponent(fmt);f.style.display='none';
  var inp=document.createElement('input');inp.type='hidden';inp.name='query';inp.value=q;
  f.appendChild(inp);document.body.appendChild(f);f.submit();document.body.removeChild(f);
}

/* EXPLAIN the current editor query */
function runExplain(){
  var q=document.getElementById('sql').value;
  if(!q.trim())return;
  var fd=new FormData();fd.append('query',q);
  fetch('/api/explain',{method:'POST',body:fd}).then(function(r){return r.text();}).then(function(html){
    document.getElementById('results').innerHTML=html;
  });
}
</script>
</body>
</html>`
