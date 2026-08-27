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
	"go.opentelemetry.io/collector/config/configopaque"
	"go.uber.org/zap"
)

const TablePlanVersion = 1

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
	Table   string            `json:"table"`
	Signals []string          `json:"signals"`
	Exists  bool              `json:"exists"`
	Action  TablePlanAction   `json:"action"`
	Columns []TableColumnPlan `json:"columns"`
}

type TableColumnPlan struct {
	Name          string            `json:"name"`
	RequiredType  string            `json:"required_type,omitempty"`
	ActualType    string            `json:"actual_type,omitempty"`
	Status        TableColumnStatus `json:"status"`
	ObservedTypes []string          `json:"observed_types,omitempty"`
	Reason        string            `json:"reason,omitempty"`
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

	tables, mappings := ingestion.exporterConfig()
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = endpoint
	cfg.APIKey = configopaque.String(apiKey)
	cfg.Tables = tables
	cfg.Mappings = mappings
	if err := cfg.Validate(); err != nil {
		return IngestionTablePlan{}, err
	}
	client, err := newClient(cfg, zap.NewNop())
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
		result.Tables = append(result.Tables, buildTablePlan(builder, exists, resource))
	}
	return result, nil
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

	tables := append([]TablePlan(nil), plan.Tables...)
	sort.Slice(tables, func(left int, right int) bool {
		return tables[left].Table < tables[right].Table
	})
	var statements []string
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

	tableNames := make([]string, 0, len(byTable))
	for table := range byTable {
		tableNames = append(tableNames, table)
	}
	sort.Strings(tableNames)
	builders := make([]tablePlanBuilder, 0, len(tableNames))
	for _, table := range tableNames {
		builders = append(builders, *byTable[table])
	}
	return builders, nil
}

func buildTablePlan(builder tablePlanBuilder, exists bool, resource scopedb.TableResource) TablePlan {
	signals := make([]string, 0, len(builder.signals))
	for signal := range builder.signals {
		signals = append(signals, signal)
	}
	sort.Strings(signals)

	actual := make(map[string]scopedb.DataType, len(resource.Columns))
	if exists {
		for _, column := range resource.Columns {
			actual[column.Name] = column.DataType
		}
	}
	columnNames := make([]string, 0, len(builder.requirements))
	for name := range builder.requirements {
		columnNames = append(columnNames, name)
	}
	sort.Strings(columnNames)

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
	types := make(map[string]struct{}, len(requirements))
	var unresolved bool
	var sampleErrors []string
	observed := make(map[string]struct{})
	for _, requirement := range requirements {
		if _, ok := scopeDBTableType(requirement.description.OutputType); ok {
			types[requirement.description.OutputType] = struct{}{}
		} else {
			unresolved = true
		}
		if requirement.preview == nil {
			continue
		}
		for _, valueType := range requirement.preview.ObservedTypes {
			observed[valueType] = struct{}{}
		}
		if requirement.preview.Errors > 0 {
			sampleErrors = append(sampleErrors, fmt.Sprintf("%s=%d", requirement.signal, requirement.preview.Errors))
		}
	}
	for valueType := range observed {
		column.ObservedTypes = append(column.ObservedTypes, valueType)
	}
	sort.Strings(column.ObservedTypes)

	if len(types) > 1 {
		parts := make([]string, 0, len(requirements))
		for _, requirement := range requirements {
			parts = append(parts, requirement.signal+"="+requirement.description.OutputType)
		}
		column.Status = TableColumnConflict
		column.Reason = "signals require different output types: " + strings.Join(parts, ", ")
		return column
	}
	if unresolved {
		column.Status = TableColumnBlocked
		column.Reason = "output type is runtime-dependent; add an explicit cast to the mapping"
		return column
	}
	for valueType := range types {
		column.RequiredType = valueType
	}
	if len(sampleErrors) > 0 {
		column.Status = TableColumnBlocked
		column.Reason = "sample mapping failed: " + strings.Join(sampleErrors, ", ")
		return column
	}
	if !exists {
		column.Status = TableColumnCreate
		return column
	}
	actualType, found := actual[name]
	if !found {
		column.Status = TableColumnAdd
		return column
	}
	column.ActualType = string(actualType)
	if selectorType(column.RequiredType).compatibilityWith(actualType) == MappingIncompatible {
		column.Status = TableColumnConflict
		column.Reason = fmt.Sprintf("mapping requires %s but the table has %s", column.RequiredType, actualType)
		return column
	}
	column.Status = TableColumnExists
	return column
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
