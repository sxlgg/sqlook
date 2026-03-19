package sqlook

import (
	"database/sql"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

const pageSize = 50

// Explorer serves a web UI for browsing a SQLite database.
type Explorer struct {
	db     *sql.DB
	dbName string
	mux    *http.ServeMux
}

// New creates a new Explorer for the given SQLite database file.
// The database is opened in read-only mode.
func New(dbPath string) (*Explorer, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("database not found: %s", dbPath)
	}

	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("file:%s?mode=ro", abs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to database: %w", err)
	}

	e := &Explorer{
		db:     db,
		dbName: filepath.Base(dbPath),
		mux:    http.NewServeMux(),
	}
	e.setupRoutes()
	return e, nil
}

func (e *Explorer) setupRoutes() {
	e.mux.HandleFunc("GET /{$}", e.handleIndex)
	e.mux.HandleFunc("GET /api/tables", e.handleTables)
	e.mux.HandleFunc("GET /api/table/{name}", e.handleTable)
	e.mux.HandleFunc("POST /api/query", e.handleQuery)
}

// Start starts the web server. Pass 0 for a random available port.
func (e *Explorer) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	fmt.Printf("sqlook → http://localhost:%d\n", ln.Addr().(*net.TCPAddr).Port)
	return http.Serve(ln, e.mux)
}

// Handler returns the http.Handler for embedding in another server.
func (e *Explorer) Handler() http.Handler {
	return e.mux
}

// Close closes the database connection.
func (e *Explorer) Close() error {
	return e.db.Close()
}

// --- handlers ---

func (e *Explorer) handleIndex(w http.ResponseWriter, r *http.Request) {
	t := template.Must(template.New("index").Parse(indexHTML))
	t.Execute(w, map[string]string{"DBName": e.dbName})
}

func (e *Explorer) handleTables(w http.ResponseWriter, r *http.Request) {
	rows, err := e.db.Query(`SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()

	var b strings.Builder
	for rows.Next() {
		var name, typ string
		rows.Scan(&name, &typ)
		esc := template.HTMLEscapeString(name)
		urlName := url.PathEscape(name)
		label := esc
		if typ == "view" {
			label += ` <span style="opacity:.4;font-size:11px">(view)</span>`
		}
		fmt.Fprintf(&b,
			`<button class="table-btn" data-table="%s" hx-get="/api/table/%s" hx-target="#results" onclick="activateBtn(this)">%s</button>`,
			esc, urlName, label)
	}
	if b.Len() == 0 {
		b.WriteString(`<div style="padding:20px;color:#6b7280;font-size:13px">No tables found</div>`)
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(b.String()))
}

func (e *Explorer) handleTable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	// row count
	var count int
	e.db.QueryRow("SELECT COUNT(*) FROM " + quoteIdent(name)).Scan(&count)

	// schema
	schema := e.renderSchema(name)

	// data
	rows, err := e.db.Query(fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", quoteIdent(name), pageSize, offset))
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	dataHTML := renderRows(rows)

	// pagination
	totalPages := (count + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	pagination := renderPagination(name, page, totalPages, count)

	w.Header().Set("Content-Type", "text/html")
	esc := template.HTMLEscapeString(name)
	fmt.Fprintf(w, `<div class="table-header"><h2>%s</h2><span class="meta">%d rows</span></div>%s%s%s`,
		esc, count, schema, dataHTML, pagination)
}

func (e *Explorer) handleQuery(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("query"))
	if q == "" {
		writeError(w, fmt.Errorf("empty query"))
		return
	}
	rows, err := e.db.Query(q)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(renderRows(rows)))
}

// --- helpers ---

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func (e *Explorer) renderSchema(table string) string {
	rows, err := e.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return ""
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString(`<details class="schema-section"><summary>Schema</summary><table>`)
	b.WriteString(`<tr><th>Column</th><th>Type</th><th>Nullable</th><th>Default</th><th>PK</th></tr>`)
	for rows.Next() {
		var cid, notNull, pk int
		var colName, colType string
		var dflt sql.NullString
		rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk)
		nullable := "yes"
		if notNull == 1 {
			nullable = "no"
		}
		dfltVal := ""
		if dflt.Valid {
			dfltVal = dflt.String
		}
		pkMark := ""
		if pk > 0 {
			pkMark = "PK"
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			template.HTMLEscapeString(colName),
			template.HTMLEscapeString(colType),
			nullable,
			template.HTMLEscapeString(dfltVal),
			pkMark)
	}
	b.WriteString(`</table></details>`)
	return b.String()
}

func renderRows(rows *sql.Rows) string {
	cols, err := rows.Columns()
	if err != nil {
		return `<div class="error">` + template.HTMLEscapeString(err.Error()) + `</div>`
	}

	values := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}

	var b strings.Builder
	b.WriteString(`<table class="data-table"><thead><tr>`)
	for _, c := range cols {
		b.WriteString(`<th>` + template.HTMLEscapeString(c) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)

	n := 0
	for rows.Next() {
		rows.Scan(ptrs...)
		b.WriteString(`<tr>`)
		for _, v := range values {
			if v == nil {
				b.WriteString(`<td><span class="null">NULL</span></td>`)
			} else {
				var s string
				switch val := v.(type) {
				case []byte:
					s = string(val)
				default:
					s = fmt.Sprintf("%v", val)
				}
				b.WriteString(`<td>` + template.HTMLEscapeString(s) + `</td>`)
			}
		}
		b.WriteString(`</tr>`)
		n++
	}
	b.WriteString(`</tbody></table>`)
	if n == 0 {
		return `<div class="empty">No rows returned</div>`
	}
	return b.String()
}

func renderPagination(table string, page, totalPages, count int) string {
	esc := url.PathEscape(table)
	var b strings.Builder
	b.WriteString(`<div class="pagination">`)
	if page > 1 {
		fmt.Fprintf(&b, `<button hx-get="/api/table/%s?page=%d" hx-target="#results">&#8592; Prev</button>`, esc, page-1)
	} else {
		b.WriteString(`<button disabled>&#8592; Prev</button>`)
	}
	fmt.Fprintf(&b, `<span>Page %d of %d &middot; %d rows</span>`, page, totalPages, count)
	if page < totalPages {
		fmt.Fprintf(&b, `<button hx-get="/api/table/%s?page=%d" hx-target="#results">Next &#8594;</button>`, esc, page+1)
	} else {
		b.WriteString(`<button disabled>Next &#8594;</button>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="error">%s</div>`, template.HTMLEscapeString(err.Error()))
}

// --- HTML template ---

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
.sidebar{width:240px;background:#111827;color:#9ca3af;flex-shrink:0;display:flex;flex-direction:column;overflow:hidden}
.sidebar-head{padding:20px;border-bottom:1px solid #1f2937}
.sidebar-head h1{font-size:18px;font-weight:700;color:#f9fafb;letter-spacing:-.5px}
.sidebar-head .db{font-size:12px;color:#6b7280;margin-top:4px;font-family:'SF Mono',Monaco,'Cascadia Code',monospace;word-break:break-all}
.section-label{padding:16px 20px 8px;font-size:11px;text-transform:uppercase;letter-spacing:.05em;color:#6b7280;font-weight:600}
#table-list{overflow-y:auto;flex:1}
.table-btn{display:block;width:100%;text-align:left;background:none;border:none;color:#d1d5db;padding:8px 20px;font-size:13px;cursor:pointer;font-family:'SF Mono',Monaco,monospace;transition:background .1s}
.table-btn:hover{background:#1f2937;color:#f9fafb}
.table-btn.active{background:#1f2937;color:#60a5fa;border-left:2px solid #3b82f6}
.main{flex:1;display:flex;flex-direction:column;overflow:hidden}
.query-bar{padding:16px 24px;background:#fff;border-bottom:1px solid #e5e7eb;display:flex;gap:8px;align-items:flex-start}
.query-bar textarea{flex:1;padding:10px 14px;border:1px solid #d1d5db;border-radius:6px;font-family:'SF Mono',Monaco,monospace;font-size:13px;resize:vertical;min-height:40px;max-height:200px;outline:none;transition:border-color .15s}
.query-bar textarea:focus{border-color:#3b82f6;box-shadow:0 0 0 3px rgba(59,130,246,.1)}
.run-btn{padding:10px 20px;background:#111827;color:#fff;border:none;border-radius:6px;font-size:13px;font-weight:500;cursor:pointer;white-space:nowrap;transition:background .15s}
.run-btn:hover{background:#374151}
.run-btn .kbd{opacity:.5;font-size:11px;margin-left:6px}
#results{flex:1;overflow:auto;padding:24px}
.table-header{display:flex;align-items:baseline;gap:12px;margin-bottom:16px}
.table-header h2{font-size:18px;font-weight:600}
.table-header .meta{font-size:13px;color:#6b7280}
.schema-section{margin-bottom:16px;border:1px solid #e5e7eb;border-radius:8px;font-size:13px}
.schema-section summary{padding:10px 16px;cursor:pointer;color:#6b7280;font-weight:500;user-select:none}
.schema-section table{width:100%;border-collapse:collapse;padding:0 16px 16px}
.schema-section th{text-align:left;color:#6b7280;font-weight:500;padding:4px 16px}
.schema-section td{padding:4px 16px;font-family:'SF Mono',Monaco,monospace}
.data-table{width:100%;border-collapse:collapse;background:#fff;border-radius:8px;overflow:hidden;border:1px solid #e5e7eb;font-size:13px}
.data-table th{background:#f9fafb;text-align:left;padding:10px 14px;font-weight:500;color:#374151;border-bottom:1px solid #e5e7eb;font-family:'SF Mono',Monaco,monospace;font-size:12px;white-space:nowrap}
.data-table td{padding:8px 14px;border-bottom:1px solid #f3f4f6;font-family:'SF Mono',Monaco,monospace;max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.data-table tr:hover td{background:#f9fafb}
.null{color:#9ca3af;font-style:italic}
.pagination{display:flex;align-items:center;justify-content:space-between;margin-top:16px;font-size:13px;color:#6b7280}
.pagination button{padding:6px 14px;border:1px solid #d1d5db;background:#fff;border-radius:6px;cursor:pointer;font-size:13px;transition:all .15s}
.pagination button:hover:not(:disabled){background:#f9fafb;border-color:#9ca3af}
.pagination button:disabled{opacity:.35;cursor:default}
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
    <textarea id="sql" name="query" placeholder="SELECT * FROM ..." rows="1"></textarea>
    <button id="run-btn" class="run-btn" hx-post="/api/query" hx-target="#results" hx-include="#sql">Run<span class="kbd">&#8984;&#9166;</span></button>
  </div>
  <div id="results">
    <div class="welcome">Select a table or run a query</div>
  </div>
</main>
<script>
function activateBtn(el){
  document.querySelectorAll('.table-btn').forEach(function(b){b.classList.remove('active')});
  el.classList.add('active');
  var name=el.getAttribute('data-table');
  document.getElementById('sql').value='SELECT * FROM "'+name.replace(/"/g,'""')+'" LIMIT 100';
}
document.addEventListener('keydown',function(e){
  if((e.metaKey||e.ctrlKey)&&e.key==='Enter'){
    e.preventDefault();
    document.getElementById('run-btn').click();
  }
});
var ta=document.getElementById('sql');
ta.addEventListener('input',function(){this.style.height='auto';this.style.height=this.scrollHeight+'px'});
</script>
</body>
</html>`
