package native

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"geblang/internal/runtime"
)

// dfCellValue converts one cell to a Geblang value (Null when masked).
func dfCellValue(c *runtime.DFColumn, i int) runtime.Value {
	if c.IsNull(i) {
		return runtime.Null{}
	}
	switch c.Dtype {
	case runtime.DFFloat64:
		return runtime.Float{Value: c.F64[i]}
	case runtime.DFInt64:
		return runtime.SmallInt{Value: c.I64[i]}
	case runtime.DFString:
		return runtime.String{Value: c.Str[i]}
	default:
		return runtime.Bool{Value: c.Bool[i]}
	}
}

// dfCellF64 reads a numeric cell as float64 (mask checked by callers).
func dfCellF64(c *runtime.DFColumn, i int) float64 {
	if c.Dtype == runtime.DFInt64 {
		return float64(c.I64[i])
	}
	return c.F64[i]
}

func dfNumeric(c *runtime.DFColumn) bool {
	return c.Dtype == runtime.DFFloat64 || c.Dtype == runtime.DFInt64
}

// dfBuildColumn infers a dtype from values: all-int -> int64, numeric
// mix -> float64, all-bool -> bool, otherwise string; nulls mask.
func dfBuildColumn(name string, values []runtime.Value) (*runtime.DFColumn, error) {
	n := len(values)
	nulls := make([]bool, n)
	hasNull := false
	allInt, allNum, allBool := true, true, true
	for i, v := range values {
		if _, isNull := v.(runtime.Null); isNull {
			nulls[i] = true
			hasNull = true
			continue
		}
		if _, ok := v.(runtime.Bool); ok {
			allInt, allNum = false, false
			continue
		}
		allBool = false
		if _, ok := AsInt64(v); ok {
			continue
		}
		allInt = false
		if _, ok := ndFloatTyped(v); ok {
			continue
		}
		allNum = false
	}
	col := &runtime.DFColumn{Name: name}
	if hasNull {
		col.Null = nulls
	}
	switch {
	case allInt:
		col.Dtype = runtime.DFInt64
		col.I64 = make([]int64, n)
		for i, v := range values {
			if nulls[i] {
				continue
			}
			col.I64[i], _ = AsInt64(v)
		}
	case allNum:
		col.Dtype = runtime.DFFloat64
		col.F64 = make([]float64, n)
		for i, v := range values {
			if nulls[i] {
				continue
			}
			col.F64[i], _ = asFloat64Strict(v)
		}
	case allBool:
		col.Dtype = runtime.DFBool
		col.Bool = make([]bool, n)
		for i, v := range values {
			if nulls[i] {
				continue
			}
			col.Bool[i] = v.(runtime.Bool).Value
		}
	default:
		col.Dtype = runtime.DFString
		col.Str = make([]string, n)
		for i, v := range values {
			if nulls[i] {
				continue
			}
			if s, ok := v.(runtime.String); ok {
				col.Str[i] = s.Value
			} else {
				col.Str[i] = v.Inspect()
			}
		}
	}
	return col, nil
}

// dfEvalExpr evaluates an expression tree columnwise against a frame.
// Comparison/logic yield a bool column; arithmetic yields a numeric
// column; nulls propagate (null compares false, null arith is null).
func dfEvalExpr(frame *runtime.DataFrame, e *runtime.DFExpr) (*runtime.DFColumn, error) {
	rows := frame.Rows()
	switch e.Kind {
	case "col":
		c, ok := frame.Column(e.Col)
		if !ok {
			return nil, fmt.Errorf("unknown column %q", e.Col)
		}
		return c, nil
	case "lit":
		vals := make([]runtime.Value, rows)
		for i := range vals {
			vals[i] = e.Lit
		}
		return dfBuildColumn("", vals)
	case "isNull":
		inner, err := dfEvalExpr(frame, e.Left)
		if err != nil {
			return nil, err
		}
		out := &runtime.DFColumn{Dtype: runtime.DFBool, Bool: make([]bool, rows)}
		for i := 0; i < rows; i++ {
			out.Bool[i] = inner.IsNull(i)
		}
		return out, nil
	case "not":
		inner, err := dfEvalExpr(frame, e.Left)
		if err != nil {
			return nil, err
		}
		if inner.Dtype != runtime.DFBool {
			return nil, fmt.Errorf("not() needs a bool operand, got %s", inner.Dtype)
		}
		out := &runtime.DFColumn{Dtype: runtime.DFBool, Bool: make([]bool, rows)}
		for i := 0; i < rows; i++ {
			out.Bool[i] = !inner.Bool[i] && !inner.IsNull(i)
		}
		return out, nil
	case "bin":
		left, err := dfEvalExpr(frame, e.Left)
		if err != nil {
			return nil, err
		}
		right, err := dfEvalExpr(frame, e.Right)
		if err != nil {
			return nil, err
		}
		return dfEvalBinary(e.Op, left, right, rows)
	default:
		return nil, fmt.Errorf("unknown expression kind %q", e.Kind)
	}
}

func dfEvalBinary(op string, l, r *runtime.DFColumn, rows int) (*runtime.DFColumn, error) {
	switch op {
	case "and", "or":
		if l.Dtype != runtime.DFBool || r.Dtype != runtime.DFBool {
			return nil, fmt.Errorf("%s needs bool operands", op)
		}
		out := &runtime.DFColumn{Dtype: runtime.DFBool, Bool: make([]bool, rows)}
		for i := 0; i < rows; i++ {
			if l.IsNull(i) || r.IsNull(i) {
				continue
			}
			if op == "and" {
				out.Bool[i] = l.Bool[i] && r.Bool[i]
			} else {
				out.Bool[i] = l.Bool[i] || r.Bool[i]
			}
		}
		return out, nil
	case "gt", "lt", "gte", "lte", "eq", "ne":
		out := &runtime.DFColumn{Dtype: runtime.DFBool, Bool: make([]bool, rows)}
		for i := 0; i < rows; i++ {
			if l.IsNull(i) || r.IsNull(i) {
				continue
			}
			cmp, err := dfCompareCells(l, r, i)
			if err != nil {
				return nil, err
			}
			switch op {
			case "gt":
				out.Bool[i] = cmp > 0
			case "lt":
				out.Bool[i] = cmp < 0
			case "gte":
				out.Bool[i] = cmp >= 0
			case "lte":
				out.Bool[i] = cmp <= 0
			case "eq":
				out.Bool[i] = cmp == 0
			default:
				out.Bool[i] = cmp != 0
			}
		}
		return out, nil
	case "add", "sub", "mul", "div":
		if op == "add" && l.Dtype == runtime.DFString && r.Dtype == runtime.DFString {
			out := &runtime.DFColumn{Dtype: runtime.DFString, Str: make([]string, rows), Null: dfMergeNulls(l, r, rows)}
			for i := 0; i < rows; i++ {
				if l.IsNull(i) || r.IsNull(i) {
					continue
				}
				out.Str[i] = l.Str[i] + r.Str[i]
			}
			return out, nil
		}
		if !dfNumeric(l) || !dfNumeric(r) {
			return nil, fmt.Errorf("%s needs numeric operands, got %s and %s", op, l.Dtype, r.Dtype)
		}
		if l.Dtype == runtime.DFInt64 && r.Dtype == runtime.DFInt64 && op != "div" {
			out := &runtime.DFColumn{Dtype: runtime.DFInt64, I64: make([]int64, rows), Null: dfMergeNulls(l, r, rows)}
			for i := 0; i < rows; i++ {
				if l.IsNull(i) || r.IsNull(i) {
					continue
				}
				switch op {
				case "add":
					out.I64[i] = l.I64[i] + r.I64[i]
				case "sub":
					out.I64[i] = l.I64[i] - r.I64[i]
				default:
					out.I64[i] = l.I64[i] * r.I64[i]
				}
			}
			return out, nil
		}
		out := &runtime.DFColumn{Dtype: runtime.DFFloat64, F64: make([]float64, rows), Null: dfMergeNulls(l, r, rows)}
		for i := 0; i < rows; i++ {
			if l.IsNull(i) || r.IsNull(i) {
				continue
			}
			x, y := dfCellF64(l, i), dfCellF64(r, i)
			switch op {
			case "add":
				out.F64[i] = x + y
			case "sub":
				out.F64[i] = x - y
			case "mul":
				out.F64[i] = x * y
			default:
				out.F64[i] = x / y
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown operator %q", op)
	}
}

func dfMergeNulls(l, r *runtime.DFColumn, rows int) []bool {
	if l.Null == nil && r.Null == nil {
		return nil
	}
	out := make([]bool, rows)
	for i := 0; i < rows; i++ {
		out[i] = l.IsNull(i) || r.IsNull(i)
	}
	return out
}

func dfCompareCells(l, r *runtime.DFColumn, i int) (int, error) {
	if dfNumeric(l) && dfNumeric(r) {
		x, y := dfCellF64(l, i), dfCellF64(r, i)
		switch {
		case x < y:
			return -1, nil
		case x > y:
			return 1, nil
		default:
			return 0, nil
		}
	}
	if l.Dtype == runtime.DFString && r.Dtype == runtime.DFString {
		return strings.Compare(l.Str[i], r.Str[i]), nil
	}
	if l.Dtype == runtime.DFBool && r.Dtype == runtime.DFBool {
		x, y := 0, 0
		if l.Bool[i] {
			x = 1
		}
		if r.Bool[i] {
			y = 1
		}
		return x - y, nil
	}
	return 0, fmt.Errorf("cannot compare %s with %s", l.Dtype, r.Dtype)
}

// dfTake builds a new frame from row indices (shared nothing).
func dfTake(frame *runtime.DataFrame, idx []int) *runtime.DataFrame {
	cols := make([]*runtime.DFColumn, len(frame.Cols))
	for ci, c := range frame.Cols {
		nc := &runtime.DFColumn{Name: c.Name, Dtype: c.Dtype}
		if c.Null != nil {
			nc.Null = make([]bool, len(idx))
		}
		switch c.Dtype {
		case runtime.DFFloat64:
			nc.F64 = make([]float64, len(idx))
			for i, r := range idx {
				nc.F64[i] = c.F64[r]
			}
		case runtime.DFInt64:
			nc.I64 = make([]int64, len(idx))
			for i, r := range idx {
				nc.I64[i] = c.I64[r]
			}
		case runtime.DFString:
			nc.Str = make([]string, len(idx))
			for i, r := range idx {
				nc.Str[i] = c.Str[r]
			}
		default:
			nc.Bool = make([]bool, len(idx))
			for i, r := range idx {
				nc.Bool[i] = c.Bool[r]
			}
		}
		if c.Null != nil {
			for i, r := range idx {
				nc.Null[i] = c.Null[r]
			}
		}
		cols[ci] = nc
	}
	return &runtime.DataFrame{Cols: cols}
}

// dfSortIndices orders rows by one column, stable, nulls last.
func dfSortIndices(c *runtime.DFColumn, desc bool) []int {
	idx := make([]int, c.Len())
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ia, ib := idx[a], idx[b]
		if c.IsNull(ia) || c.IsNull(ib) {
			return !c.IsNull(ia) && c.IsNull(ib)
		}
		cmp, _ := dfCompareCells(c, c, 0)
		_ = cmp
		less := false
		switch c.Dtype {
		case runtime.DFFloat64:
			less = c.F64[ia] < c.F64[ib]
		case runtime.DFInt64:
			less = c.I64[ia] < c.I64[ib]
		case runtime.DFString:
			less = c.Str[ia] < c.Str[ib]
		default:
			less = !c.Bool[ia] && c.Bool[ib]
		}
		if desc {
			return !less && !(dfCellsEqual(c, ia, ib))
		}
		return less
	})
	return idx
}

func dfCellsEqual(c *runtime.DFColumn, a, b int) bool {
	switch c.Dtype {
	case runtime.DFFloat64:
		return c.F64[a] == c.F64[b]
	case runtime.DFInt64:
		return c.I64[a] == c.I64[b]
	case runtime.DFString:
		return c.Str[a] == c.Str[b]
	default:
		return c.Bool[a] == c.Bool[b]
	}
}

// dfGroupKey builds a composite hash key for the key columns at row i.
func dfGroupKey(cols []*runtime.DFColumn, i int) string {
	var sb strings.Builder
	for _, c := range cols {
		if c.IsNull(i) {
			sb.WriteString("\x00n")
		} else {
			switch c.Dtype {
			case runtime.DFFloat64:
				sb.WriteString(strconv.FormatFloat(c.F64[i], 'g', -1, 64))
			case runtime.DFInt64:
				sb.WriteString(strconv.FormatInt(c.I64[i], 10))
			case runtime.DFString:
				sb.WriteString(c.Str[i])
			default:
				sb.WriteString(strconv.FormatBool(c.Bool[i]))
			}
		}
		sb.WriteByte('\x1f')
	}
	return sb.String()
}

// dfAggregate computes one aggregation over the rows of a group.
func dfAggregate(c *runtime.DFColumn, rows []int, agg string) (runtime.Value, error) {
	switch agg {
	case "count":
		n := 0
		for _, r := range rows {
			if !c.IsNull(r) {
				n++
			}
		}
		return runtime.SmallInt{Value: int64(n)}, nil
	case "first", "last":
		pick := -1
		for _, r := range rows {
			if c.IsNull(r) {
				continue
			}
			pick = r
			if agg == "first" {
				break
			}
		}
		if pick < 0 {
			return runtime.Null{}, nil
		}
		return dfCellValue(c, pick), nil
	case "collect":
		var out []runtime.Value
		for _, r := range rows {
			out = append(out, dfCellValue(c, r))
		}
		if out == nil {
			out = []runtime.Value{}
		}
		return &runtime.List{Elements: out}, nil
	}
	if !dfNumeric(c) {
		return nil, fmt.Errorf("aggregation %q needs a numeric column, %s is %s", agg, c.Name, c.Dtype)
	}
	var m, m2 float64
	n := 0
	minV, maxV := math.Inf(1), math.Inf(-1)
	var sum float64
	for _, r := range rows {
		if c.IsNull(r) {
			continue
		}
		x := dfCellF64(c, r)
		n++
		sum += x
		d := x - m
		m += d / float64(n)
		m2 += d * (x - m)
		minV = math.Min(minV, x)
		maxV = math.Max(maxV, x)
	}
	if n == 0 {
		return runtime.Null{}, nil
	}
	switch agg {
	case "sum":
		if c.Dtype == runtime.DFInt64 {
			return runtime.SmallInt{Value: int64(sum)}, nil
		}
		return runtime.Float{Value: sum}, nil
	case "mean":
		return runtime.Float{Value: m}, nil
	case "min":
		if c.Dtype == runtime.DFInt64 {
			return runtime.SmallInt{Value: int64(minV)}, nil
		}
		return runtime.Float{Value: minV}, nil
	case "max":
		if c.Dtype == runtime.DFInt64 {
			return runtime.SmallInt{Value: int64(maxV)}, nil
		}
		return runtime.Float{Value: maxV}, nil
	case "std":
		if n < 2 {
			return runtime.Float{Value: 0}, nil
		}
		return runtime.Float{Value: math.Sqrt(m2 / float64(n-1))}, nil
	default:
		return nil, fmt.Errorf("unknown aggregation %q", agg)
	}
}

// dfCsvParse parses CSV text with a header row; types are inferred per
// column (int64 -> float64 -> bool -> string) unless overridden.
func dfCsvParse(text string, typeOverrides map[string]string) (*runtime.DataFrame, error) {
	reader := csv.NewReader(strings.NewReader(text))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv parse: %v", err)
	}
	if len(records) == 0 {
		return &runtime.DataFrame{}, nil
	}
	header := records[0]
	rows := records[1:]
	cols := make([]*runtime.DFColumn, len(header))
	for ci, name := range header {
		want := typeOverrides[name]
		raw := make([]string, len(rows))
		nulls := make([]bool, len(rows))
		hasNull := false
		for ri, rec := range rows {
			if ci >= len(rec) || rec[ci] == "" {
				nulls[ri] = true
				hasNull = true
				continue
			}
			raw[ri] = rec[ci]
		}
		dtype := want
		if dtype == "" {
			dtype = dfInferCsvType(raw, nulls)
		} else if dtype == "int" {
			dtype = runtime.DFInt64
		} else if dtype == "float" {
			dtype = runtime.DFFloat64
		}
		col := &runtime.DFColumn{Name: name, Dtype: dtype}
		if hasNull {
			col.Null = nulls
		}
		switch dtype {
		case runtime.DFInt64:
			col.I64 = make([]int64, len(rows))
			for ri, s := range raw {
				if nulls[ri] {
					continue
				}
				v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
				if err != nil {
					return nil, fmt.Errorf("csv column %q row %d: %q is not an int", name, ri+1, s)
				}
				col.I64[ri] = v
			}
		case runtime.DFFloat64:
			col.F64 = make([]float64, len(rows))
			for ri, s := range raw {
				if nulls[ri] {
					continue
				}
				v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
				if err != nil {
					return nil, fmt.Errorf("csv column %q row %d: %q is not a number", name, ri+1, s)
				}
				col.F64[ri] = v
			}
		case runtime.DFBool:
			col.Bool = make([]bool, len(rows))
			for ri, s := range raw {
				if nulls[ri] {
					continue
				}
				col.Bool[ri] = strings.EqualFold(strings.TrimSpace(s), "true")
			}
		default:
			col.Dtype = runtime.DFString
			col.Str = raw
		}
		cols[ci] = col
	}
	return &runtime.DataFrame{Cols: cols}, nil
}

func dfInferCsvType(raw []string, nulls []bool) string {
	allInt, allFloat, allBool := true, true, true
	seen := false
	for i, s := range raw {
		if nulls[i] {
			continue
		}
		seen = true
		t := strings.TrimSpace(s)
		if _, err := strconv.ParseInt(t, 10, 64); err != nil {
			allInt = false
		}
		if _, err := strconv.ParseFloat(t, 64); err != nil {
			allFloat = false
		}
		if !strings.EqualFold(t, "true") && !strings.EqualFold(t, "false") {
			allBool = false
		}
	}
	switch {
	case !seen:
		return runtime.DFString
	case allInt:
		return runtime.DFInt64
	case allFloat:
		return runtime.DFFloat64
	case allBool:
		return runtime.DFBool
	default:
		return runtime.DFString
	}
}

// dfCsvText renders the frame as CSV with a header row; nulls are
// empty cells.
func dfCsvText(frame *runtime.DataFrame) (string, error) {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	header := make([]string, len(frame.Cols))
	for i, c := range frame.Cols {
		header[i] = c.Name
	}
	if err := w.Write(header); err != nil {
		return "", err
	}
	rows := frame.Rows()
	rec := make([]string, len(frame.Cols))
	for r := 0; r < rows; r++ {
		for ci, c := range frame.Cols {
			if c.IsNull(r) {
				rec[ci] = ""
				continue
			}
			switch c.Dtype {
			case runtime.DFFloat64:
				rec[ci] = strconv.FormatFloat(c.F64[r], 'g', -1, 64)
			case runtime.DFInt64:
				rec[ci] = strconv.FormatInt(c.I64[r], 10)
			case runtime.DFString:
				rec[ci] = c.Str[r]
			default:
				rec[ci] = strconv.FormatBool(c.Bool[r])
			}
		}
		if err := w.Write(rec); err != nil {
			return "", err
		}
	}
	w.Flush()
	return sb.String(), w.Error()
}

// dfJsonRecords parses a JSON array of objects; key union, nulls for
// absent fields.
func dfJsonRecords(text string) (*runtime.DataFrame, error) {
	var records []map[string]any
	if err := json.Unmarshal([]byte(text), &records); err != nil {
		return nil, fmt.Errorf("json records parse: %v", err)
	}
	var order []string
	seen := map[string]bool{}
	for _, rec := range records {
		keys := make([]string, 0, len(rec))
		for k := range rec {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	cols := make([]*runtime.DFColumn, len(order))
	for ci, name := range order {
		vals := make([]runtime.Value, len(records))
		for ri, rec := range records {
			raw, ok := rec[name]
			if !ok || raw == nil {
				vals[ri] = runtime.Null{}
				continue
			}
			switch v := raw.(type) {
			case float64:
				if v == math.Trunc(v) && math.Abs(v) < 1e15 {
					vals[ri] = runtime.SmallInt{Value: int64(v)}
				} else {
					vals[ri] = runtime.Float{Value: v}
				}
			case string:
				vals[ri] = runtime.String{Value: v}
			case bool:
				vals[ri] = runtime.Bool{Value: v}
			default:
				b, _ := json.Marshal(v)
				vals[ri] = runtime.String{Value: string(b)}
			}
		}
		col, err := dfBuildColumn(name, vals)
		if err != nil {
			return nil, err
		}
		cols[ci] = col
	}
	return &runtime.DataFrame{Cols: cols}, nil
}

// dfJoin hash-joins on one key column; how is inner/left/right/outer.
func dfJoin(left, right *runtime.DataFrame, on, how string) (*runtime.DataFrame, error) {
	lc, ok := left.Column(on)
	if !ok {
		return nil, fmt.Errorf("join column %q missing from the left frame", on)
	}
	rc, ok := right.Column(on)
	if !ok {
		return nil, fmt.Errorf("join column %q missing from the right frame", on)
	}
	index := map[string][]int{}
	for i := 0; i < rc.Len(); i++ {
		if rc.IsNull(i) {
			continue
		}
		k := dfGroupKey([]*runtime.DFColumn{rc}, i)
		index[k] = append(index[k], i)
	}
	var leftIdx, rightIdx []int
	matchedRight := make([]bool, rc.Len())
	for i := 0; i < lc.Len(); i++ {
		var matches []int
		if !lc.IsNull(i) {
			matches = index[dfGroupKey([]*runtime.DFColumn{lc}, i)]
		}
		if len(matches) == 0 {
			if how == "left" || how == "outer" {
				leftIdx = append(leftIdx, i)
				rightIdx = append(rightIdx, -1)
			}
			continue
		}
		for _, r := range matches {
			matchedRight[r] = true
			leftIdx = append(leftIdx, i)
			rightIdx = append(rightIdx, r)
		}
	}
	if how == "right" || how == "outer" {
		for r := 0; r < rc.Len(); r++ {
			if !matchedRight[r] {
				leftIdx = append(leftIdx, -1)
				rightIdx = append(rightIdx, r)
			}
		}
	}
	out := &runtime.DataFrame{}
	appendSide := func(frame *runtime.DataFrame, idx []int, skip string, suffix string) {
		for _, c := range frame.Cols {
			if c.Name == skip {
				continue
			}
			name := c.Name
			if _, exists := out.Column(name); exists {
				name += suffix
			}
			vals := make([]runtime.Value, len(idx))
			for i, r := range idx {
				if r < 0 {
					vals[i] = runtime.Null{}
				} else {
					vals[i] = dfCellValue(c, r)
				}
			}
			col, _ := dfBuildColumn(name, vals)
			out.Cols = append(out.Cols, col)
		}
	}
	keyVals := make([]runtime.Value, len(leftIdx))
	for i := range leftIdx {
		if leftIdx[i] >= 0 {
			keyVals[i] = dfCellValue(lc, leftIdx[i])
		} else {
			keyVals[i] = dfCellValue(rc, rightIdx[i])
		}
	}
	keyCol, _ := dfBuildColumn(on, keyVals)
	out.Cols = append(out.Cols, keyCol)
	appendSide(left, leftIdx, on, "_left")
	appendSide(right, rightIdx, on, "_right")
	return out, nil
}

// dfPivot spreads `columns` values into new columns, one row per
// distinct `index` value, aggregating `values` per cell. Rows with a
// null index or columns cell are skipped; empty cells are null.
func dfPivot(frame *runtime.DataFrame, opts runtime.Dict) (*runtime.DataFrame, error) {
	str := func(key string) (string, error) {
		v, ok := ndDictValue(opts, key)
		if !ok {
			return "", fmt.Errorf("dataframe.pivot requires opts.%s", key)
		}
		s, ok := v.(runtime.String)
		if !ok {
			return "", fmt.Errorf("dataframe.pivot opts.%s must be a string", key)
		}
		return s.Value, nil
	}
	indexName, err := str("index")
	if err != nil {
		return nil, err
	}
	columnsName, err := str("columns")
	if err != nil {
		return nil, err
	}
	valuesName, err := str("values")
	if err != nil {
		return nil, err
	}
	agg := "sum"
	if v, ok := ndDictValue(opts, "agg"); ok {
		s, ok := v.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("dataframe.pivot opts.agg must be a string")
		}
		agg = s.Value
	}
	if agg == "collect" {
		return nil, fmt.Errorf("dataframe.pivot does not support agg \"collect\"")
	}
	idxCol, ok := frame.Column(indexName)
	if !ok {
		return nil, fmt.Errorf("unknown column %q", indexName)
	}
	colCol, ok := frame.Column(columnsName)
	if !ok {
		return nil, fmt.Errorf("unknown column %q", columnsName)
	}
	valCol, ok := frame.Column(valuesName)
	if !ok {
		return nil, fmt.Errorf("unknown column %q", valuesName)
	}
	type cellKey struct{ idx, col string }
	var idxOrder, colOrder []string
	idxFirstRow := map[string]int{}
	colDisplay := map[string]string{}
	cellRows := map[cellKey][]int{}
	for i := 0; i < idxCol.Len(); i++ {
		if idxCol.IsNull(i) || colCol.IsNull(i) {
			continue
		}
		ik := dfGroupKey([]*runtime.DFColumn{idxCol}, i)
		ck := dfGroupKey([]*runtime.DFColumn{colCol}, i)
		if _, seen := idxFirstRow[ik]; !seen {
			idxFirstRow[ik] = i
			idxOrder = append(idxOrder, ik)
		}
		if _, seen := colDisplay[ck]; !seen {
			colDisplay[ck] = dfCellDisplay(colCol, i)
			colOrder = append(colOrder, ck)
		}
		key := cellKey{idx: ik, col: ck}
		cellRows[key] = append(cellRows[key], i)
	}
	out := &runtime.DataFrame{}
	idxVals := make([]runtime.Value, len(idxOrder))
	for gi, ik := range idxOrder {
		idxVals[gi] = dfCellValue(idxCol, idxFirstRow[ik])
	}
	built, err := dfBuildColumn(indexName, idxVals)
	if err != nil {
		return nil, err
	}
	out.Cols = append(out.Cols, built)
	for _, ck := range colOrder {
		vals := make([]runtime.Value, len(idxOrder))
		for gi, ik := range idxOrder {
			rows, has := cellRows[cellKey{idx: ik, col: ck}]
			if !has {
				vals[gi] = runtime.Null{}
				continue
			}
			v, err := dfAggregate(valCol, rows, agg)
			if err != nil {
				return nil, err
			}
			vals[gi] = v
		}
		built, err := dfBuildColumn(colDisplay[ck], vals)
		if err != nil {
			return nil, err
		}
		out.Cols = append(out.Cols, built)
	}
	return out, nil
}

// dfCellDisplay renders a cell as a pivot column name.
func dfCellDisplay(c *runtime.DFColumn, i int) string {
	switch v := dfCellValue(c, i).(type) {
	case runtime.String:
		return v.Value
	case runtime.SmallInt:
		return strconv.FormatInt(v.Value, 10)
	case runtime.Bool:
		return strconv.FormatBool(v.Value)
	case runtime.Float:
		return strconv.FormatFloat(v.Value, 'g', -1, 64)
	default:
		return "null"
	}
}
