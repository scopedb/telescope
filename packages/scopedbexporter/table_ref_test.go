package scopedbexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTableRef(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		database   string
		schema     string
		table      string
		identifier string
	}{
		{
			name:       "table only",
			raw:        "logs",
			table:      "logs",
			identifier: "`logs`",
		},
		{
			name:       "schema table",
			raw:        "otel.logs",
			schema:     "otel",
			table:      "logs",
			identifier: "`otel`.`logs`",
		},
		{
			name:       "database schema table",
			raw:        "scopedb.otel.logs",
			database:   "scopedb",
			schema:     "otel",
			table:      "logs",
			identifier: "`scopedb`.`otel`.`logs`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := parseTableRef(tt.raw)
			require.NoError(t, err)
			assert.Equal(t, tt.database, ref.Database)
			assert.Equal(t, tt.schema, ref.Schema)
			assert.Equal(t, tt.table, ref.Table)
			assert.Equal(t, tt.raw, ref.String())
			assert.Equal(t, tt.identifier, ref.Identifier())
		})
	}
}

func TestParseTableRefRejectsInvalidRoute(t *testing.T) {
	_, err := parseTableRef("too.many.parts.here")
	require.Error(t, err)

	_, err = parseTableRef("bad-table-name")
	require.Error(t, err)
}
