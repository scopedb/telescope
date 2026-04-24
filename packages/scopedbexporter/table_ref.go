package scopedbexporter

import (
	"fmt"
	"strings"

	scopedb "github.com/scopedb/scopedb-sdk/go"
)

type tableRef struct {
	Database string
	Schema   string
	Table    string
}

func parseTableRef(raw string) (tableRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return tableRef{}, fmt.Errorf("table route is required")
	}

	parts := strings.Split(raw, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return tableRef{}, fmt.Errorf("table route must be table, schema.table, or database.schema.table")
	}

	for _, part := range parts {
		if !tablePartPattern.MatchString(part) {
			return tableRef{}, fmt.Errorf("table route must be table, schema.table, or database.schema.table")
		}
	}

	switch len(parts) {
	case 1:
		return tableRef{Table: parts[0]}, nil
	case 2:
		return tableRef{Schema: parts[0], Table: parts[1]}, nil
	default:
		return tableRef{Database: parts[0], Schema: parts[1], Table: parts[2]}, nil
	}
}

func (r tableRef) String() string {
	parts := make([]string, 0, 3)
	if r.Database != "" {
		parts = append(parts, r.Database)
	}
	if r.Schema != "" {
		parts = append(parts, r.Schema)
	}
	parts = append(parts, r.Table)
	return strings.Join(parts, ".")
}

func (r tableRef) Identifier() string {
	return (&scopedb.Table{
		Database: r.Database,
		Schema:   r.Schema,
		Table:    r.Table,
	}).Identifier()
}

func (r tableRef) SchemaIdentifier() string {
	if r.Schema == "" {
		return ""
	}
	if r.Database != "" {
		return quoteTablePart(r.Database) + "." + quoteTablePart(r.Schema)
	}
	return quoteTablePart(r.Schema)
}

func quoteTablePart(part string) string {
	return "`" + part + "`"
}
