package sqlook

import (
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pure-Go Postgres driver
	_ "modernc.org/sqlite"             // pure-Go SQLite driver
)

const pageSize = 50

// Options configure an Explorer. Zero values are fine.
type Options struct {
	// StatementTimeout, when non-empty, is applied to the session on Postgres
	// (e.g. "30s", "2min"). Ignored for SQLite.
	StatementTimeout string

	// AutoLimit, if > 0, silently appends "LIMIT N" to any SELECT in the
	// query editor that doesn't already have a LIMIT. Zero disables.
	AutoLimit int

	// BasicAuthUser / BasicAuthPass, when both non-empty, require HTTP
	// basic auth on every request.
	BasicAuthUser string
	BasicAuthPass string
}

// DefaultOptions is used by New. Safe defaults: 30s statement timeout on
// Postgres, auto-LIMIT 1000 on ad-hoc queries, no auth.
var DefaultOptions = Options{
	StatementTimeout: "30s",
	AutoLimit:        1000,
}

// Explorer serves a web UI for browsing a SQL database.
type Explorer struct {
	db      *sql.DB
	dbName  string
	dialect dialect
	mux     *http.ServeMux
	opts    Options
}

// New creates a new Explorer for the given connection string with
// DefaultOptions. See NewWithOptions for full control.
func New(connStr string) (*Explorer, error) {
	return NewWithOptions(connStr, DefaultOptions)
}

// NewWithOptions creates an Explorer with custom options.
func NewWithOptions(connStr string, opts Options) (*Explorer, error) {
	db, d, name, err := openDB(connStr, opts)
	if err != nil {
		return nil, err
	}
	e := &Explorer{
		db:      db,
		dbName:  name,
		dialect: d,
		mux:     http.NewServeMux(),
		opts:    opts,
	}
	e.setupRoutes()
	return e, nil
}

func (e *Explorer) setupRoutes() {
	e.mux.HandleFunc("GET /{$}", e.handleIndex)
	e.mux.HandleFunc("GET /api/tables", e.handleTables)
	e.mux.HandleFunc("GET /api/table/{name}", e.handleTable)
	e.mux.HandleFunc("GET /api/row/{name}", e.handleRow)
	e.mux.HandleFunc("POST /api/query", e.handleQuery)
	e.mux.HandleFunc("POST /api/explain", e.handleExplain)
	e.mux.HandleFunc("GET /api/export/{name}", e.handleExportTable)
	e.mux.HandleFunc("POST /api/export", e.handleExportQuery)
}

// Start starts the web server. Pass 0 for a random available port.
// bind is the bind address (e.g. "127.0.0.1" or "" for all interfaces).
func (e *Explorer) Start(port int) error {
	return e.StartOn("", port)
}

// StartOn starts the web server on a specific bind address and port.
func (e *Explorer) StartOn(bind string, port int) error {
	addr := fmt.Sprintf("%s:%d", bind, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	shown := bind
	if shown == "" || shown == "0.0.0.0" {
		shown = "localhost"
	}
	fmt.Printf("sqlook → http://%s:%d\n", shown, ln.Addr().(*net.TCPAddr).Port)
	return http.Serve(ln, e.withAuth(e.mux))
}

// Handler returns the http.Handler for embedding in another server.
// Basic-auth middleware is applied if configured.
func (e *Explorer) Handler() http.Handler { return e.withAuth(e.mux) }

// Close closes the database connection.
func (e *Explorer) Close() error { return e.db.Close() }

// ── middleware ────────────────────────────────────────────────────────

func (e *Explorer) withAuth(next http.Handler) http.Handler {
	if e.opts.BasicAuthUser == "" || e.opts.BasicAuthPass == "" {
		return next
	}
	wantU := []byte(e.opts.BasicAuthUser)
	wantP := []byte(e.opts.BasicAuthPass)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), wantU) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), wantP) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="sqlook"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── handlers ──────────────────────────────────────────────────────────

func (e *Explorer) handleIndex(w http.ResponseWriter, r *http.Request) {
	t := template.Must(template.New("index").Parse(indexHTML))
	t.Execute(w, map[string]string{"DBName": e.dbName})
}

func (e *Explorer) handleTables(w http.ResponseWriter, r *http.Request) {
	tables, err := e.dialect.listTables(e.db)
	if err != nil {
		writeError(w, err)
		return
	}

	var b strings.Builder
	for _, t := range tables {
		esc := template.HTMLEscapeString(t.Name)
		urlName := url.PathEscape(t.Name)
		label := esc
		if t.Type == "view" {
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

// parseColFilters extracts filter_<col>=value query params.
func parseColFilters(r *http.Request, cols []string) []colFilter {
	var out []colFilter
	for _, c := range cols {
		v := r.URL.Query().Get("filter_" + c)
		if v != "" {
			out = append(out, colFilter{Col: c, Val: v})
		}
	}
	return out
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

	cols := e.dialect.columns(e.db, name)
	qualified := e.dialect.qualifyTable(name)
	colFilters := parseColFilters(r, cols)

	// Build WHERE: search OR-of-all-cols, AND-ed with per-column equality filters.
	var whereParts []string
	var args []any
	if frag, a := e.dialect.buildSearch(cols, search); frag != "" {
		whereParts = append(whereParts, frag)
		args = append(args, a...)
	}
	if frag, a := e.dialect.buildEq(colFilters, len(args)+1); frag != "" {
		whereParts = append(whereParts, frag)
		args = append(args, a...)
	}
	whereSQL := ""
	if len(whereParts) > 0 {
		whereSQL = " WHERE " + strings.Join(whereParts, " AND ")
	}

	// count
	var count int
	cArgs := make([]any, len(args))
	copy(cArgs, args)
	e.db.QueryRow("SELECT COUNT(*) FROM "+qualified+whereSQL, cArgs...).Scan(&count)

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
		qualified, whereSQL, orderSQL, pageSize, offset)
	start := time.Now()
	rows, err := e.db.Query(q, args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()

	totalPages := max((count+pageSize-1)/pageSize, 1)

	w.Header().Set("Content-Type", "text/html")
	var b strings.Builder

	pk := e.dialect.primaryKey(e.db, name)
	fks := e.dialect.foreignKeys(e.db, name)

	// header + export
	esc := template.HTMLEscapeString(name)
	exportBase := fmt.Sprintf("/api/export/%s?sort=%s&dir=%s&search=%s",
		url.PathEscape(name), url.QueryEscape(sortCol), url.QueryEscape(dir), url.QueryEscape(search))
	for _, f := range colFilters {
		exportBase += "&filter_" + url.QueryEscape(f.Col) + "=" + url.QueryEscape(f.Val)
	}
	fmt.Fprintf(&b, `<div class="table-header"><div><h2>%s</h2><span class="meta">%d rows</span></div><div class="export-btns"><a href="%s&format=csv" class="export-btn">CSV</a><a href="%s&format=json" class="export-btn">JSON</a></div></div>`,
		esc, count,
		template.HTMLEscapeString(exportBase),
		template.HTMLEscapeString(exportBase))

	// schema
	b.WriteString(e.dialect.renderSchema(e.db, name))

	// search bar
	searchBase := fmt.Sprintf("/api/table/%s?sort=%s&dir=%s",
		url.PathEscape(name), url.QueryEscape(sortCol), url.QueryEscape(dir))
	for _, f := range colFilters {
		searchBase += "&filter_" + url.QueryEscape(f.Col) + "=" + url.QueryEscape(f.Val)
	}
	fmt.Fprintf(&b, `<div class="search-bar"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#9ca3af" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg><input type="text" name="search" value="%s" placeholder="Filter rows..." hx-get="%s" hx-trigger="keyup changed delay:300ms" hx-target="#results" hx-include="this"></div>`,
		template.HTMLEscapeString(search), template.HTMLEscapeString(searchBase))

	// data table with sortable, resizable headers + per-column filters
	b.WriteString(renderTableRowsFull(rows, name, sortCol, dir, search, pk, fks, colFilters))

	// pagination + timing
	elapsed := time.Since(start)
	b.WriteString(renderPagination(name, page, totalPages, count, sortCol, dir, search, colFilters, elapsed))

	w.Write([]byte(b.String()))
}

// handleRow returns a vertical detail drawer for a single row, identified
// by primary-key equality filters in query params: pk_<col>=value.
func (e *Explorer) handleRow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pk := e.dialect.primaryKey(e.db, name)
	cols := e.dialect.columns(e.db, name)
	qualified := e.dialect.qualifyTable(name)

	// If the table has a PK, look up by PK. Otherwise fall back to full-row match.
	var filters []colFilter
	keyCols := pk
	if len(keyCols) == 0 {
		keyCols = cols
	}
	for _, c := range keyCols {
		v := r.URL.Query().Get("pk_" + c)
		if v == "" {
			http.Error(w, "missing pk_"+c, http.StatusBadRequest)
			return
		}
		filters = append(filters, colFilter{Col: c, Val: v})
	}
	frag, args := e.dialect.buildEq(filters, 1)
	q := "SELECT * FROM " + qualified + " WHERE " + frag + " LIMIT 1"
	rows, err := e.db.Query(q, args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()

	rowCols, _ := rows.Columns()
	if !rows.Next() {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<div class="drawer-inner"><div class="drawer-head"><h3>Not found</h3><button onclick="closeDrawer()" class="export-btn">Close</button></div></div>`))
		return
	}
	vals := make([]any, len(rowCols))
	ptrs := make([]any, len(rowCols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	rows.Scan(ptrs...)

	fks := e.dialect.foreignKeys(e.db, name)

	var b strings.Builder
	b.WriteString(`<div class="drawer-inner">`)
	fmt.Fprintf(&b, `<div class="drawer-head"><h3>%s</h3><button onclick="closeDrawer()" class="export-btn">Close</button></div>`,
		template.HTMLEscapeString(name))
	b.WriteString(`<table class="detail-table">`)
	for i, col := range rowCols {
		var cell string
		if vals[i] == nil {
			cell = `<span class="null">NULL</span>`
		} else {
			var s string
			switch val := vals[i].(type) {
			case []byte:
				s = string(val)
			default:
				s = fmt.Sprintf("%v", val)
			}
			cell = `<span class="cell-val">` + template.HTMLEscapeString(s) + `</span>`
			if ref, ok := fks[col]; ok {
				cell += fmt.Sprintf(` <a class="fk-link" href="#" hx-get="/api/table/%s?filter_%s=%s" hx-target="#results" onclick="closeDrawer()">→ %s</a>`,
					url.PathEscape(ref.Table), url.QueryEscape(ref.Column), url.QueryEscape(s),
					template.HTMLEscapeString(ref.Table))
			}
		}
		fmt.Fprintf(&b, `<tr><th>%s</th><td>%s <button class="copy-btn" onclick="copyText(this)" data-copy="%s">copy</button></td></tr>`,
			template.HTMLEscapeString(col),
			cell,
			template.HTMLEscapeString(fmt.Sprintf("%v", vals[i])))
	}
	b.WriteString(`</table></div>`)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(b.String()))
}

// hasLimit reports whether a top-level SELECT already has a LIMIT clause.
var limitRe = regexp.MustCompile(`(?i)\blimit\s+\d`)
var selectRe = regexp.MustCompile(`(?i)^\s*(select|with)\b`)

// maybeAutoLimit appends LIMIT N to a query if enabled and not already present.
// Returns the possibly-modified query and a bool indicating whether a limit was added.
func (e *Explorer) maybeAutoLimit(q string) (string, bool) {
	if e.opts.AutoLimit <= 0 {
		return q, false
	}
	if !selectRe.MatchString(q) {
		return q, false
	}
	if limitRe.MatchString(q) {
		return q, false
	}
	trimmed := strings.TrimRight(strings.TrimSpace(q), ";")
	return fmt.Sprintf("%s LIMIT %d", trimmed, e.opts.AutoLimit), true
}

func (e *Explorer) handleQuery(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("query"))
	if q == "" {
		writeError(w, fmt.Errorf("empty query"))
		return
	}
	q2, limited := e.maybeAutoLimit(q)
	start := time.Now()
	rows, err := e.db.Query(q2)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(renderQueryResults(rows, time.Since(start), limited, e.opts.AutoLimit)))
}

func (e *Explorer) handleExplain(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("query"))
	if q == "" {
		writeError(w, fmt.Errorf("empty query"))
		return
	}
	q = strings.TrimRight(q, ";")
	rows, err := e.db.Query(e.dialect.explainPrefix() + q)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var b strings.Builder
	b.WriteString(`<div class="result-meta"><span>EXPLAIN</span></div><pre class="explain">`)
	for rows.Next() {
		rows.Scan(ptrs...)
		for i, v := range vals {
			if i > 0 {
				b.WriteString("  ")
			}
			if v == nil {
				b.WriteString("NULL")
			} else {
				switch val := v.(type) {
				case []byte:
					b.WriteString(template.HTMLEscapeString(string(val)))
				default:
					b.WriteString(template.HTMLEscapeString(fmt.Sprintf("%v", val)))
				}
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(`</pre>`)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(b.String()))
}

func (e *Explorer) handleExportTable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	format := r.URL.Query().Get("format")
	sortCol := r.URL.Query().Get("sort")
	dir := r.URL.Query().Get("dir")
	search := r.URL.Query().Get("search")

	cols := e.dialect.columns(e.db, name)
	qualified := e.dialect.qualifyTable(name)
	colFilters := parseColFilters(r, cols)

	var whereParts []string
	var args []any
	if frag, a := e.dialect.buildSearch(cols, search); frag != "" {
		whereParts = append(whereParts, frag)
		args = append(args, a...)
	}
	if frag, a := e.dialect.buildEq(colFilters, len(args)+1); frag != "" {
		whereParts = append(whereParts, frag)
		args = append(args, a...)
	}
	whereSQL := ""
	if len(whereParts) > 0 {
		whereSQL = " WHERE " + strings.Join(whereParts, " AND ")
	}
	orderSQL := ""
	if sortCol != "" && isValidColumn(sortCol, cols) {
		if dir != "desc" {
			dir = "asc"
		}
		orderSQL = fmt.Sprintf(" ORDER BY %s %s", quoteIdent(sortCol), strings.ToUpper(dir))
	}

	q := "SELECT * FROM " + qualified + whereSQL + orderSQL
	rows, err := e.db.Query(q, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer rows.Close()

	if format == "json" {
		exportJSONStream(w, name, rows)
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
		exportJSONStream(w, "query_result", rows)
	} else {
		exportCSV(w, "query_result", rows)
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func isValidColumn(col string, cols []string) bool {
	for _, c := range cols {
		if c == col {
			return true
		}
	}
	return false
}

// renderTableRowsFull renders the data table with FK links on cells matching
// foreign-key columns, and a per-column filter input in the header row.
func renderTableRowsFull(rows *sql.Rows, table, sortCol, dir, search string,
	pk []string, fks map[string]fkRef, colFilters []colFilter) string {
	cols, err := rows.Columns()
	if err != nil {
		return `<div class="error">` + template.HTMLEscapeString(err.Error()) + `</div>`
	}

	filterMap := map[string]string{}
	for _, f := range colFilters {
		filterMap[f.Col] = f.Val
	}
	pkSet := map[string]bool{}
	for _, p := range pk {
		pkSet[p] = true
	}

	// Build filter-query-string prefix reused in header links.
	filterQS := ""
	for _, f := range colFilters {
		filterQS += "&filter_" + url.QueryEscape(f.Col) + "=" + url.QueryEscape(f.Val)
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
		href := fmt.Sprintf("/api/table/%s?sort=%s&dir=%s&search=%s%s",
			url.PathEscape(table), url.QueryEscape(col), newDir, url.QueryEscape(search), filterQS)
		fmt.Fprintf(&b, `<th class="%s" data-col="%s" hx-get="%s" hx-target="#results">%s%s<div class="resize-handle"></div></th>`,
			cls,
			template.HTMLEscapeString(col),
			template.HTMLEscapeString(href),
			template.HTMLEscapeString(col),
			indicator)
	}
	b.WriteString(`</tr>`)

	// Per-column filter row.
	b.WriteString(`<tr class="col-filter-row">`)
	filterBase := fmt.Sprintf("/api/table/%s?sort=%s&dir=%s&search=%s",
		url.PathEscape(table), url.QueryEscape(sortCol), url.QueryEscape(dir), url.QueryEscape(search))
	for _, col := range cols {
		val := filterMap[col]
		// Build include: every other filter input on the page by name.
		fmt.Fprintf(&b, `<th><input class="col-filter" type="text" name="filter_%s" value="%s" placeholder="=" hx-get="%s" hx-trigger="keyup changed delay:400ms" hx-target="#results" hx-include=".col-filter" autocomplete="off"></th>`,
			template.HTMLEscapeString(col),
			template.HTMLEscapeString(val),
			template.HTMLEscapeString(filterBase))
	}
	b.WriteString(`</tr></thead><tbody>`)

	n := scanRowsWithLinks(&b, rows, cols, table, pk, pkSet, fks)

	b.WriteString(`</tbody></table></div>`)
	if n == 0 {
		return b.String() + `<div class="empty">No rows match</div>`
	}
	return b.String()
}

func renderQueryResults(rows *sql.Rows, elapsed time.Duration, limited bool, limit int) string {
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
	if n == 0 && !limited {
		return `<div class="empty">No rows returned</div>`
	}

	limitNote := ""
	if limited {
		limitNote = fmt.Sprintf(` &middot; <span class="warn">auto-LIMIT %d applied</span>`, limit)
	}
	meta := fmt.Sprintf(`<div class="result-meta"><span>%d rows &middot; %s%s</span><div class="export-btns"><button onclick="exportQuery('csv')" class="export-btn">CSV</button><button onclick="exportQuery('json')" class="export-btn">JSON</button><button onclick="runExplain()" class="export-btn">EXPLAIN</button></div></div>`,
		n, fmtDuration(elapsed), limitNote)
	return meta + b.String()
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
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
				b.WriteString(`<td title="` + template.HTMLEscapeString(s) + `"><span class="cell">` + template.HTMLEscapeString(s) + `</span></td>`)
			}
		}
		b.WriteString(`</tr>`)
		n++
	}
	return n
}

// scanRowsWithLinks is like scanRows but:
//   - renders foreign-key cells as clickable links
//   - builds a row-detail href using PK columns if available
//   - marks the row clickable for the detail drawer
func scanRowsWithLinks(b *strings.Builder, rows *sql.Rows, cols []string,
	table string, pk []string, pkSet map[string]bool, fks map[string]fkRef) int {
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	colIdx := map[string]int{}
	for i, c := range cols {
		colIdx[c] = i
	}
	keyCols := pk
	if len(keyCols) == 0 {
		keyCols = cols
	}
	n := 0
	for rows.Next() {
		rows.Scan(ptrs...)
		// Build row-detail URL
		qs := ""
		for _, kc := range keyCols {
			i, ok := colIdx[kc]
			if !ok {
				continue
			}
			if values[i] == nil {
				qs = ""
				break
			}
			var s string
			switch val := values[i].(type) {
			case []byte:
				s = string(val)
			default:
				s = fmt.Sprintf("%v", val)
			}
			qs += "&pk_" + url.QueryEscape(kc) + "=" + url.QueryEscape(s)
		}
		if qs != "" {
			rowHref := "/api/row/" + url.PathEscape(table) + "?" + qs[1:]
			fmt.Fprintf(b, `<tr class="clickable" hx-get="%s" hx-target="#drawer-body" onclick="openDrawer()">`,
				template.HTMLEscapeString(rowHref))
		} else {
			b.WriteString(`<tr>`)
		}
		for i, v := range values {
			col := cols[i]
			if v == nil {
				b.WriteString(`<td><span class="null">NULL</span></td>`)
				continue
			}
			var s string
			switch val := v.(type) {
			case []byte:
				s = string(val)
			default:
				s = fmt.Sprintf("%v", val)
			}
			cellHTML := `<span class="cell">` + template.HTMLEscapeString(s) + `</span>`
			// FK link
			if ref, ok := fks[col]; ok {
				cellHTML += fmt.Sprintf(` <a class="fk-inline" href="#" hx-get="/api/table/%s?filter_%s=%s" hx-target="#results" onclick="event.stopPropagation()">→</a>`,
					url.PathEscape(ref.Table), url.QueryEscape(ref.Column), url.QueryEscape(s))
			}
			pkCls := ""
			if pkSet[col] {
				pkCls = " pk-col"
			}
			fmt.Fprintf(b, `<td class="cell-td%s" title="%s">%s</td>`, pkCls,
				template.HTMLEscapeString(s), cellHTML)
		}
		b.WriteString(`</tr>`)
		n++
	}
	return n
}

func renderPagination(table string, page, totalPages, count int, sortCol, dir, search string, colFilters []colFilter, elapsed time.Duration) string {
	base := fmt.Sprintf("/api/table/%s?sort=%s&dir=%s&search=%s",
		url.PathEscape(table), url.QueryEscape(sortCol), url.QueryEscape(dir), url.QueryEscape(search))
	for _, f := range colFilters {
		base += "&filter_" + url.QueryEscape(f.Col) + "=" + url.QueryEscape(f.Val)
	}
	var b strings.Builder
	b.WriteString(`<div class="pagination">`)
	if page > 1 {
		fmt.Fprintf(&b, `<button hx-get="%s&page=%d" hx-target="#results">&#8592; Prev</button>`,
			template.HTMLEscapeString(base), page-1)
	} else {
		b.WriteString(`<button disabled>&#8592; Prev</button>`)
	}
	fmt.Fprintf(&b, `<span>Page %d of %d &middot; %d rows &middot; %s</span>`, page, totalPages, count, fmtDuration(elapsed))
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

// exportJSONStream writes a JSON array row-by-row without buffering the whole
// result set in memory. Large Postgres tables should export without OOM.
func exportJSONStream(w http.ResponseWriter, name string, rows *sql.Rows) {
	cols, _ := rows.Columns()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, name))

	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	enc := json.NewEncoder(w)

	w.Write([]byte("[\n"))
	first := true
	for rows.Next() {
		rows.Scan(ptrs...)
		row := make(map[string]any, len(cols))
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
		if !first {
			w.Write([]byte(",\n"))
		}
		first = false
		enc.Encode(row) // writes with trailing newline
	}
	w.Write([]byte("]\n"))
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="error">%s</div>`, template.HTMLEscapeString(err.Error()))
}
