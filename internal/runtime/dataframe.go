package runtime

import (
	"fmt"
	"strings"
)

// DFColumn dtype names.
const (
	DFFloat64 = "float64"
	DFInt64   = "int64"
	DFString  = "string"
	DFBool    = "bool"
)

// DFColumn is one named, typed column with an optional null mask.
// Columns are immutable once owned by a frame; verbs build new columns.
type DFColumn struct {
	Name  string
	Dtype string
	F64   []float64
	I64   []int64
	Str   []string
	Bool  []bool
	Null  []bool
}

// Len returns the row count.
func (c *DFColumn) Len() int {
	switch c.Dtype {
	case DFFloat64:
		return len(c.F64)
	case DFInt64:
		return len(c.I64)
	case DFString:
		return len(c.Str)
	default:
		return len(c.Bool)
	}
}

// IsNull reports the mask at row i (nil mask = no nulls).
func (c *DFColumn) IsNull(i int) bool {
	return c.Null != nil && c.Null[i]
}

// DataFrame is an ordered set of equal-length columns. Verbs are
// immutable: derived frames share untouched column pointers.
type DataFrame struct {
	Cols []*DFColumn
}

func (v *DataFrame) TypeName() string { return "dataframe.DataFrame" }

func (v *DataFrame) Inspect() string {
	names := make([]string, len(v.Cols))
	for i, c := range v.Cols {
		names[i] = c.Name
	}
	return fmt.Sprintf("<dataframe %d x %d [%s]>", v.Rows(), len(v.Cols), strings.Join(names, ", "))
}

// Rows returns the row count (0 for an empty frame).
func (v *DataFrame) Rows() int {
	if len(v.Cols) == 0 {
		return 0
	}
	return v.Cols[0].Len()
}

// Column finds a column by name.
func (v *DataFrame) Column(name string) (*DFColumn, bool) {
	for _, c := range v.Cols {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}

// DFSeries is a named 1-D column view handed out by df.col(name).
type DFSeries struct {
	Col *DFColumn
}

func (v *DFSeries) TypeName() string { return "dataframe.Series" }
func (v *DFSeries) Inspect() string {
	return fmt.Sprintf("<series %s %s [%d]>", v.Col.Name, v.Col.Dtype, v.Col.Len())
}

// DFExpr is a columnwise expression tree built by dataframe.col/lit
// and combined with comparison/arithmetic/logic methods; filter and
// withColumn evaluate it against a frame in Go.
type DFExpr struct {
	Kind  string // "col", "lit", "bin", "not", "isNull"
	Col   string
	Lit   Value
	Op    string // gt lt gte lte eq ne and or add sub mul div
	Left  *DFExpr
	Right *DFExpr
}

func (v *DFExpr) TypeName() string { return "dataframe.Expr" }
func (v *DFExpr) Inspect() string  { return "<dataframe.Expr " + v.Kind + ">" }

// DFGroupBy is the intermediate produced by df.groupBy(keys), consumed
// by .agg(spec).
type DFGroupBy struct {
	Frame *DataFrame
	Keys  []string
}

func (v *DFGroupBy) TypeName() string { return "dataframe.GroupBy" }
func (v *DFGroupBy) Inspect() string {
	return "<dataframe.GroupBy " + strings.Join(v.Keys, ", ") + ">"
}
