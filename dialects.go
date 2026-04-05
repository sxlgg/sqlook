package sqlook

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// dialect abstracts the differences between database backends so the rest of
// the explorer can stay backend-agnostic.
type dialect interface {
	// listTables returns all user tables/views. Each entry's Name is used as
	// both the display name and the path segment in URLs; it must round-trip
	// through qualifyTable to produce a safe SQL identifier.
	listTables(db *sql.DB) ([]tableEntry, error)

	// columns returns the column names of the given table, in declaration order.
	columns(db *sql.DB, table string) []string

	// primaryKey returns the PK column names for the table, or nil if none.
	primaryKey(db *sql.DB, table string) []string

	// foreignKeys returns a map of column name → FK target.
	foreignKeys(db *sql.DB, table string) map[string]fkRef

	// renderSchema returns an HTML <details> block describing the table's schema.
	renderSchema(db *sql.DB, table string) string

	// qualifyTable turns a table name (possibly schema.table) into a quoted,
	// fully-qualified SQL identifier safe to inline in a query.
	qualifyTable(table string) string

	// buildSearch builds a WHERE-clause fragment (without the leading WHERE)
	// matching `search` against every column via substring match, plus the
	// positional args. Returns "", nil when search is empty.
	buildSearch(cols []string, search string) (string, []any)

	// buildEq builds a WHERE-clause fragment matching equality on each
	// (column, value) pair in filters, joined with AND. Used for per-column
	// filters and row detail lookup. placeholderStart is the 1-based index
	// for the first placeholder to use ($N or ?).
	buildEq(filters []colFilter, placeholderStart int) (string, []any)

	// explainPrefix returns the keyword prefix to turn a query into an
	// EXPLAIN query (e.g. "EXPLAIN " or "EXPLAIN QUERY PLAN ").
	explainPrefix() string
}

type tableEntry struct {
	Name string // display name + URL segment
	Type string // "table" or "view"
}

type fkRef struct {
	Table  string // display name (schema.table on pg, bare on sqlite)
	Column string
}

type colFilter struct {
	Col string
	Val string
}

// ── driver selection ───────────────────────────────────────────────────

// openDB opens a database from a connection string. It auto-detects the
// backend from the string: anything starting with postgres:// or postgresql://
// is treated as a Postgres DSN, everything else as a SQLite file path.
// Returns the opened *sql.DB, the matching dialect, and a short display name.
func openDB(connStr string, opts Options) (*sql.DB, dialect, string, error) {
	trimmed := strings.TrimSpace(connStr)
	if strings.HasPrefix(trimmed, "postgres://") || strings.HasPrefix(trimmed, "postgresql://") {
		return openPostgres(trimmed, opts)
	}
	return openSQLite(trimmed)
}

// ── SQLite ─────────────────────────────────────────────────────────────

func openSQLite(dbPath string) (*sql.DB, dialect, string, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil, "", fmt.Errorf("database not found: %s", dbPath)
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, nil, "", err
	}
	dsn := fmt.Sprintf("file:%s?mode=ro", abs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, "", fmt.Errorf("opening database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, nil, "", fmt.Errorf("connecting to database: %w", err)
	}
	return db, sqliteDialect{}, filepath.Base(dbPath), nil
}

type sqliteDialect struct{}

func (sqliteDialect) listTables(db *sql.DB) ([]tableEntry, error) {
	rows, err := db.Query(`SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tableEntry
	for rows.Next() {
		var e tableEntry
		if err := rows.Scan(&e.Name, &e.Type); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (sqliteDialect) columns(db *sql.DB, table string) []string {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
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

func (sqliteDialect) primaryKey(db *sql.DB, table string) []string {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	type pkCol struct {
		name string
		pos  int
	}
	var pks []pkCol
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk)
		if pk > 0 {
			pks = append(pks, pkCol{name, pk})
		}
	}
	// sort by pk position
	for i := 1; i < len(pks); i++ {
		for j := i; j > 0 && pks[j].pos < pks[j-1].pos; j-- {
			pks[j], pks[j-1] = pks[j-1], pks[j]
		}
	}
	out := make([]string, len(pks))
	for i, p := range pks {
		out[i] = p.name
	}
	return out
}

func (sqliteDialect) foreignKeys(db *sql.DB, table string) map[string]fkRef {
	rows, err := db.Query(fmt.Sprintf("PRAGMA foreign_key_list(%s)", quoteIdent(table)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]fkRef{}
	for rows.Next() {
		// id, seq, table, from, to, on_update, on_delete, match
		var id, seq int
		var refTable, from, to, onUpd, onDel, match string
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpd, &onDel, &match); err != nil {
			continue
		}
		out[from] = fkRef{Table: refTable, Column: to}
	}
	return out
}

func (sqliteDialect) renderSchema(db *sql.DB, table string) string {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return ""
	}
	defer rows.Close()

	fks := sqliteDialect{}.foreignKeys(db, table)

	var b strings.Builder
	b.WriteString(`<details class="schema-section"><summary>Schema</summary><table>`)
	b.WriteString(`<tr><th>Column</th><th>Type</th><th>Nullable</th><th>Default</th><th>PK</th><th>FK</th></tr>`)
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
		fkMark := ""
		if ref, ok := fks[colName]; ok {
			fkMark = fmt.Sprintf(`→ <a href="/api/table/%s" hx-get="/api/table/%s" hx-target="#results">%s.%s</a>`,
				url.PathEscape(ref.Table), url.PathEscape(ref.Table),
				template.HTMLEscapeString(ref.Table), template.HTMLEscapeString(ref.Column))
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			template.HTMLEscapeString(colName),
			template.HTMLEscapeString(colType),
			nullable,
			template.HTMLEscapeString(dfltVal),
			pkMark,
			fkMark)
	}
	b.WriteString(`</table></details>`)
	return b.String()
}

func (sqliteDialect) qualifyTable(table string) string { return quoteIdent(table) }

func (sqliteDialect) buildSearch(cols []string, search string) (string, []any) {
	if search == "" || len(cols) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for _, col := range cols {
		parts = append(parts, fmt.Sprintf("CAST(%s AS TEXT) LIKE ?", quoteIdent(col)))
		args = append(args, "%"+search+"%")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func (sqliteDialect) buildEq(filters []colFilter, _ int) (string, []any) {
	if len(filters) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for _, f := range filters {
		parts = append(parts, fmt.Sprintf("CAST(%s AS TEXT) = ?", quoteIdent(f.Col)))
		args = append(args, f.Val)
	}
	return strings.Join(parts, " AND "), args
}

func (sqliteDialect) explainPrefix() string { return "EXPLAIN QUERY PLAN " }

// ── Postgres ───────────────────────────────────────────────────────────

func openPostgres(dsn string, opts Options) (*sql.DB, dialect, string, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, "", fmt.Errorf("opening database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, nil, "", fmt.Errorf("connecting to database: %w", err)
	}
	// Enforce read-only at the session level. Every transaction inherits this.
	if _, err := db.Exec("SET default_transaction_read_only = on"); err != nil {
		db.Close()
		return nil, nil, "", fmt.Errorf("setting read-only: %w", err)
	}
	// Prevent runaway queries. Value like "30s" or "2min".
	if opts.StatementTimeout != "" {
		if _, err := db.Exec("SET statement_timeout = '" + strings.ReplaceAll(opts.StatementTimeout, "'", "") + "'"); err != nil {
			db.Close()
			return nil, nil, "", fmt.Errorf("setting statement_timeout: %w", err)
		}
	}
	// Display name: host/dbname from the DSN (best-effort).
	name := "postgres"
	if u, err := url.Parse(dsn); err == nil {
		host := u.Hostname()
		path := strings.TrimPrefix(u.Path, "/")
		if host != "" && path != "" {
			name = host + "/" + path
		} else if path != "" {
			name = path
		} else if host != "" {
			name = host
		}
	}
	return db, postgresDialect{}, name, nil
}

type postgresDialect struct{}

func (postgresDialect) listTables(db *sql.DB) ([]tableEntry, error) {
	rows, err := db.Query(`
		SELECT table_schema, table_name, table_type
		FROM information_schema.tables
		WHERE table_schema NOT IN ('pg_catalog','information_schema')
		  AND table_schema NOT LIKE 'pg_toast%'
		  AND table_schema NOT LIKE 'pg_temp_%'
		ORDER BY table_schema, table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tableEntry
	for rows.Next() {
		var schema, name, typ string
		if err := rows.Scan(&schema, &name, &typ); err != nil {
			return nil, err
		}
		display := name
		if schema != "public" {
			display = schema + "." + name
		}
		kind := "table"
		if strings.Contains(strings.ToUpper(typ), "VIEW") {
			kind = "view"
		}
		out = append(out, tableEntry{Name: display, Type: kind})
	}
	return out, nil
}

// splitSchemaTable splits a display name into (schema, table).
func splitSchemaTable(name string) (schema, table string) {
	if i := strings.Index(name, "."); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "public", name
}

func (postgresDialect) buildSearch(cols []string, search string) (string, []any) {
	if search == "" || len(cols) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for i, col := range cols {
		parts = append(parts, fmt.Sprintf("%s::text ILIKE $%d", quoteIdent(col), i+1))
		args = append(args, "%"+search+"%")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func (postgresDialect) buildEq(filters []colFilter, start int) (string, []any) {
	if len(filters) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for i, f := range filters {
		parts = append(parts, fmt.Sprintf("%s::text = $%d", quoteIdent(f.Col), start+i))
		args = append(args, f.Val)
	}
	return strings.Join(parts, " AND "), args
}

func (postgresDialect) qualifyTable(table string) string {
	schema, tbl := splitSchemaTable(table)
	return quoteIdent(schema) + "." + quoteIdent(tbl)
}

func (postgresDialect) columns(db *sql.DB, table string) []string {
	schema, tbl := splitSchemaTable(table)
	rows, err := db.Query(`
		SELECT column_name FROM information_schema.columns
		WHERE table_schema=$1 AND table_name=$2
		ORDER BY ordinal_position`, schema, tbl)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil
		}
		cols = append(cols, c)
	}
	return cols
}

func (postgresDialect) primaryKey(db *sql.DB, table string) []string {
	schema, tbl := splitSchemaTable(table)
	rows, err := db.Query(`
		SELECT a.attname
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = ($1||'.'||$2)::regclass AND i.indisprimary
		ORDER BY array_position(i.indkey, a.attnum)`, schema, tbl)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func (postgresDialect) foreignKeys(db *sql.DB, table string) map[string]fkRef {
	schema, tbl := splitSchemaTable(table)
	rows, err := db.Query(`
		SELECT
			kcu.column_name,
			ccu.table_schema AS ref_schema,
			ccu.table_name AS ref_table,
			ccu.column_name AS ref_column
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name
		 AND tc.table_schema = kcu.table_schema
		JOIN information_schema.constraint_column_usage ccu
		  ON ccu.constraint_name = tc.constraint_name
		 AND ccu.table_schema = tc.table_schema
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema = $1
		  AND tc.table_name = $2`, schema, tbl)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]fkRef{}
	for rows.Next() {
		var col, refSchema, refTable, refCol string
		if err := rows.Scan(&col, &refSchema, &refTable, &refCol); err != nil {
			continue
		}
		display := refTable
		if refSchema != "public" {
			display = refSchema + "." + refTable
		}
		out[col] = fkRef{Table: display, Column: refCol}
	}
	return out
}

func (postgresDialect) renderSchema(db *sql.DB, table string) string {
	schema, tbl := splitSchemaTable(table)

	pkSet := map[string]bool{}
	for _, c := range (postgresDialect{}).primaryKey(db, table) {
		pkSet[c] = true
	}
	fks := postgresDialect{}.foreignKeys(db, table)

	rows, err := db.Query(`
		SELECT column_name, data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema=$1 AND table_name=$2
		ORDER BY ordinal_position`, schema, tbl)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString(`<details class="schema-section"><summary>Schema</summary><table>`)
	b.WriteString(`<tr><th>Column</th><th>Type</th><th>Nullable</th><th>Default</th><th>PK</th><th>FK</th></tr>`)
	for rows.Next() {
		var colName, colType, isNullable string
		var dflt sql.NullString
		rows.Scan(&colName, &colType, &isNullable, &dflt)
		nullable := "yes"
		if strings.EqualFold(isNullable, "NO") {
			nullable = "no"
		}
		dfltVal := ""
		if dflt.Valid {
			dfltVal = dflt.String
		}
		pkMark := ""
		if pkSet[colName] {
			pkMark = "PK"
		}
		fkMark := ""
		if ref, ok := fks[colName]; ok {
			fkMark = fmt.Sprintf(`→ <a href="#" hx-get="/api/table/%s" hx-target="#results">%s.%s</a>`,
				url.PathEscape(ref.Table),
				template.HTMLEscapeString(ref.Table), template.HTMLEscapeString(ref.Column))
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			template.HTMLEscapeString(colName),
			template.HTMLEscapeString(colType),
			nullable,
			template.HTMLEscapeString(dfltVal),
			pkMark,
			fkMark)
	}
	b.WriteString(`</table></details>`)
	return b.String()
}

func (postgresDialect) explainPrefix() string { return "EXPLAIN " }
