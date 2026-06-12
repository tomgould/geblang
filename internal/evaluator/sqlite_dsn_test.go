package evaluator

import "testing"

func TestSqliteDSNWithBusyTimeout(t *testing.T) {
	cases := map[string]string{
		":memory:":                         ":memory:",
		"/data/app.db":                     "file:/data/app.db?_pragma=busy_timeout(5000)",
		"app.db?mode=ro":                   "file:app.db?mode=ro&_pragma=busy_timeout(5000)",
		"file:app.db":                      "file:app.db?_pragma=busy_timeout(5000)",
		"file:app.db?cache=shared":         "file:app.db?cache=shared&_pragma=busy_timeout(5000)",
		"app.db?_pragma=busy_timeout(100)": "app.db?_pragma=busy_timeout(100)",
	}
	for in, want := range cases {
		if got := sqliteDSNWithBusyTimeout(in); got != want {
			t.Errorf("dsn %q: got %q want %q", in, got, want)
		}
	}
}
