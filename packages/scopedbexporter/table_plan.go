/*
 * Copyright 2026 ScopeDB, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package scopedbexporter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	scopedb "github.com/scopedb/goscopedb"
)

const TablePlanVersion = 1

const (
	defaultCatalogDatabase = "scopedb"
	defaultCatalogSchema   = "public"
)

type TablePlanAction string

const (
	TableActionNoop    TablePlanAction = "no-op"
	TableActionCreate  TablePlanAction = "create"
	TableActionAlter   TablePlanAction = "alter"
	TableActionBlocked TablePlanAction = "blocked"
)

type TableColumnStatus string

const (
	TableColumnExists   TableColumnStatus = "exists"
	TableColumnCreate   TableColumnStatus = "create"
	TableColumnAdd      TableColumnStatus = "add"
	TableColumnBlocked  TableColumnStatus = "blocked"
	TableColumnConflict TableColumnStatus = "conflict"
)

// IngestionTablePlan is a generated comparison between Telescope's logical
// append contract and the live ScopeDB catalog. It is output, not configuration.
type IngestionTablePlan struct {
	Version int         `json:"version"`
	Tables  []TablePlan `json:"tables"`
}

type TablePlan struct {
	Table          string            `json:"table"`
	Database       string            `json:"database"`
	Schema         string            `json:"schema"`
	Signals        []string          `json:"signals"`
	Exists         bool              `json:"exists"`
	CreateDatabase bool              `json:"create_database,omitempty"`
	CreateSchema   bool              `json:"create_schema,omitempty"`
	Action         TablePlanAction   `json:"action"`
	Columns        []TableColumnPlan `json:"columns"`
}

type TableColumnPlan struct {
	Name          string                   `json:"name"`
	RequiredType  string                   `json:"required_type,omitempty"`
	ActualType    string                   `json:"actual_type,omitempty"`
	Status        TableColumnStatus        `json:"status"`
	ObservedTypes []string                 `json:"observed_types,omitempty"`
	Reason        string                   `json:"reason,omitempty"`
	Requirements  []TableColumnRequirement `json:"requirements,omitempty"`
}

// TableColumnRequirement identifies the per-signal mapping and optional sample
// evidence behind one destination column. SuggestedCast is evidence-based but
// is never applied automatically.
type TableColumnRequirement struct {
	Signal        string                    `json:"signal"`
	Mapping       string                    `json:"mapping"`
	OutputType    string                    `json:"output_type"`
	Sampled       bool                      `json:"sampled,omitempty"`
	Present       int                       `json:"present,omitempty"`
	Total         int                       `json:"total,omitempty"`
	Errors        int                       `json:"errors,omitempty"`
	ObservedTypes []string                  `json:"observed_types,omitempty"`
	Selections    []MappingSelectionPreview `json:"selections,omitempty"`
	SuggestedCast string                    `json:"suggested_cast,omitempty"`
}

func (p IngestionTablePlan) Blocked() bool {
	for _, table := range p.Tables {
		if table.Action == TableActionBlocked {
			return true
		}
	}
	return false
}

// PlanIngestionTables compares configured mappings with the live catalog. A
// missing table is a valid create plan. Other catalog failures are returned.
func PlanIngestionTables(
	ctx context.Context,
	endpoint string,
	apiKey string,
	ingestion IngestionConfig,
	previews []MappingPreview,
) (IngestionTablePlan, error) {
	descriptions, err := DescribeIngestionMappings(ingestion)
	if err != nil {
		return IngestionTablePlan{}, err
	}
	previewIndex, err := indexTablePlanPreviews(descriptions, previews)
	if err != nil {
		return IngestionTablePlan{}, err
	}

	client, err := newIngestionClient(endpoint, apiKey, ingestion)
	if err != nil {
		return IngestionTablePlan{}, err
	}
	defer client.Close()

	builders, err := groupTableRequirements(descriptions, previewIndex)
	if err != nil {
		return IngestionTablePlan{}, err
	}
	result := IngestionTablePlan{
		Version: TablePlanVersion,
		Tables:  make([]TablePlan, 0, len(builders)),
	}
	for _, builder := range builders {
		resource, describeErr := client.table(builder.ref).Describe(ctx)
		exists := describeErr == nil
		if describeErr != nil && !isCatalogNotFound(describeErr) {
			return IngestionTablePlan{}, fmt.Errorf("describe target table %s: %w", builder.ref.String(), describeErr)
		}
		database, schema := resolvedTableNamespace(builder.ref)
		createDatabase := false
		createSchema := false
		if !exists {
			createDatabase, createSchema, err = missingNamespace(ctx, client, database, schema)
			if err != nil {
				return IngestionTablePlan{}, err
			}
		}
		tablePlan := buildTablePlan(builder, exists, resource)
		tablePlan.Database = database
		tablePlan.Schema = schema
		tablePlan.CreateDatabase = createDatabase
		tablePlan.CreateSchema = createSchema
		result.Tables = append(result.Tables, tablePlan)
	}
	return result, nil
}

func missingNamespace(ctx context.Context, client *Client, database string, schema string) (bool, bool, error) {
	_, err := client.sdk.FetchSchema(ctx, database, schema)
	if err == nil {
		return false, false, nil
	}
	if !isCatalogNotFound(err) {
		return false, false, fmt.Errorf("describe target schema %s.%s: %w", database, schema, err)
	}

	_, err = client.sdk.FetchDatabase(ctx, database)
	if err == nil {
		return false, true, nil
	}
	if isCatalogNotFound(err) {
		return true, true, nil
	}
	return false, false, fmt.Errorf("describe target database %s: %w", database, err)
}

// RenderTablePlanScopeQL renders only additive, reviewable DDL. It never
// executes statements and refuses to produce a partial script for a blocked
// plan.
func RenderTablePlanScopeQL(plan IngestionTablePlan) (string, error) {
	if plan.Version != TablePlanVersion {
		return "", fmt.Errorf("unsupported table plan version %d", plan.Version)
	}
	var blocked []string
	for _, table := range plan.Tables {
		if table.Action == TableActionBlocked {
			blocked = append(blocked, table.Table)
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return "", fmt.Errorf("table plan is blocked: %s", strings.Join(blocked, ", "))
	}

	databaseSet := make(map[string]struct{})
	type schemaName struct {
		database string
		schema   string
	}
	schemaSet := make(map[schemaName]struct{})
	for _, table := range plan.Tables {
		database, schema, err := tablePlanNamespace(table)
		if err != nil {
			return "", err
		}
		if table.CreateDatabase {
			databaseSet[database] = struct{}{}
		}
		if table.CreateSchema {
			schemaSet[schemaName{database: database, schema: schema}] = struct{}{}
		}
	}
	databases := sortedStringKeys(databaseSet)
	schemas := make([]schemaName, 0, len(schemaSet))
	for schema := range schemaSet {
		schemas = append(schemas, schema)
	}
	sort.Slice(schemas, func(left int, right int) bool {
		if schemas[left].database == schemas[right].database {
			return schemas[left].schema < schemas[right].schema
		}
		return schemas[left].database < schemas[right].database
	})

	var statements []string
	for _, database := range databases {
		statements = append(statements, fmt.Sprintf("CREATE DATABASE %s;", scopeQLIdentifier(database)))
	}
	for _, schema := range schemas {
		statements = append(statements, fmt.Sprintf(
			"CREATE SCHEMA %s.%s;",
			scopeQLIdentifier(schema.database),
			scopeQLIdentifier(schema.schema),
		))
	}

	tables := append([]TablePlan(nil), plan.Tables...)
	sort.Slice(tables, func(left int, right int) bool {
		return tables[left].Table < tables[right].Table
	})
	for _, table := range tables {
		identifier, err := scopeQLTableIdentifier(table.Table)
		if err != nil {
			return "", err
		}
		switch table.Action {
		case TableActionNoop:
			continue
		case TableActionCreate:
			columns := make([]string, 0, len(table.Columns))
			for _, column := range table.Columns {
				if column.Status != TableColumnCreate {
					return "", fmt.Errorf("create plan for %s has column %s in state %s", table.Table, column.Name, column.Status)
				}
				definition, err := scopeQLColumnDefinition(column)
				if err != nil {
					return "", fmt.Errorf("table %s: %w", table.Table, err)
				}
				columns = append(columns, "    "+definition)
			}
			statements = append(statements, fmt.Sprintf("CREATE TABLE %s (\n%s\n);", identifier, strings.Join(columns, ",\n")))
		case TableActionAlter:
			for _, column := range table.Columns {
				if column.Status != TableColumnAdd {
					continue
				}
				definition, err := scopeQLColumnDefinition(column)
				if err != nil {
					return "", fmt.Errorf("table %s: %w", table.Table, err)
				}
				statements = append(statements, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", identifier, definition))
			}
		default:
			return "", fmt.Errorf("table %s has unsupported plan action %q", table.Table, table.Action)
		}
	}
	if len(statements) == 0 {
		return "", nil
	}
	return strings.Join(statements, "\n") + "\n", nil
}

func resolvedTableNamespace(ref tableRef) (string, string) {
	database := ref.Database
	if database == "" {
		database = defaultCatalogDatabase
	}
	schema := ref.Schema
	if schema == "" {
		schema = defaultCatalogSchema
	}
	return database, schema
}

func tablePlanNamespace(plan TablePlan) (string, string, error) {
	ref, err := parseTableRef(plan.Table)
	if err != nil {
		return "", "", err
	}
	database, schema := resolvedTableNamespace(ref)
	if plan.Database != "" {
		database = plan.Database
	}
	if plan.Schema != "" {
		schema = plan.Schema
	}
	if !tablePartPattern.MatchString(database) {
		return "", "", fmt.Errorf("database %q is not an unquoted ScopeDB identifier", database)
	}
	if !tablePartPattern.MatchString(schema) {
		return "", "", fmt.Errorf("schema %q is not an unquoted ScopeDB identifier", schema)
	}
	return database, schema, nil
}

type tableColumnRequirement struct {
	signal      string
	description MappingColumnDescription
	preview     *MappingColumnPreview
}

type tablePlanBuilder struct {
	ref          tableRef
	signals      map[string]struct{}
	requirements map[string][]tableColumnRequirement
}

func indexTablePlanPreviews(
	descriptions []SignalMappingDescription,
	previews []MappingPreview,
) (map[string]map[string]MappingColumnPreview, error) {
	enabled := make(map[string]bool, len(descriptions))
	for _, description := range descriptions {
		enabled[description.Signal] = true
	}
	indexed := make(map[string]map[string]MappingColumnPreview, len(previews))
	for _, preview := range previews {
		if !enabled[preview.Signal] {
			return nil, fmt.Errorf("mapping preview provided for disabled signal %q", preview.Signal)
		}
		if _, exists := indexed[preview.Signal]; exists {
			return nil, fmt.Errorf("mapping preview for %s was provided more than once", preview.Signal)
		}
		columns := make(map[string]MappingColumnPreview, len(preview.Columns))
		for _, column := range preview.Columns {
			columns[column.Column] = column
		}
		indexed[preview.Signal] = columns
	}
	return indexed, nil
}

func groupTableRequirements(
	descriptions []SignalMappingDescription,
	previews map[string]map[string]MappingColumnPreview,
) ([]tablePlanBuilder, error) {
	byTable := make(map[string]*tablePlanBuilder)
	for _, description := range descriptions {
		builder := byTable[description.Table]
		if builder == nil {
			ref, err := parseTableRef(description.Table)
			if err != nil {
				return nil, err
			}
			builder = &tablePlanBuilder{
				ref:          ref,
				signals:      make(map[string]struct{}),
				requirements: make(map[string][]tableColumnRequirement),
			}
			byTable[description.Table] = builder
		}
		builder.signals[description.Signal] = struct{}{}
		for _, column := range description.Columns {
			var preview *MappingColumnPreview
			if previewColumns := previews[description.Signal]; previewColumns != nil {
				if value, ok := previewColumns[column.Column]; ok {
					copy := value
					preview = &copy
				}
			}
			builder.requirements[column.Column] = append(builder.requirements[column.Column], tableColumnRequirement{
				signal:      description.Signal,
				description: column,
				preview:     preview,
			})
		}
	}

	tableNames := sortedStringKeys(byTable)
	builders := make([]tablePlanBuilder, 0, len(tableNames))
	for _, table := range tableNames {
		builders = append(builders, *byTable[table])
	}
	return builders, nil
}

func buildTablePlan(builder tablePlanBuilder, exists bool, resource scopedb.TableResource) TablePlan {
	signals := sortedStringKeys(builder.signals)

	actual := make(map[string]scopedb.DataType, len(resource.Columns))
	if exists {
		for _, column := range resource.Columns {
			actual[column.Name] = column.DataType
		}
	}
	columnNames := sortedStringKeys(builder.requirements)

	plan := TablePlan{
		Table:   builder.ref.String(),
		Signals: signals,
		Exists:  exists,
		Columns: make([]TableColumnPlan, 0, len(columnNames)),
	}
	blocked := false
	additive := false
	for _, name := range columnNames {
		column := planTableColumn(name, builder.requirements[name], exists, actual)
		plan.Columns = append(plan.Columns, column)
		switch column.Status {
		case TableColumnBlocked, TableColumnConflict:
			blocked = true
		case TableColumnAdd:
			additive = true
		}
	}
	switch {
	case blocked:
		plan.Action = TableActionBlocked
	case !exists:
		plan.Action = TableActionCreate
	case additive:
		plan.Action = TableActionAlter
	default:
		plan.Action = TableActionNoop
	}
	return plan
}

func planTableColumn(
	name string,
	requirements []tableColumnRequirement,
	exists bool,
	actual map[string]scopedb.DataType,
) TableColumnPlan {
	sort.Slice(requirements, func(left int, right int) bool {
		return requirements[left].signal < requirements[right].signal
	})
	column := TableColumnPlan{Name: name}
	details := make([]TableColumnRequirement, 0, len(requirements))
	includeDetails := len(requirements) > 1
	types := make(map[string]struct{}, len(requirements))
	var unresolved bool
	var sampleErrors []string
	observed := make(map[string]struct{})
	for _, requirement := range requirements {
		detail := TableColumnRequirement{
			Signal:     requirement.signal,
			Mapping:    requirement.description.Source,
			OutputType: requirement.description.OutputType,
		}
		_, resolvedType := scopeDBTableType(requirement.description.OutputType)
		if resolvedType {
			types[requirement.description.OutputType] = struct{}{}
		} else {
			unresolved = true
			includeDetails = true
		}
		if requirement.preview == nil {
			details = append(details, detail)
			continue
		}
		includeDetails = true
		detail.Sampled = true
		detail.Present = requirement.preview.Present
		detail.Total = requirement.preview.Total
		detail.Errors = requirement.preview.Errors
		detail.ObservedTypes = append([]string(nil), requirement.preview.ObservedTypes...)
		detail.Selections = append([]MappingSelectionPreview(nil), requirement.preview.Selections...)
		if !resolvedType && requirement.preview.Errors == 0 && len(detail.ObservedTypes) == 1 && supportedMappingCast(detail.ObservedTypes[0]) {
			detail.SuggestedCast = detail.ObservedTypes[0]
		}
		for _, valueType := range requirement.preview.ObservedTypes {
			observed[valueType] = struct{}{}
		}
		if requirement.preview.Errors > 0 {
			sampleErrors = append(sampleErrors, fmt.Sprintf("%s=%d", requirement.signal, requirement.preview.Errors))
		}
		details = append(details, detail)
	}
	column.ObservedTypes = sortedStringKeys(observed)
	finish := func() TableColumnPlan {
		if includeDetails {
			column.Requirements = details
		}
		return column
	}

	if len(types) > 1 {
		includeDetails = true
		parts := make([]string, 0, len(requirements))
		for _, requirement := range requirements {
			parts = append(parts, requirement.signal+"="+requirement.description.OutputType)
		}
		column.Status = TableColumnConflict
		column.Reason = "signals require different output types: " + strings.Join(parts, ", ")
		return finish()
	}
	if unresolved {
		column.Status = TableColumnBlocked
		column.Reason = "output type is runtime-dependent; add an explicit cast to the mapping"
		return finish()
	}
	for valueType := range types {
		column.RequiredType = valueType
	}
	if len(sampleErrors) > 0 {
		column.Status = TableColumnBlocked
		column.Reason = "sample mapping failed: " + strings.Join(sampleErrors, ", ")
		return finish()
	}
	if !exists {
		column.Status = TableColumnCreate
		return finish()
	}
	actualType, found := actual[name]
	if !found {
		column.Status = TableColumnAdd
		return finish()
	}
	column.ActualType = string(actualType)
	if selectorType(column.RequiredType).compatibilityWith(actualType) == MappingIncompatible {
		includeDetails = true
		column.Status = TableColumnConflict
		column.Reason = fmt.Sprintf("mapping requires %s but the table has %s", column.RequiredType, actualType)
		return finish()
	}
	column.Status = TableColumnExists
	return finish()
}

func scopeDBTableType(value string) (scopedb.DataType, bool) {
	dataType := scopedb.DataType(value)
	switch dataType {
	case scopedb.StringDataType,
		scopedb.BinaryDataType,
		scopedb.IntDataType,
		scopedb.UIntDataType,
		scopedb.FloatDataType,
		scopedb.BooleanDataType,
		scopedb.TimestampDataType,
		scopedb.IntervalDataType,
		scopedb.ArrayDataType,
		scopedb.ObjectDataType,
		scopedb.AnyDataType:
		return dataType, true
	default:
		return "", false
	}
}

func isCatalogNotFound(err error) bool {
	var scopeErr *scopedb.Error
	return errors.As(err, &scopeErr) && scopeErr.HTTPStatus == http.StatusNotFound
}

func scopeQLTableIdentifier(raw string) (string, error) {
	ref, err := parseTableRef(raw)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, 3)
	if ref.Database != "" {
		parts = append(parts, scopeQLIdentifier(ref.Database))
	}
	if ref.Schema != "" {
		parts = append(parts, scopeQLIdentifier(ref.Schema))
	}
	parts = append(parts, scopeQLIdentifier(ref.Table))
	return strings.Join(parts, "."), nil
}

func scopeQLColumnDefinition(column TableColumnPlan) (string, error) {
	if !tablePartPattern.MatchString(column.Name) {
		return "", fmt.Errorf("column %q is not an unquoted ScopeDB identifier", column.Name)
	}
	if _, ok := scopeDBTableType(column.RequiredType); !ok {
		return "", fmt.Errorf("column %s has unsupported required type %q", column.Name, column.RequiredType)
	}
	return scopeQLIdentifier(column.Name) + " " + column.RequiredType, nil
}

func scopeQLIdentifier(value string) string {
	return "`" + value + "`"
}

func sortedStringKeys[V any](values map[string]V) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
