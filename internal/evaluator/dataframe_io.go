package evaluator

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/native"
	"geblang/internal/runtime"
)

// dfIdentPattern gates table/column identifiers in generated SQL.
var dfIdentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (e *Evaluator) dataframeReadCsv(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("%s expects a path and an optional options dict", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be a string", call.Callee.String())
	}
	overrides, err := native.DataFrameTypeOverrides(call.Callee.String(), args)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path.Value)
	if err != nil {
		return nil, err
	}
	return native.DataFrameFromCsvText(string(data), overrides)
}

func dataframeWriteCsv(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects a dataframe and a path", call.Callee.String())
	}
	frame, ok := args[0].(*runtime.DataFrame)
	if !ok {
		return nil, fmt.Errorf("%s expects a dataframe first", call.Callee.String())
	}
	path, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be a string", call.Callee.String())
	}
	text, err := native.DataFrameCsvText(frame)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path.Value, []byte(text), 0o644); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) dataframeFromQuery(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects a connection, sql text, and optional parameters", call.Callee.String())
	}
	db, err := e.dbHandle(dfConnHandle(args[0]))
	if err != nil {
		return nil, err
	}
	query, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s query must be a string", call.Callee.String())
	}
	sqlArgs, err := dbArgs(args[2:])
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(query.Value, sqlArgs...)
	if err != nil {
		return nil, err
	}
	records, err := sqlRowsToRuntime(rows)
	if err != nil {
		return nil, err
	}
	list, ok := records.(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s: unexpected row shape", call.Callee.String())
	}
	return native.DataFrameFromRecordsList(list)
}

func (e *Evaluator) dataframeToTable(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects a dataframe, a connection, and a table name", call.Callee.String())
	}
	frame, ok := args[0].(*runtime.DataFrame)
	if !ok {
		return nil, fmt.Errorf("%s expects a dataframe first", call.Callee.String())
	}
	db, err := e.dbHandle(dfConnHandle(args[1]))
	if err != nil {
		return nil, err
	}
	table, ok := args[2].(runtime.String)
	if !ok || !dfIdentPattern.MatchString(table.Value) {
		return nil, fmt.Errorf("%s table name must be a plain identifier", call.Callee.String())
	}
	if len(frame.Cols) == 0 {
		return nil, fmt.Errorf("%s: the dataframe has no columns", call.Callee.String())
	}
	driver, err := e.dbDriverFromValue(dfConnHandle(args[1]))
	if err != nil {
		return nil, err
	}
	colDefs := make([]string, len(frame.Cols))
	colNames := make([]string, len(frame.Cols))
	for i, c := range frame.Cols {
		if !dfIdentPattern.MatchString(c.Name) {
			return nil, fmt.Errorf("%s column %q is not a plain identifier", call.Callee.String(), c.Name)
		}
		sqlType := "TEXT"
		switch c.Dtype {
		case runtime.DFInt64:
			sqlType = "BIGINT"
		case runtime.DFFloat64:
			sqlType = "DOUBLE PRECISION"
		case runtime.DFBool:
			sqlType = "BOOLEAN"
		}
		colNames[i] = c.Name
		colDefs[i] = c.Name + " " + sqlType
	}
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS " + table.Value + " (" + strings.Join(colDefs, ", ") + ")"); err != nil {
		return nil, err
	}
	placeholder := func(n int) string {
		if strings.HasPrefix(driver, "postgres") || driver == "pgx" {
			return fmt.Sprintf("$%d", n)
		}
		return "?"
	}
	const chunkRows = 200
	rows := frame.Rows()
	for start := 0; start < rows; start += chunkRows {
		end := start + chunkRows
		if end > rows {
			end = rows
		}
		var sb strings.Builder
		sb.WriteString("INSERT INTO " + table.Value + " (" + strings.Join(colNames, ", ") + ") VALUES ")
		params := make([]any, 0, (end-start)*len(frame.Cols))
		n := 1
		for r := start; r < end; r++ {
			if r > start {
				sb.WriteByte(',')
			}
			sb.WriteByte('(')
			for ci, c := range frame.Cols {
				if ci > 0 {
					sb.WriteByte(',')
				}
				sb.WriteString(placeholder(n))
				n++
				arg, err := dbArg(dfColumnCell(c, r))
				if err != nil {
					return nil, err
				}
				params = append(params, arg)
			}
			sb.WriteByte(')')
		}
		if _, err := db.Exec(sb.String(), params...); err != nil {
			return nil, err
		}
	}
	return runtime.SmallInt{Value: int64(rows)}, nil
}

func dfColumnCell(c *runtime.DFColumn, row int) runtime.Value {
	if c.IsNull(row) {
		return runtime.Null{}
	}
	switch c.Dtype {
	case runtime.DFFloat64:
		return runtime.Float{Value: c.F64[row]}
	case runtime.DFInt64:
		return runtime.SmallInt{Value: c.I64[row]}
	case runtime.DFString:
		return runtime.String{Value: c.Str[row]}
	default:
		return runtime.Bool{Value: c.Bool[row]}
	}
}

// dfConnHandle unwraps a db.Connection instance to its raw handle so
// fromQuery/toTable accept the object form db.connect returns.
func dfConnHandle(v runtime.Value) runtime.Value {
	if inst, ok := v.(*runtime.Instance); ok {
		if h, ok := inst.Fields["handle"]; ok {
			return h
		}
	}
	return v
}
