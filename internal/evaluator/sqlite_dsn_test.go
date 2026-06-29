package evaluator

import "testing"

func TestSqliteDSNWithDefaults(t *testing.T) {
	const p = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	cases := map[string]string{
		":memory:":                         ":memory:",
		"/data/app.db":                     "file:/data/app.db?" + p,
		"app.db?mode=ro":                   "file:app.db?mode=ro&" + p,
		"file:app.db":                      "file:app.db?" + p,
		"file:app.db?cache=shared":         "file:app.db?cache=shared&" + p,
		"app.db?_pragma=busy_timeout(100)": "app.db?_pragma=busy_timeout(100)",
	}
	for in, want := range cases {
		if got := sqliteDSNWithDefaults(in); got != want {
			t.Errorf("dsn %q: got %q want %q", in, got, want)
		}
	}
}
