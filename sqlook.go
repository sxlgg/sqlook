package sqlook

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
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
	e.mux.HandleFunc("GET /api/export/{name}", e.handleExportTable)
	e.mux.HandleFunc("POST /api/export", e.handleExportQuery)
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
func (e *Explorer) Handler() http.Handler { return e.mux }

// Close closes the database connection.
func (e *Explorer) Close() error { return e.db.Close() }

// ── handlers ──────────────────────────────────────────────────────────

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
	sortCol := r.URL.Query().Get("sort")
	dir := r.URL.Query().Get("dir")
	search := r.URL.Query().Get("search")

	cols := e.getColumns(name)

	// search → WHERE clause
	var whereParts []string
	var args []any
	if search != "" {
		for _, col := range cols {
			whereParts = append(whereParts, fmt.Sprintf("CAST(%s AS TEXT) LIKE ?", quoteIdent(col)))
			args = append(args, "%"+search+"%")
		}
	}
	whereSQL := ""
	if len(whereParts) > 0 {
		whereSQL = " WHERE (" + strings.Join(whereParts, " OR ") + ")"
	}

	// count
	var count int
	cArgs := make([]any, len(args))
	copy(cArgs, args)
	e.db.QueryRow("SELECT COUNT(*) FROM "+quoteIdent(name)+whereSQL, cArgs...).Scan(&count)

	// sort
	orderSQL := ""
	if sortCol != "" && isValidColumn(sortCol, cols) {
		if dir != "desc" {
			dir = "asc"
		}
		orderSQL = fmt.Sprintf(" ORDER BY %s %s", quoteIdent(sortCol), strings.ToUpper(dir))
	}

	// data
	offset := (page - 1) * pageSize
	q := fmt.Sprintf("SELECT * FROM %s%s%s LIMIT %d OFFSET %d",
		quoteIdent(name), whereSQL, orderSQL, pageSize, offset)
	rows, err := e.db.Query(q, args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()

	totalPages := max((count+pageSize-1)/pageSize, 1)

	w.Header().Set("Content-Type", "text/html")
	var b strings.Builder

	// header + export
	esc := template.HTMLEscapeString(name)
	exportBase := fmt.Sprintf("/api/export/%s?sort=%s&dir=%s&search=%s",
		url.PathEscape(name), url.QueryEscape(sortCol), url.QueryEscape(dir), url.QueryEscape(search))
	fmt.Fprintf(&b, `<div class="table-header"><div><h2>%s</h2><span class="meta">%d rows</span></div><div class="export-btns"><a href="%s&format=csv" class="export-btn">CSV</a><a href="%s&format=json" class="export-btn">JSON</a></div></div>`,
		esc, count,
		template.HTMLEscapeString(exportBase),
		template.HTMLEscapeString(exportBase))

	// schema
	b.WriteString(e.renderSchema(name))

	// search bar
	searchBase := fmt.Sprintf("/api/table/%s?sort=%s&dir=%s",
		url.PathEscape(name), url.QueryEscape(sortCol), url.QueryEscape(dir))
	fmt.Fprintf(&b, `<div class="search-bar"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#9ca3af" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg><input type="text" name="search" value="%s" placeholder="Filter rows..." hx-get="%s" hx-trigger="keyup changed delay:300ms" hx-target="#results" hx-include="this"></div>`,
		template.HTMLEscapeString(search), template.HTMLEscapeString(searchBase))

	// data table with sortable, resizable headers
	b.WriteString(renderTableRows(rows, name, sortCol, dir, search))

	// pagination
	b.WriteString(renderPagination(name, page, totalPages, count, sortCol, dir, search))

	w.Write([]byte(b.String()))
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
	w.Write([]byte(renderQueryResults(rows)))
}

func (e *Explorer) handleExportTable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	format := r.URL.Query().Get("format")
	sortCol := r.URL.Query().Get("sort")
	dir := r.URL.Query().Get("dir")
	search := r.URL.Query().Get("search")

	cols := e.getColumns(name)

	var whereParts []string
	var args []any
	if search != "" {
		for _, col := range cols {
			whereParts = append(whereParts, fmt.Sprintf("CAST(%s AS TEXT) LIKE ?", quoteIdent(col)))
			args = append(args, "%"+search+"%")
		}
	}
	whereSQL := ""
	if len(whereParts) > 0 {
		whereSQL = " WHERE (" + strings.Join(whereParts, " OR ") + ")"
	}
	orderSQL := ""
	if sortCol != "" && isValidColumn(sortCol, cols) {
		if dir != "desc" {
			dir = "asc"
		}
		orderSQL = fmt.Sprintf(" ORDER BY %s %s", quoteIdent(sortCol), strings.ToUpper(dir))
	}

	q := "SELECT * FROM " + quoteIdent(name) + whereSQL + orderSQL
	rows, err := e.db.Query(q, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer rows.Close()

	if format == "json" {
		exportJSON(w, name, rows)
	} else {
		exportCSV(w, name, rows)
	}
}

func (e *Explorer) handleExportQuery(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("query"))
	format := r.URL.Query().Get("format")
	if q == "" {
		http.Error(w, "empty query", http.StatusBadRequest)
		return
	}
	rows, err := e.db.Query(q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer rows.Close()

	if format == "json" {
		exportJSON(w, "query_result", rows)
	} else {
		exportCSV(w, "query_result", rows)
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func (e *Explorer) getColumns(table string) []string {
	rows, err := e.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk)
		cols = append(cols, name)
	}
	return cols
}

func isValidColumn(col string, cols []string) bool {
	for _, c := range cols {
		if c == col {
			return true
		}
	}
	return false
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

func renderTableRows(rows *sql.Rows, table, sortCol, dir, search string) string {
	cols, err := rows.Columns()
	if err != nil {
		return `<div class="error">` + template.HTMLEscapeString(err.Error()) + `</div>`
	}

	var b strings.Builder
	b.WriteString(`<div class="table-scroll"><table class="data-table"><thead><tr>`)

	for _, col := range cols {
		newDir := "asc"
		indicator := ""
		cls := "sortable"
		if col == sortCol {
			cls += " sorted"
			if dir == "asc" {
				newDir = "desc"
				indicator = ` <span class="sort-arrow">&#9650;</span>`
			} else {
				indicator = ` <span class="sort-arrow">&#9660;</span>`
			}
		}
		href := fmt.Sprintf("/api/table/%s?sort=%s&dir=%s&search=%s",
			url.PathEscape(table), url.QueryEscape(col), newDir, url.QueryEscape(search))
		fmt.Fprintf(&b, `<th class="%s" data-col="%s" hx-get="%s" hx-target="#results">%s%s<div class="resize-handle"></div></th>`,
			cls,
			template.HTMLEscapeString(col),
			template.HTMLEscapeString(href),
			template.HTMLEscapeString(col),
			indicator)
	}
	b.WriteString(`</tr></thead><tbody>`)

	n := scanRows(&b, rows, cols)

	b.WriteString(`</tbody></table></div>`)
	if n == 0 {
		return `<div class="empty">No rows match</div>`
	}
	return b.String()
}

func renderQueryResults(rows *sql.Rows) string {
	cols, err := rows.Columns()
	if err != nil {
		return `<div class="error">` + template.HTMLEscapeString(err.Error()) + `</div>`
	}

	var b strings.Builder
	b.WriteString(`<div class="table-scroll"><table class="data-table"><thead><tr>`)
	for _, c := range cols {
		fmt.Fprintf(&b, `<th data-col="%s">%s<div class="resize-handle"></div></th>`,
			template.HTMLEscapeString(c), template.HTMLEscapeString(c))
	}
	b.WriteString(`</tr></thead><tbody>`)

	n := scanRows(&b, rows, cols)

	b.WriteString(`</tbody></table></div>`)
	if n == 0 {
		return `<div class="empty">No rows returned</div>`
	}

	meta := fmt.Sprintf(`<div class="result-meta"><span>%d rows returned</span><div class="export-btns"><button onclick="exportQuery('csv')" class="export-btn">CSV</button><button onclick="exportQuery('json')" class="export-btn">JSON</button></div></div>`, n)
	return meta + b.String()
}

func scanRows(b *strings.Builder, rows *sql.Rows, cols []string) int {
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
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
				b.WriteString(`<td title="` + template.HTMLEscapeString(s) + `">` + template.HTMLEscapeString(s) + `</td>`)
			}
		}
		b.WriteString(`</tr>`)
		n++
	}
	return n
}

func renderPagination(table string, page, totalPages, count int, sortCol, dir, search string) string {
	base := fmt.Sprintf("/api/table/%s?sort=%s&dir=%s&search=%s",
		url.PathEscape(table), url.QueryEscape(sortCol), url.QueryEscape(dir), url.QueryEscape(search))
	var b strings.Builder
	b.WriteString(`<div class="pagination">`)
	if page > 1 {
		fmt.Fprintf(&b, `<button hx-get="%s&page=%d" hx-target="#results">&#8592; Prev</button>`,
			template.HTMLEscapeString(base), page-1)
	} else {
		b.WriteString(`<button disabled>&#8592; Prev</button>`)
	}
	fmt.Fprintf(&b, `<span>Page %d of %d &middot; %d rows</span>`, page, totalPages, count)
	if page < totalPages {
		fmt.Fprintf(&b, `<button hx-get="%s&page=%d" hx-target="#results">Next &#8594;</button>`,
			template.HTMLEscapeString(base), page+1)
	} else {
		b.WriteString(`<button disabled>Next &#8594;</button>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func exportCSV(w http.ResponseWriter, name string, rows *sql.Rows) {
	cols, _ := rows.Columns()
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, name))

	wr := csv.NewWriter(w)
	wr.Write(cols)

	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	for rows.Next() {
		rows.Scan(ptrs...)
		rec := make([]string, len(cols))
		for i, v := range values {
			if v != nil {
				switch val := v.(type) {
				case []byte:
					rec[i] = string(val)
				default:
					rec[i] = fmt.Sprintf("%v", val)
				}
			}
		}
		wr.Write(rec)
	}
	wr.Flush()
}

func exportJSON(w http.ResponseWriter, name string, rows *sql.Rows) {
	cols, _ := rows.Columns()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, name))

	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}

	var results []map[string]any
	for rows.Next() {
		rows.Scan(ptrs...)
		row := make(map[string]any)
		for i, col := range cols {
			if values[i] == nil {
				row[col] = nil
			} else {
				switch v := values[i].(type) {
				case []byte:
					row[col] = string(v)
				default:
					row[col] = v
				}
			}
		}
		results = append(results, row)
	}
	if results == nil {
		results = []map[string]any{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(results)
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="error">%s</div>`, template.HTMLEscapeString(err.Error()))
}

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
.null{color:#9ca3af;font-style:italic}

/* query result meta */
.result-meta{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;font-size:13px;color:#6b7280}

/* pagination */
.pagination{display:flex;align-items:center;justify-content:space-between;margin-top:16px;font-size:13px;color:#6b7280}
.pagination button{padding:6px 14px;border:1px solid #d1d5db;background:#fff;border-radius:6px;cursor:pointer;font-size:13px;transition:all .15s}
.pagination button:hover:not(:disabled){background:#f9fafb;border-color:#9ca3af}
.pagination button:disabled{opacity:.35;cursor:default}

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
    <button id="run-btn" class="run-btn" hx-post="/api/query" hx-target="#results" hx-include="#sql">Run<span class="kbd">&#8984;&#9166;</span></button>
  </div>
  <div id="results">
    <div class="welcome">Select a table or run a query</div>
  </div>
</main>
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
'RANDOM','RANDOMBLOB','UNICODE','QUOTE','LIKELIHOOD','LIKELY','UNLIKELY','IIF','ASC','DESC']);

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

/* keyboard shortcut */
document.addEventListener('keydown',function(e){
  if((e.metaKey||e.ctrlKey)&&e.key==='Enter'){e.preventDefault();document.getElementById('run-btn').click();}
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

/* restore widths + init resize after htmx swap */
document.addEventListener('htmx:afterSwap',function(){
  initResize();
  Object.keys(colWidths).forEach(function(key){
    var th=document.querySelector('th[data-col="'+CSS.escape(key)+'"]');
    if(th){th.style.width=colWidths[key]+'px';th.style.minWidth=colWidths[key]+'px';}
  });
});

/* export custom query results */
function exportQuery(fmt){
  var q=document.getElementById('sql').value;
  if(!q.trim())return;
  var f=document.createElement('form');
  f.method='POST';f.action='/api/export?format='+encodeURIComponent(fmt);f.style.display='none';
  var inp=document.createElement('input');inp.type='hidden';inp.name='query';inp.value=q;
  f.appendChild(inp);document.body.appendChild(f);f.submit();document.body.removeChild(f);
}
</script>
</body>
</html>`
