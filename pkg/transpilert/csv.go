package transpilert

import (
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
)

// CSV helpers mirror internal/native csv-module functions over encoding/csv
// (stdlib), so --native matches the interpreter byte-for-byte (quoting, field
// splitting, delimiter/trimSpace options) and stays zero-dep.

type csvOptions struct {
	delimiter rune
	trimSpace bool
}

func csvOptionsFrom(opts []*OrderedDict[string, any]) csvOptions {
	out := csvOptions{}
	if len(opts) == 0 || opts[0] == nil {
		return out
	}
	if v, ok := opts[0].Get("delimiter"); ok {
		s, ok := v.(string)
		if !ok || len(s) == 0 {
			panic(NewError("RuntimeError", "delimiter must be a non-empty string"))
		}
		out.delimiter = []rune(s)[0]
	}
	if v, ok := opts[0].Get("trimSpace"); ok {
		b, ok := v.(bool)
		if !ok {
			panic(NewError("RuntimeError", "trimSpace must be bool"))
		}
		out.trimSpace = b
	}
	return out
}

func csvReadAll(text string, opts csvOptions) [][]string {
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	if opts.delimiter != 0 {
		reader.Comma = opts.delimiter
	}
	reader.TrimLeadingSpace = opts.trimSpace
	rows, err := reader.ReadAll()
	if err != nil {
		panic(NewError("RuntimeError", fmt.Sprintf("csv parse: %v", err)))
	}
	return rows
}

func CSVParse(text string, opts ...*OrderedDict[string, any]) [][]string {
	return csvReadAll(text, csvOptionsFrom(opts))
}

// CSVParseDict treats the first row as headers; each later row becomes a dict
// keyed by header. Header keys are inserted sorted to match the interpreter's
// nil-Order dict rendering. Missing trailing cells default to "".
func CSVParseDict(text string, opts ...*OrderedDict[string, any]) []*OrderedDict[string, string] {
	rows := csvReadAll(text, csvOptionsFrom(opts))
	if len(rows) == 0 {
		return []*OrderedDict[string, string]{}
	}
	headers := rows[0]
	order := make([]int, len(headers))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return headers[order[a]] < headers[order[b]] })
	out := make([]*OrderedDict[string, string], 0, len(rows)-1)
	for _, row := range rows[1:] {
		d := NewOrderedDict[string, string]()
		for _, idx := range order {
			val := ""
			if idx < len(row) {
				val = row[idx]
			}
			d.Set(headers[idx], val)
		}
		out = append(out, d)
	}
	return out
}

// CSVStringify writes string rows via encoding/csv.
func CSVStringify(rows [][]string, opts ...*OrderedDict[string, any]) string {
	options := csvOptionsFrom(opts)
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if options.delimiter != 0 {
		w.Comma = options.delimiter
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			panic(NewError("RuntimeError", fmt.Sprintf("csv.stringify: %v", err)))
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		panic(NewError("RuntimeError", fmt.Sprintf("csv.stringify: %v", err)))
	}
	return buf.String()
}
